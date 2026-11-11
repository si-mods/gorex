package rexapi

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
)

// Handler returns the gateway's HTTP endpoints: the control and data
// connections, uploads, the help document and this API's own pages. A
// request from a web page whose origin the gateway does not trust is
// refused on every path, before the access rule (checkOrigin).
func (g *Gateway) Handler() http.Handler {
	if relay := relayHandler(); relay != nil {
		return relay
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control", g.handleControl)
	mux.HandleFunc("/api/data", g.handleData)
	mux.HandleFunc("/api/upload", g.handleUpload)
	mux.HandleFunc("/api/spaces", g.handleSpaces)
	mux.HandleFunc("/api/help", serveHelp)
	mux.HandleFunc("/llms.txt", serveLLMs)
	mux.HandleFunc("/llms.html", serveLLMsHTML)
	return traceRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.checkOrigin(w, r) {
			return
		}
		if g.opt.Access != nil {
			if err := g.opt.Access.Allow(r); err != nil {
				tracef("refused %s: %v", r.URL.Path, err)
				writeRefusal(w, err.Error())
				return
			}
		}
		if preflight(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

// writeRefusal answers a request the access rules refuse, as Rex does.
func writeRefusal(w http.ResponseWriter, message string) {
	writeError(w, http.StatusForbidden, message)
}

// writeError answers a request that failed with status, in the shape of a
// refusal.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// serveHelp serves the API's help document, the same one help.all
// returns without its http field.
func serveHelp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(helpJSON)
}

// serveLLMs serves the API documentation as Markdown, with the revision
// 11 and 12 supplements appended, since the embedded document is
// revision 10's.
func serveLLMs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(llmsText)
	w.Write(llmsRev11)
	w.Write(llmsRev12)
}

// llmsRev11 documents what revision 11 adds, which the revision 10
// document the server embeds predates.
var llmsRev11 = []byte(`

## Revision 11: Terminal Uploads

Revision 11 adds terminal uploads.

### com.superlogical.terminal.upload_begin

Reserve a file to upload to the terminal's machine, such as one the user dropped on the terminal,
and get a ticket for the ` + "`rex.data.bytes.v1`" + ` data stream that carries its bytes. Once every file of the batch has
arrived, the terminal types their paths as if the user had pasted them.

- ` + "`ref`" + ` (string, required): The upload's ref, ` + "`upload:`" + ` followed by a new UUID in lower-case canonical form.
- ` + "`name`" + ` (string, required): The file's name, which is its last path component only.
- ` + "`size`" + ` (integer, required): The file's exact size in bytes. A file larger than the server takes fails with ` + "`too_large`" + `.
- ` + "`batch`" + ` (string): An ID the client chooses for a group of files whose paths are typed together.
- ` + "`index`" + ` (integer): The file's position in its batch, from 0. Needs batch.
- ` + "`count`" + ` (integer): How many files the batch has. Required with batch.

The result has ` + "`ticket`" + ` and ` + "`expires_in_ms`" + `, as data.connect's does. Open a data stream with the
` + "`rex.data.bytes.v1`" + ` subprotocol and that ticket, send the file's bytes as binary messages, and half-close the
stream with one empty binary message. The terminal ends each stream with a close code: 1000 once its file is
whole — the stream of the batch's last file is the one whose close follows the batch's paths being typed.
Reserving a
file that is already reserved, or more than the terminal allows at once, fails with ` + "`conflict`" + `; a file
or batch over the server's limits fails with ` + "`too_large`" + `. A batch a file of which failed is aborted,
and its reservations are gone, so start a new batch with a new ID.

### block.event wheel and pointer payloads

A wheel event's payload is now its deltas: ` + "`delta_mode`" + ` (pixel, line, or page), ` + "`delta_x`" + `, ` + "`delta_y`" + `, and
` + "`delta_z`" + ` (movement on a third axis, for devices that have one), beside ` + "`mods`" + ` and the cell. Positive
deltas scroll down and right. A pointer event may carry ` + "`pixel`" + `, the position in pixels from the
block's top-left corner, or null when only the cell is known, and ` + "`buttons`" + `, the buttons held down
after the event, whose motion with a button held is a drag.
`)

// llmsRev12 documents what revision 12 adds. Its prose comes from Rex
// 0.1.0-preview.14's revision note and schema.
var llmsRev12 = []byte(`
## Revision 12: Server Listeners

Revision 12 adds the endpoints the server accepts connections on. The server speaks API revisions 0 through 12.

### server.status listeners

The result of ` + "`server.status`" + ` has a new member, ` + "`listeners`" + `: each endpoint the server accepts connections
on, the primary endpoint first, then the extra listeners in the order they were configured. The
` + "`server_status`" + ` member of the ` + "`session.bootstrap`" + ` result is the same object, so it has the list too. The list
doesn't include built-in Tailscale, which can start and stop while the server runs; the ` + "`tailscale`" + `
member describes it. Servers older than API revision 12 leave the list out, and a missing list means
unknown; a client of an older revision doesn't get it.

- ` + "`endpoint`" + ` (string, required): The endpoint the listener was configured with, such as
  ` + "`unix:///run/rex.sock`" + `, ` + "`127.0.0.1:7297`" + `, or ` + "`tailnet://:7297`" + `.
- ` + "`addresses`" + ` (array of strings): The addresses the listener is bound to, such as a socket path or
  ` + "`127.0.0.1:54321`" + `. They show the port that an endpoint like ` + "`127.0.0.1:0`" + ` got. Left out when the server can't tell.
- ` + "`transport`" + ` (string, required): How the listener identifies its callers: ` + "`unix`" + `, ` + "`named_pipe`" + `, ` + "`tcp`" + `, or
  ` + "`tailscale`" + `. Show a value you don't know as it is.
- ` + "`admission`" + ` (string): Which callers the listener lets in: ` + "`peer_credentials`" + `, ` + "`anonymous`" + `,
  ` + "`tailscale_owner`" + `, ` + "`tailscale_identified`" + `, or ` + "`login_server`" + `. Left out when the server can't tell. Show a
  value you don't know as it is. A TCP listener whose admission is ` + "`anonymous`" + ` gives full access to anyone
  who can reach it.
- ` + "`primary`" + ` (boolean): True for the server's primary endpoint, where commands such as ` + "`rex server status`" + ` and
  ` + "`rex server stop`" + ` look for the server. Left out for the other listeners. Exactly one listener is primary.

Example:

    "listeners": [{"endpoint": "unix:///run/user/1000/rex/server.sock", "addresses": ["/run/user/1000/rex/server.sock"], "transport": "unix", "admission": "peer_credentials", "primary": true}, {"endpoint": "127.0.0.1:0", "addresses": ["127.0.0.1:52061"], "transport": "tcp", "admission": "anonymous"}]
`)

// serveLLMsHTML serves the documentation in a simple HTML wrapper.
func serveLLMsHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html>\n<meta charset=\"utf-8\">\n<title>Rex API</title>\n<pre>")
	io.WriteString(w, html.EscapeString(string(llmsText)))
	io.WriteString(w, html.EscapeString(string(llmsRev11)))
	io.WriteString(w, html.EscapeString(string(llmsRev12)))
	io.WriteString(w, "</pre>\n")
}
