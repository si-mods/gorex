package rexapi

import (
	"encoding/json"

	"gorex/internal/rex"
)

// portsReply is com.superlogical.terminal.ports's answer, and the payload
// of ports_changed: Rex's shape, with the truncation fields GoRex adds
// when the list does not fit in one message.
type portsReply struct {
	Status    string            `json:"status"`
	Ports     []rex.PortEntry   `json:"ports"`
	Processes []rex.PortProcess `json:"processes"`
	Truncated bool              `json:"truncated,omitempty"`
	ID        int               `json:"id,omitempty"`
}

// blockPorts lists the TCP ports that a terminal's programs listen on.
func blockPorts(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	res, err := c.g.opt.Client.Ports(b.sid)
	if err != nil {
		return nil, errInternal("listing the terminal's ports: %v", err)
	}
	return c.g.portsReply(res), nil
}

// portsReply builds the answer from the server's: the whole list when it
// fits, or the start of it and a blob_read id for the rest.
func (g *Gateway) portsReply(res rex.PortsResult) portsReply {
	out := portsReply{Status: res.Status, Ports: res.Ports, Processes: res.Processes}
	if out.Ports == nil {
		out.Ports = []rex.PortEntry{}
	}
	if out.Processes == nil {
		out.Processes = []rex.PortProcess{}
	}
	data, _ := json.Marshal(out)
	if len(data) <= maxBlobBytes {
		return out
	}
	out.ID = g.putBlob(string(data), len(data))
	out.Truncated = true
	for len(out.Ports) > 0 {
		out.Ports = out.Ports[:len(out.Ports)-1]
		out.Processes = portsProcessesOf(out.Ports, out.Processes)
		data, _ = json.Marshal(out)
		if len(data) <= maxBlobBytes {
			break
		}
	}
	return out
}

// portsProcessesOf keeps the processes that the remaining ports name.
func portsProcessesOf(ports []rex.PortEntry, processes []rex.PortProcess) []rex.PortProcess {
	keep := map[int]bool{}
	for _, p := range ports {
		if p.PID != 0 {
			keep[p.PID] = true
		}
	}
	out := processes[:0]
	for _, p := range processes {
		if keep[p.PID] {
			out = append(out, p)
		}
	}
	if out == nil {
		out = []rex.PortProcess{}
	}
	return out
}

// emitPorts sends each changed terminal's ports_changed event, to the
// connections that receive the block's events.
func (g *Gateway) emitPorts(list []rex.PortsEvent) {
	if len(list) == 0 {
		return
	}
	m := g.model.Load()
	if m == nil {
		return
	}
	for _, pe := range list {
		b := m.blockBySID(pe.SessionID)
		if b == nil {
			continue
		}
		g.emitBlockEvent(b, "ports_changed", g.portsReply(pe.Ports))
	}
}
