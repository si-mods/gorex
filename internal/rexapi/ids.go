package rexapi

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// Namespaces of Rex IDs, fixed for every run of this package: a GoRex
// workspace or session has the same Rex ID each time it is served.
var (
	// idNamespace is the UUID namespace name-based IDs are hashed under.
	idNamespace = [16]byte{
		0x5c, 0x2a, 0xd8, 0x31, 0x6f, 0x0e, 0x4b, 0x8b,
		0x9a, 0x53, 0x71, 0xc4, 0x2e, 0x88, 0x0d, 0xf6,
	}
)

// kSession and the other kinds name the sort of thing an ID is for, so
// that two things with the same seed still get different IDs.
const (
	kSession = "session"
	kWindow  = "window"
	kLayer   = "layer"
	kBlock   = "block"
	kSplit   = "split"
	kHost    = "host"
)

// nameID makes a name-based ID of the kind prefix, from the seed and the
// kind, as a UUID version 8: SHA-256 over the namespace, the kind and
// the seed, with the version and variant bits set as RFC 9562 says.
func nameID(kind, seed string) string {
	h := sha256.New()
	h.Write(idNamespace[:])
	h.Write([]byte{0})
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(seed))
	sum := h.Sum(nil)
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = b[6]&0x0f | 0x80
	b[8] = b[8]&0x3f | 0x80
	return kind + ":" + uuidString(b)
}

// sessionID names the Rex session of a GoRex workspace.
func sessionID(workspace string) string { return nameID(kSession, workspace) }

// blockID names the Rex block of a GoRex session.
func blockID(sid string) string { return nameID(kBlock, sid) }

// layerID names the tiled layer of a window.
func layerID(window string) string { return nameID(kLayer, window) }

// splitID names a split by the shape of the tree it divides, without its
// ratio: a split keeps its ID while it is dragged, and while the panes
// under it are resized.
func splitID(shape string) string { return nameID(kSplit, shape) }

// hostID names the machine and account the server runs as, so that every
// server that sees the same files reports the same one. It is a bare
// UUID, as Rex reports a host.
func hostID(name, user string) string {
	return strings.TrimPrefix(nameID(kHost, name+"/"+user), kHost+":")
}

// newWindowID makes a UUID version 7 for a tab that had no window before.
func newWindowID() string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	rand.Read(b[6:])
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	return "window:" + uuidString(b)
}

// randomUUID makes a random UUID version 4.
func randomUUID() string {
	var b [16]byte
	copy(b[:], randomBytes(16))
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return uuidString(b)
}

// newID makes a random ID of the kind, shaped as Rex's are.
func newID(kind string) string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return kind + ":" + uuidString(b)
}

// randomBytes returns n random bytes.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// The system's random source is required for the secrets this
		// package issues. Without it, make a value that is at least not
		// the same every time, and let the failure show in other ways.
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> (8 * (i % 8)))
		}
	}
	return b
}

// randomToken returns a 32-byte reconnect token as lowercase hexadecimal.
func randomToken() string { return fmt.Sprintf("%x", randomBytes(32)) }

// uuidString writes a UUID as Rex writes IDs.
func uuidString(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// isUUIDID tells whether id is a Rex ID of the kind: the prefix and a
// UUID, as client.inspect requires.
func isUUIDID(id, kind string) bool {
	rest, ok := strings.CutPrefix(id, kind+":")
	if !ok || len(rest) != 36 {
		return false
	}
	for i, r := range rest {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !isHex(r) {
			return false
		}
	}
	return true
}

// shortID is the last eight hexadecimal digits of an ID, as Rex shows
// one in a default label or a completion.
func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}
