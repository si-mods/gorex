package rex

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// PortsResult is com.superlogical.terminal.ports's answer: the TCP ports a
// terminal's programs listen on, and the processes that hold them. It is
// also the payload of a ports event, and the shape Rex's clients decode.
type PortsResult struct {
	// Status is "ok" when every process of the terminal was inspected,
	// "partial" when some could not be, and "unsupported" when this
	// platform cannot list sockets.
	Status string `json:"status"`
	// Ports is sorted by port, then address.
	Ports []PortEntry `json:"ports"`
	// Processes holds one entry for each pid in Ports, sorted by pid.
	Processes []PortProcess `json:"processes"`
}

// PortEntry is one listening socket of a terminal's program.
type PortEntry struct {
	Port        int    `json:"port"`
	Address     string `json:"address"`
	Protocol    string `json:"protocol"`
	FirstSeenAt string `json:"first_seen_at"`
	// PID is the process that holds the socket, left out when the
	// platform could not tell.
	PID int `json:"pid,omitempty"`
}

// PortProcess describes a process that holds a port.
type PortProcess struct {
	PID    int    `json:"pid"`
	UserID int    `json:"user_id"`
	User   string `json:"user,omitempty"`
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Argv0  string `json:"argv0,omitempty"`
	Script string `json:"script,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

// portSocket is one listening TCP socket that a process of a terminal
// holds.
type portSocket struct {
	PID      int
	Port     int
	Address  string
	Protocol string
}

// PortsEvent tells subscribers that a session's ports changed: the whole
// list, as the ports method answers.
type PortsEvent struct {
	SessionID string      `json:"session_id"`
	Ports     PortsResult `json:"ports"`
}

// portsTiming is how often the scan looks, and how soon a call may look
// again. A test gives its server its own timings.
type portsTiming struct {
	every  time.Duration
	min    time.Duration
	active time.Duration
	idle   time.Duration
}

// defaultPortsTiming is the scan's timing: every second the server looks
// at which session is due; a call reuses a look for 250 ms; a terminal is
// looked at every 10 seconds while it is active, and up to every 5
// minutes while it is idle.
var defaultPortsTiming = portsTiming{
	every: time.Second, min: 250 * time.Millisecond,
	active: 10 * time.Second, idle: 5 * time.Minute,
}

// portsTiming returns the timings of this server.
func (s *Server) portsTiming() portsTiming {
	if s.portsClock != nil {
		return *s.portsClock
	}
	return defaultPortsTiming
}

// portsScan is one session's port state: the last look, the first time
// each socket was seen, and the answer that look produced.
type portsScan struct {
	mu       sync.Mutex
	last     time.Time
	interval time.Duration
	first    map[string]time.Time
	sig      string
	scanned  bool
	result   PortsResult
}

// portsState returns the state of a session, creating it.
func (s *Server) portsState(sid string) *portsScan {
	s.portsMu.Lock()
	defer s.portsMu.Unlock()
	if s.ports == nil {
		s.ports = map[string]*portsScan{}
	}
	st := s.ports[sid]
	if st == nil {
		st = &portsScan{first: map[string]time.Time{}}
		s.ports[sid] = st
	}
	return st
}

// portsFor answers a ports request: the last look when it is young
// enough, or a new one.
func (s *Server) portsFor(ss *session) (PortsResult, error) {
	now := time.Now()
	st := s.portsState(ss.id)
	timing := s.portsTiming()
	st.mu.Lock()
	if !st.last.IsZero() && now.Sub(st.last) < timing.min {
		res := st.result
		st.mu.Unlock()
		return res, nil
	}
	st.interval = timing.active
	st.mu.Unlock()
	res, changed := s.scanPorts(ss, now)
	if changed {
		s.queuePortsEvent(ss.id, res)
	}
	return res, nil
}

// scanPorts looks at a session's processes and sockets, and returns the
// answer and whether it differs from the one before.
func (s *Server) scanPorts(ss *session, now time.Time) (PortsResult, bool) {
	info := s.info(ss)
	st := s.portsState(ss.id)
	st.mu.Lock()
	defer st.mu.Unlock()
	previous := st.last
	st.last = now
	active := previous.IsZero() ||
		info.LastOutput.After(previous) || info.LastInput.After(previous)

	pids, treeOK := portPIDs(info.PID)
	socks, sockOK := portListeners(pids)
	statusOK := treeOK && sockOK

	procs := map[int]PortProcess{}
	for _, sk := range socks {
		if _, done := procs[sk.PID]; done || sk.PID == 0 {
			continue
		}
		p, ok := portProcess(sk.PID, s.hostUser())
		if !ok {
			statusOK = false
			continue
		}
		procs[sk.PID] = p
	}

	seen := make(map[string]time.Time, len(socks))
	entries := make([]PortEntry, 0, len(socks))
	for _, sk := range socks {
		key := portsKey(sk)
		first, ok := st.first[key]
		if !ok {
			first = now
		}
		seen[key] = first
		entries = append(entries, PortEntry{
			Port: sk.Port, Address: sk.Address, Protocol: sk.Protocol,
			FirstSeenAt: first.UTC().Format(time.RFC3339),
			PID:         sk.PID,
		})
	}
	st.first = seen
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Port != entries[j].Port {
			return entries[i].Port < entries[j].Port
		}
		if entries[i].Address != entries[j].Address {
			return entries[i].Address < entries[j].Address
		}
		return entries[i].Protocol < entries[j].Protocol
	})
	processes := make([]PortProcess, 0, len(procs))
	for _, p := range procs {
		processes = append(processes, p)
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })

	status := "ok"
	if !statusOK {
		status = "partial"
	}
	res := PortsResult{Status: status, Ports: entries, Processes: processes}

	sig := portsSignature(res)
	changed := sig != st.sig
	if !st.scanned && len(entries) == 0 {
		// A terminal whose first list is empty sends no event.
		changed = false
	}
	st.scanned = true
	st.sig = sig
	st.result = res
	timing := s.portsTiming()
	if active || changed {
		st.interval = timing.active
	} else {
		st.interval = min(st.interval*2, timing.idle)
		if st.interval < timing.active {
			st.interval = timing.active
		}
	}
	return res, changed
}

// portsSignature names what a result says, for telling a change.
func portsSignature(r PortsResult) string {
	var b strings.Builder
	b.WriteString(r.Status)
	for _, p := range r.Ports {
		b.WriteByte(1)
		b.WriteString(strconv.Itoa(p.Port))
		b.WriteByte(0)
		b.WriteString(p.Address)
		b.WriteByte(0)
		b.WriteString(p.Protocol)
		b.WriteByte(0)
		b.WriteString(strconv.Itoa(p.PID))
	}
	for _, p := range r.Processes {
		b.WriteByte(2)
		b.WriteString(strconv.Itoa(p.PID))
		b.WriteByte(0)
		b.WriteString(p.Name)
		b.WriteByte(0)
		b.WriteString(p.Path)
		b.WriteByte(0)
		b.WriteString(p.Cwd)
	}
	return b.String()
}

// dedupeSockets sorts the sockets and drops duplicates, which a process
// can hold through two descriptors.
func dedupeSockets(in []portSocket) []portSocket {
	sort.Slice(in, func(i, j int) bool {
		if in[i].Port != in[j].Port {
			return in[i].Port < in[j].Port
		}
		if in[i].Address != in[j].Address {
			return in[i].Address < in[j].Address
		}
		return in[i].PID < in[j].PID
	})
	out := in[:0]
	for _, sk := range in {
		if n := len(out); n > 0 && out[n-1] == sk {
			continue
		}
		out = append(out, sk)
	}
	return out
}

// portsKey names one held socket: a new process holding the same address
// and port is a new entry.
func portsKey(sk portSocket) string {
	return sk.Protocol + "\x00" + sk.Address + "\x00" + strconv.Itoa(sk.Port) + "\x00" + strconv.Itoa(sk.PID)
}

// portProcess describes a process that holds a socket. ok is false when
// the system would not let the server inspect it.
func portProcess(pid int, user string) (PortProcess, bool) {
	p := inspect(pid)
	if p.name == "" && p.path == "" {
		return PortProcess{}, false
	}
	pp := PortProcess{
		PID: pid, UserID: p.uid, User: user,
		Name: p.name, Path: p.path, Cwd: p.dir,
	}
	if len(p.args) > 0 {
		pp.Argv0 = clipString(p.args[0], 256)
	}
	if interpreters[strings.TrimPrefix(p.name, "-")] && len(p.args) > 1 {
		for _, a := range p.args[1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			pp.Script = a
			break
		}
	}
	return pp, true
}

// clipString cuts s to at most n bytes, at a rune boundary.
func clipString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// hostUser names the account the sessions run as, once the machine is
// read. It is empty before that.
func (s *Server) hostUser() string {
	if s.hostDone != nil {
		select {
		case <-s.hostDone:
		default:
			return ""
		}
	}
	return s.host.User
}

// portsLoop looks at each session when its interval has passed, and tells
// the subscribers of a change.
func (s *Server) portsLoop() {
	t := time.NewTicker(s.portsTiming().every)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case now := <-t.C:
			s.scanPortsDue(now)
		}
	}
}

// scanPortsDue scans the sessions whose interval has passed.
func (s *Server) scanPortsDue(now time.Time) {
	for _, ss := range s.all() {
		st := s.portsState(ss.id)
		st.mu.Lock()
		due := st.last.IsZero() || now.Sub(st.last) >= st.interval
		st.mu.Unlock()
		if !due {
			continue
		}
		res, changed := s.scanPorts(ss, now)
		if changed {
			s.queuePortsEvent(ss.id, res)
		}
	}
	live := map[string]bool{}
	for _, ss := range s.all() {
		live[ss.id] = true
	}
	s.portsMu.Lock()
	for sid := range s.ports {
		if !live[sid] {
			delete(s.ports, sid)
		}
	}
	s.portsMu.Unlock()
}

// queuePortsEvent keeps the newest result of a session for subscribers,
// and wakes them.
func (s *Server) queuePortsEvent(sid string, res PortsResult) {
	s.portsMu.Lock()
	if s.portsEvents == nil {
		s.portsEvents = map[string]PortsResult{}
	}
	s.portsEvents[sid] = res
	s.portsMu.Unlock()
	s.notify("ports")
}

// takePortsEvents returns the changes not told of yet, and forgets them.
func (s *Server) takePortsEvents() []PortsEvent {
	s.portsMu.Lock()
	defer s.portsMu.Unlock()
	if len(s.portsEvents) == 0 {
		return nil
	}
	out := make([]PortsEvent, 0, len(s.portsEvents))
	for sid, res := range s.portsEvents {
		out = append(out, PortsEvent{SessionID: sid, Ports: res})
	}
	s.portsEvents = nil
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}
