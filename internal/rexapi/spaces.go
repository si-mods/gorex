package rexapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gorex/internal/rex"
)

// Spaces across hosts: a GoRex space may hold panes of this machine and
// of other hosts, which the app reaches over tailcat or Tailscale. A Rex
// session shows this machine's panes only (filterNode), and a client
// reaches each host's own gateway apart. GET /api/spaces tells such a
// client which panes of other hosts each space holds, named as those
// hosts' own gateways name them, so that it shows them in the space
// through its own connection to each host: one it was let into there.
// The answer opens nothing: a pane of a host the client does not reach
// is of no use to it. GoRex's own, which Rex has not: the welcome lists
// featureSpaces.

// featureSpaces is the welcome's feature of a gateway that serves
// /api/spaces.
const featureSpaces = "gorex.spaces"

// remoteBlock is a pane of another host in a space: the machine it runs
// on (rex.MachineID, which that host's gateway tells as machine_id) and
// the block of its session there (blockID, as that gateway names it). It
// holds no address, key nor name of the host: nothing that reaches it.
type remoteBlock struct {
	Machine string `json:"machine"`
	BlockID string `json:"block_id"`
}

// spacesResult is /api/spaces's answer: this machine, and the spaces
// that hold panes of others, with those panes in layout order.
type spacesResult struct {
	MachineID string        `json:"machine_id"`
	Sessions  []spacesEntry `json:"sessions"`
}

type spacesEntry struct {
	SessionID    string        `json:"session_id"`
	Revision     uint64        `json:"revision"`
	RemoteBlocks []remoteBlock `json:"remote_blocks"`
}

// spaces builds /api/spaces's answer from a model.
func (g *Gateway) spaces(m *model) spacesResult {
	res := spacesResult{MachineID: g.hello.Machine, Sessions: []spacesEntry{}}
	for _, s := range m.sessions {
		if len(s.remote) > 0 {
			res.Sessions = append(res.Sessions, spacesEntry{SessionID: s.id, Revision: s.revision, RemoteBlocks: s.remote})
		}
	}
	return res
}

func (g *Gateway) handleSpaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "spaces are a GET")
		return
	}
	m, err := g.current()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "reading the layout: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(g.spaces(m))
}

// remoteSignature names a space's panes of other hosts, "" for none, so
// that a view's signature without them stays what it was.
func remoteSignature(rbs []remoteBlock) string {
	if len(rbs) == 0 {
		return ""
	}
	b, _ := json.Marshal(rbs)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hostMachines is rex.HostMachines of dir, read again only when
// hosts.json's modification time or size changed. One goroutine uses it
// at a time, under the gateway's refresh lock.
type hostMachines struct {
	dir  string
	read bool
	mod  time.Time
	size int64
	m    map[string]string
}

func (h *hostMachines) get() map[string]string {
	mod, size := time.Time{}, int64(-1)
	if fi, err := os.Stat(filepath.Join(h.dir, "hosts.json")); err == nil {
		mod, size = fi.ModTime(), fi.Size()
	}
	if !h.read || !mod.Equal(h.mod) || size != h.size {
		h.read, h.mod, h.size, h.m = true, mod, size, rex.HostMachines(h.dir)
	}
	return h.m
}
