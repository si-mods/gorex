// Package webui serves the browser client of the Rex API gateway: the
// files web/build.mjs writes into dist, embedded in the binary. A
// checkout that never built the client embeds no client, and its page
// says how to build one.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var files embed.FS

// Handler serves the client at / and its assets beside it.
func Handler() http.Handler {
	dist, _ := fs.Sub(files, "dist")
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return http.HandlerFunc(notBuilt)
	}
	static := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// The files change with each build of the binary, and are small.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		static.ServeHTTP(w, r)
	})
}

// notBuilt answers in place of a client that was not built.
func notBuilt(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("<!doctype html>\n<meta charset=\"utf-8\">\n<title>GoRex</title>\n" +
		"<p>This GoRex was built without its web client. Build it with <code>node web/build.mjs</code>, " +
		"then build GoRex again (docs/web-client.md).</p>\n"))
}
