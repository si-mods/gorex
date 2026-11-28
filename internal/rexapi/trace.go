package rexapi

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// tracing, set by GOREX_REXAPI_TRACE, logs the wire: every HTTP request,
// every message of a control connection both ways, data handshakes, and
// why each connection ended. It is for finding out what a client does
// that the gateway does not expect; it logs reconnect tokens and tickets,
// so it is off by default.
var tracing = newTracing()

func newTracing() *atomic.Bool {
	var b atomic.Bool
	b.Store(os.Getenv("GOREX_REXAPI_TRACE") != "")
	return &b
}

// tracef logs a trace line when tracing.
func tracef(format string, args ...any) {
	if tracing.Load() {
		logf("trace: "+format, args...)
	}
}

// clip shortens a message for the trace.
func clip(b []byte) string {
	const most = 4000
	if len(b) > most {
		return string(b[:most]) + "…(" + strconv.Itoa(len(b)) + " bytes)"
	}
	return string(b)
}

// traceRequests logs each request's line and the headers that say what
// the client asks for.
func traceRequests(next http.Handler) http.Handler {
	if !tracing.Load() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var h []string
		for _, k := range []string{"Upgrade", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions", "User-Agent", "Origin", "Authorization"} {
			if v := r.Header.Get(k); v != "" {
				if k == "Authorization" {
					v = "(set)"
				}
				h = append(h, k+"="+v)
			}
		}
		tracef("http %s %s %s from %s [%s]", r.Method, r.URL.RequestURI(), r.Proto, r.RemoteAddr, strings.Join(h, " "))
		next.ServeHTTP(w, r)
	})
}
