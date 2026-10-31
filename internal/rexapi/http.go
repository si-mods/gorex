package rexapi

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
)

// Handler returns the gateway's HTTP endpoints: the control and data
// connections, the help document, and this API's own pages.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control", g.handleControl)
	mux.HandleFunc("/api/data", g.handleData)
	mux.HandleFunc("/api/help", serveHelp)
	mux.HandleFunc("/llms.txt", serveLLMs)
	mux.HandleFunc("/llms.html", serveLLMsHTML)
	if g.opt.Access == nil {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := g.opt.Access.Allow(r); err != nil {
			writeRefusal(w, err.Error())
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// writeRefusal answers a request the access rules refuse, as Rex does.
func writeRefusal(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// serveHelp serves the API's help document, the same one help.all
// returns without its http field.
func serveHelp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(helpJSON)
}

// serveLLMs serves the API documentation as Markdown.
func serveLLMs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(llmsText)
}

// serveLLMsHTML serves the documentation in a simple HTML wrapper.
func serveLLMsHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html>\n<meta charset=\"utf-8\">\n<title>Rex API</title>\n<pre>")
	io.WriteString(w, html.EscapeString(string(llmsText)))
	io.WriteString(w, "</pre>\n")
}
