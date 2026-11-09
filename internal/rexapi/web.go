package rexapi

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path"
	"time"
)

// Only browser runtime assets are embedded; development files and dependency
// metadata never become public endpoints. There is no runtime CDN dependency.
//
//go:embed web/index.html web/app.css web/app.js web/core.js web/vendor/*
var webFiles embed.FS

func webAsset(p string) string {
	switch p {
	case "/", "/index.html":
		return "web/index.html"
	case "/web/app.css", "/web/app.js", "/web/core.js",
		"/web/vendor/xterm.js", "/web/vendor/xterm.css", "/web/vendor/addon-fit.js", "/web/vendor/addon-unicode11.js",
		"/web/vendor/LICENSE.xterm.txt", "/web/vendor/LICENSE.addon-fit.txt", "/web/vendor/LICENSE.addon-unicode11.txt":
		return p[1:]
	}
	return ""
}

func isWebAsset(p string) bool { return webAsset(p) != "" }

func serveWeb(w http.ResponseWriter, r *http.Request) {
	name := webAsset(r.URL.Path)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, 405, "browser assets are GET or HEAD")
		return
	}
	data, err := webFiles.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	if name == "web/index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", fmt.Sprintf("\"%x\"", sha256.Sum256(data)))
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (g *Gateway) handleWebSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "session information is a GET")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	p := principalOf(r)
	json.NewEncoder(w).Encode(map[string]any{"host": g.hello.Host.Name, "transport": transportOf(r), "device": p["display"]})
}

func (g *Gateway) handleWebLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "logout is a POST")
		return
	}
	if transportOf(r) != transportPublic || g.public == nil {
		writeRefusal(w, "this connection has no browser pairing")
		return
	}
	id, _ := principalOf(r)["device_id"].(string)
	c, _ := r.Context().Value(connKey{}).(*trackedConn)
	if err := g.public.revokeExcept(id, c); err != nil {
		writeError(w, 500, "could not revoke this browser")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: deviceCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
