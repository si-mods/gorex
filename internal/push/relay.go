package push

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client posts requests to the push relay (NET-003, Relay API), each
// signed with the host's push key.
type Client struct {
	// URL is the relay's base, such as https://relay.gorex.uk.
	URL string
	Key *Keypair
	// HTTP is the client to post with; nil uses http.DefaultClient.
	HTTP *http.Client
}

// Alert is a push's alert, for a push that wakes the app.
type Alert struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// PushRequest is a POST /v1/push's body.
type PushRequest struct {
	// Device is the phone's device key, base64.
	Device string `json:"device"`
	// Event is "update" or "end".
	Event        string       `json:"event"`
	ContentState ContentState `json:"content_state"`
	// StaleDate and DismissalDate are unix seconds, left out when zero.
	StaleDate     int64  `json:"stale_date,omitempty"`
	DismissalDate int64  `json:"dismissal_date,omitempty"`
	Alert         *Alert `json:"alert,omitempty"`
	// Priority is the APNs priority: 5 for a coalesced update, 10 for one
	// the user waits on.
	Priority int `json:"priority"`
}

// Push posts a push to /v1/push, signed.
func (c *Client) Push(ctx context.Context, req PushRequest) error {
	return c.Post(ctx, "/v1/push", req)
}

// Post posts body as JSON to the relay, the signature of the raw bytes
// in X-Gorex-Sig. An answer of {"error": ...} or a non-200 status is an
// error.
func (c *Client) Post(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	sig, err := c.Key.Sign(data)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gorex-Sig", base64.StdEncoding.EncodeToString(sig))
	h := c.HTTP
	if h == nil {
		h = &http.Client{}
	}
	res, err := h.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var r struct {
		Error string `json:"error"`
	}
	json.Unmarshal(answer, &r)
	if r.Error != "" {
		return fmt.Errorf("push: the relay answered %q", r.Error)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("push: the relay answered %s", res.Status)
	}
	return nil
}
