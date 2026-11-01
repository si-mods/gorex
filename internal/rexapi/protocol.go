package rexapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// The constants of the protocol this package speaks.
const (
	// controlProtocol is the WebSocket subprotocol of a control connection.
	controlProtocol = "rex.control.v1"
	// terminalProtocol is the subprotocol of a terminal's data connection.
	terminalProtocol = "rex.data.terminal.v1"
	// dataTerminal is the data_ref_id of a terminal block's stream.
	dataTerminal = "terminal"
	// apiRevision is the newest revision of Rex's API this package serves.
	apiRevision = 7
	// apiMinRevision is the oldest revision it serves. Shapes are Rex's
	// revision 7 ones; revision 0 through 6 clients see the same fields,
	// which newer clients ignore.
	apiMinRevision = 0
	// maxMessage is the largest message a control connection sends or
	// accepts, in bytes.
	maxMessage = 1 << 20
	// maxHello is the largest control hello, as Rex's clients keep it.
	maxHello = 512
	// maxChunk is the largest binary message of a data connection.
	maxChunk = 64 << 10
	// eventBacklog is how many bytes of events a control connection may
	// fall behind before the server closes it.
	eventBacklog = 8 << 20
)

// apiError is a failed request, in Rex's shape: a code to branch on, a
// message for people, and an HTTP status with the same meaning.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

// errorf makes an apiError.
func errorf(code string, status int, format string, args ...any) *apiError {
	return &apiError{Code: code, Message: fmt.Sprintf(format, args...), Status: status}
}

// The error codes of Rex's Errors table that this server answers with.
func errBadRequest(format string, args ...any) *apiError {
	return errorf("bad_request", 400, format, args...)
}

func errNotFound(format string, args ...any) *apiError {
	return errorf("not_found", 404, format, args...)
}

func errMethodNotFound(format string, args ...any) *apiError {
	return errorf("method_not_found", 404, format, args...)
}

func errSessionNotFound(format string, args ...any) *apiError {
	return errorf("session_not_found", 404, format, args...)
}

func errConflict(format string, args ...any) *apiError {
	return errorf("conflict", 409, format, args...)
}

func errNotSupported(format string, args ...any) *apiError {
	return errorf("not_supported", 400, format, args...)
}

func errTooLarge(format string, args ...any) *apiError {
	return errorf("too_large", 413, format, args...)
}

func errInternal(format string, args ...any) *apiError {
	return errorf("internal", 500, format, args...)
}

// unsupportedMethod is what a method Rex documents, but this server does
// not do, is answered with. not_supported (400) is Rex's code for a
// method the target can't perform now, and the one whose meaning comes
// closest to "this server does not do this": method_not_found would tell
// a client to update its server, which would not help.
func unsupportedMethod(method string) *apiError {
	return errNotSupported("GoRex does not support %s", method)
}

// orderedMethods run one at a time, in the order they arrive on a
// connection, as Rex's "Order of Requests" says. This server implements
// none of them, but the rule is kept for the methods it will add.
var orderedMethods = map[string]bool{
	"block.close": true, "block.event": true, "client.action": true, "client.key": true,
	"session.close_layer": true, "session.close_window": true, "session.detach_block": true,
	"session.disown_block": true, "session.float_block": true, "session.focus_block": true,
	"session.focus_direction": true, "session.focus_next_pane": true, "session.focus_next_window": true,
	"session.focus_previous_pane": true, "session.focus_previous_window": true, "session.focus_window": true,
	"session.move_block": true, "session.move_window": true, "session.new_layer": true,
	"session.new_split": true, "session.new_window": true, "session.own_block": true,
	"session.swap_blocks": true, "session.zoom_block": true,
}

// The API's own documents, served at /api/help, /llms.txt and
// /llms.html, and described by help.all.
var (
	//go:embed testdata/rex-api-help.json
	helpJSON []byte
	//go:embed testdata/rex-llms.txt
	llmsText []byte
)

// helpDoc is the parsed help document: every method the API documents,
// including the ones this server does not implement.
var helpDoc = sync.OnceValue(func() map[string]json.RawMessage {
	var doc map[string]json.RawMessage
	json.Unmarshal(helpJSON, &doc)
	return doc
})

// knownMethods is every method in the help document, including block
// methods, so that a request for one is answered with not_supported
// rather than method_not_found.
var knownMethods = sync.OnceValue(func() map[string]bool {
	var doc struct {
		Methods []struct {
			Name string `json:"name"`
		} `json:"methods"`
		BlockTypes []struct {
			Methods []struct {
				Name   string `json:"name"`
				Invoke struct {
					Method string `json:"method"`
				} `json:"invoke"`
			} `json:"methods"`
		} `json:"block_types"`
	}
	json.Unmarshal(helpJSON, &doc)
	known := make(map[string]bool, len(doc.Methods))
	for _, m := range doc.Methods {
		known[m.Name] = true
	}
	// Block methods are named by their creator and the method, which the
	// invoke field spells in full.
	for _, b := range doc.BlockTypes {
		for _, m := range b.Methods {
			if m.Invoke.Method != "" {
				known[m.Invoke.Method] = true
			}
		}
	}
	return known
})

// request is one message from a client, decoded far enough to tell its
// kind and to validate the fields every request has.
type request struct {
	ID      string
	Method  string
	Payload json.RawMessage
	// seq is the number of the last event queued for the connection when
	// the request arrived, which its response carries.
	seq uint64
}

// response is an answer to a request, in Rex's shape.
type response struct {
	Type    string    `json:"type"`
	ID      string    `json:"id"`
	Seq     uint64    `json:"seq"`
	Payload any       `json:"payload,omitempty"`
	Error   *apiError `json:"error,omitempty"`
}

// eventMessage is an event sent without a request.
type eventMessage struct {
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	Payload any    `json:"payload"`
}

// innerEvent is an event's own name and fields.
type innerEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// sessionEvent wraps an event of one session with the session it came
// from.
type sessionEvent struct {
	Type    string    `json:"type"`
	Payload eventBody `json:"payload"`
}

type eventBody struct {
	SessionID string     `json:"session_id"`
	Event     innerEvent `json:"event"`
}

// sessionListEntry is a session as session.list reports it.
type sessionListEntry struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
}

// sessionCreated is session_created's payload.
type sessionCreated struct {
	Session sessionListEntry `json:"session"`
}

// sessionID is session_destroyed's payload.
type sessionIDPayload struct {
	SessionID string `json:"session_id"`
}

// labelPayload is session_label_changed's payload.
type labelPayload struct {
	Label string `json:"label"`
}

// windowPayload is window_created's and window_label_changed's payload.
type windowPayload struct {
	WindowID string `json:"window_id"`
	Label    string `json:"label"`
}

// windowIDPayload is window_closed's payload.
type windowIDPayload struct {
	WindowID string `json:"window_id"`
}

// activeWindowPayload is active_window_changed's payload.
type activeWindowPayload struct {
	WindowID string `json:"window_id,omitempty"`
}

// blockIDPayload is block_created's and block_closed's payload.
type blockIDPayload struct {
	BlockID string `json:"block_id"`
}

// revisionPayload is session_view_changed's payload.
type revisionPayload struct {
	Revision uint64 `json:"revision"`
}

// clientIDPayload names a client in an event.
type clientIDPayload struct {
	ClientID string `json:"client_id"`
}
