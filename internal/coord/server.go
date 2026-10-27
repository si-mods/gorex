package coord

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorex/internal/rex"
)

// The wrong-key lockout: how many failures an address may make, and how
// long it is then kept out.
const (
	maxKeyFails = 5
	keyCooldown = 10 * time.Minute
)

// Server answers the network's protocol, one per coordinator. It keeps
// the directory in memory and writes it on every change; lastSeen
// lives in memory alone, and rides along with the next change.
type Server struct {
	mu  sync.Mutex
	st  *state
	dir string

	// failures and blocked are the wrong-key lockout, by address.
	failures map[string]int
	blocked  map[string]time.Time
}

// NewServer serves the state in dir, which gcoord init made.
func NewServer(dir string) (*Server, error) {
	st, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	return &Server{st: st, dir: dir, failures: map[string]int{}, blocked: map[string]time.Time{}}, nil
}

// Revision tells the directory's current revision.
func (s *Server) Revision() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Revision
}

// Handler is the coordinator's HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("POST /v1/join", s.auth(s.join))
	mux.HandleFunc("GET /v1/members", s.auth(s.members))
	mux.HandleFunc("POST /v1/leave", s.auth(s.leave))
	mux.HandleFunc("POST /v1/kick", s.auth(s.kick))
	return mux
}

// auth checks the network key of a request. A right key always comes
// through: members may share an address (one home, one NAT), and one
// of them guessing wrong must not lock the others out. An address that
// keeps guessing wrong is only kept from guessing further.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !checkKey(s.st, r.Header.Get("X-Gorex-Key")) {
			s.mu.Lock()
			if until, ok := s.blocked[host]; !ok || time.Now().After(until) {
				s.failures[host]++
				if s.failures[host] >= maxKeyFails {
					s.blocked[host] = time.Now().Add(keyCooldown)
					delete(s.failures, host)
				}
			}
			s.mu.Unlock()
			w.Header().Set("Retry-After", "600")
			writeErr(w, http.StatusUnauthorized, "the network key is wrong")
			return
		}
		s.mu.Lock()
		s.failures[host] = 0
		delete(s.blocked, host)
		s.mu.Unlock()
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	n := len(s.st.Members)
	rev := s.st.Revision
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev, "members": n})
}

// joinBody is what a machine sends to join or rejoin.
type joinBody struct {
	Name    string `json:"name"`
	NodeKey string `json:"nodeKey"`
	Address string `json:"address"`
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	var b joinBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "the request does not read")
		return
	}
	nodeKey, err := rex.NormalizePublicKey(b.NodeKey)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the public key does not read")
		return
	}
	if !rex.ValidRemoteAddress(b.Address) {
		writeErr(w, http.StatusBadRequest, "the address does not read")
		return
	}
	name := CleanName(b.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "the name is empty")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.Members {
		m := &s.st.Members[i]
		if m.NodeKey == nodeKey { // a rejoin refreshes the machine
			if m.Name != name && nameTaken(s.st.Members, name) {
				writeErr(w, http.StatusConflict, "the name is taken")
				return
			}
			m.Name, m.Address = name, strings.TrimSpace(b.Address)
			m.LastSeen = time.Now()
			s.reply(w, m.NodeKey)
			return
		}
	}
	if nameTaken(s.st.Members, name) {
		writeErr(w, http.StatusConflict, "the name is taken")
		return
	}
	s.st.Members = append(s.st.Members, Member{
		Name:     name,
		NodeKey:  nodeKey,
		Address:  strings.TrimSpace(b.Address),
		Joined:   time.Now(),
		LastSeen: time.Now(),
	})
	s.bump()
	log.Printf("coord: %s joined (%s)", name, Member{NodeKey: nodeKey}.Fingerprint())
	s.reply(w, nodeKey)
}

func (s *Server) members(w http.ResponseWriter, r *http.Request) {
	since, err := strconv.Atoi(r.URL.Query().Get("since"))
	if err != nil {
		since = 0 // no or unreadable since asks for the directory
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A member's own poll marks it seen, before the unchanged answer.
	if nodeKey, err := rex.NormalizePublicKey(r.Header.Get("X-Gorex-Node")); err == nil {
		for i := range s.st.Members {
			if s.st.Members[i].NodeKey == nodeKey {
				s.st.Members[i].LastSeen = time.Now()
				break
			}
		}
	}
	if since == s.st.Revision {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.reply(w, "")
}

// nodeBody names a member by its public key.
type nodeBody struct {
	NodeKey string `json:"nodeKey"`
	Name    string `json:"name"`
}

func (s *Server) leave(w http.ResponseWriter, r *http.Request) {
	var b nodeBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "the request does not read")
		return
	}
	nodeKey, err := rex.NormalizePublicKey(b.NodeKey)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the public key does not read")
		return
	}
	if err := s.Kick("", nodeKey); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"revision": s.st.Revision})
}

func (s *Server) kick(w http.ResponseWriter, r *http.Request) {
	var b nodeBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "the request does not read")
		return
	}
	nodeKey, err := rex.NormalizePublicKey(b.NodeKey)
	if err != nil && b.NodeKey != "" {
		writeErr(w, http.StatusBadRequest, "the public key does not read")
		return
	}
	if nodeKey == "" && CleanName(b.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name a member by its public key or its name")
		return
	}
	if err := s.Kick(CleanName(b.Name), nodeKey); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"revision": s.st.Revision})
}

// Kick drops a member, by name or by public key, saving the directory;
// it serves the wire and the coordinator's own command line. Who is
// gone is logged with the fingerprint only, never the address.
func (s *Server) Kick(name, nodeKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.st.Members, func(m Member) bool {
		if name != "" && m.Name == name {
			return true
		}
		return nodeKey != "" && m.NodeKey == nodeKey
	})
	if i < 0 {
		return errors.New("coord: no such member")
	}
	gone := s.st.Members[i]
	s.st.Members = slices.Delete(s.st.Members, i, i+1)
	s.bump()
	log.Printf("coord: %s left (%s)", gone.Name, gone.Fingerprint())
	return nil
}

// bump grows the revision and writes the state; it holds the lock.
func (s *Server) bump() {
	s.st.Revision++
	if err := s.st.save(s.dir); err != nil {
		log.Printf("coord: writing the network state: %v", err)
	}
}

// reply answers with the directory; the joining machine's key is
// echoed back so it knows which member it is. It holds the lock.
func (s *Server) reply(w http.ResponseWriter, nodeKey string) {
	writeJSON(w, http.StatusOK, struct {
		Revision int      `json:"revision"`
		You      string   `json:"you,omitempty"`
		Members  []Member `json:"members"`
	}{s.st.Revision, nodeKey, s.st.Members})
}

func nameTaken(ms []Member, name string) bool {
	return slices.ContainsFunc(ms, func(m Member) bool { return m.Name == name })
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
