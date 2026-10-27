package coord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client speaks the coordinator's protocol, pinning the coordinator by
// the fingerprint of its join URL instead of trusting an authority.
// The network key it holds may run commands on every member, so it is
// never logged.
type Client struct {
	host        string // host:port
	fingerprint string // 16 hex digits pinning the certificate
	key         string // the network key
	http        *http.Client
}

// NewClient returns a client for a join URL and its network key.
func NewClient(joinURL, networkKey string) (*Client, error) {
	host, fp, err := SplitJoinURL(joinURL)
	if err != nil {
		return nil, err
	}
	if networkKey == "" {
		return nil, errors.New("coord: the network key is empty")
	}
	return &Client{
		host:        host,
		fingerprint: fp,
		key:         networkKey,
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: pinTLS(fp)},
		},
	}, nil
}

// pinTLS verifies exactly one certificate, whose SHA-256 begins with
// the fingerprint; anything else, including a certificate authority's
// word, fails.
func pinTLS(fingerprint string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // the pin below is the verification
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) != 1 {
				return errors.New("coord: the coordinator's certificate does not match its fingerprint")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:])[:len(fingerprint)] != fingerprint {
				return errors.New("coord: the coordinator's certificate does not match its fingerprint")
			}
			return nil
		},
	}
}

// directory is the answer of join and members.
type directory struct {
	Revision int      `json:"revision"`
	You      string   `json:"you"`
	Members  []Member `json:"members"`
}

// Join adds or refreshes this machine and returns the network's
// members.
func (c *Client) Join(ctx context.Context, name, nodeKey, address string) ([]Member, int, error) {
	var d directory
	err := c.call(ctx, "POST", "/v1/join", map[string]string{
		"name": name, "nodeKey": nodeKey, "address": address,
	}, nil, &d)
	if err != nil {
		return nil, 0, err
	}
	return d.Members, d.Revision, nil
}

// Members returns the directory when it changed since revision, or nil
// and changed false when it has not.
func (c *Client) Members(ctx context.Context, nodeKey string, since int) (ms []Member, revision int, changed bool, err error) {
	var d directory
	q := url.Values{"since": []string{fmt.Sprint(since)}}
	hdr := map[string]string{}
	if nodeKey != "" {
		hdr["X-Gorex-Node"] = nodeKey
	}
	if err = c.call(ctx, "GET", "/v1/members?"+q.Encode(), nil, hdr, &d); err != nil {
		if errors.Is(err, errNotModified) {
			return nil, since, false, nil
		}
		return nil, 0, false, err
	}
	return d.Members, d.Revision, true, nil
}

// Leave removes this machine from the network.
func (c *Client) Leave(ctx context.Context, nodeKey string) error {
	return c.call(ctx, "POST", "/v1/leave", map[string]string{"nodeKey": nodeKey}, nil, nil)
}

// Kick removes another machine, by its public key or its name.
func (c *Client) Kick(ctx context.Context, nodeKey, name string) error {
	if nodeKey == "" && name == "" {
		return errors.New("coord: name the member by its public key or its name")
	}
	return c.call(ctx, "POST", "/v1/kick", nodeBody{NodeKey: nodeKey, Name: name}, nil, nil)
}

// errNotModified is the 204 of members.
var errNotModified = errors.New("coord: not modified")

// call makes one request: the network key in its header, a JSON body,
// and a JSON answer into out, which may be nil. Answers carry no
// secret, so their text may be shown.
func (c *Client) call(ctx context.Context, method, path string, body, headers any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+c.host+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-Gorex-Key", c.key)
	if m, ok := headers.(map[string]string); ok {
		for k, v := range m {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return errNotModified
	case resp.StatusCode == http.StatusOK:
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("coord: the coordinator's answer does not read: %w", err)
		}
		return nil
	default:
		var e struct{ Error string }
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("coord: the coordinator answered %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("coord: the coordinator answered %d", resp.StatusCode)
	}
}
