package rexapi

// cleanupClient releases every resource and attachment held by rec when
// its control connection closes cleanly or its reconnect grace expires.
func (g *Gateway) cleanupClient(rec *clientRecord, attached []string, reason string) {
	g.disownClientTunnels(rec.id)
	g.emitOwnerChanges(g.releaseBlocks(rec, reason))
	for _, sid := range attached {
		g.emitClientEvent(rec, sid, "client_disconnected")
	}
	g.emitClientEvent(rec, "", "client_left")
	g.cancelStreams(rec.id)
	g.dropTickets(rec.id, "")
	g.dropUploads(rec.id, "")
	g.refresh()
}

// cleanupClientSession drops a client's per-session reservations,
// ownerships, and attachment notification when it detaches from sid.
func (g *Gateway) cleanupClientSession(rec *clientRecord, sid string) {
	g.dropTickets(rec.id, sid)
	g.dropUploads(rec.id, sid)
	g.emitClientEvent(rec, sid, "client_disconnected")
	if m, err := g.current(); err == nil {
		if s := m.byID[sid]; s != nil {
			g.emitOwnerChanges(g.releaseIn(rec, s, "disconnect"))
			g.refresh()
		}
	}
}

// closeAllResources shuts down every gateway-owned stream, download,
// and tunnel when the gateway closes.
func (g *Gateway) closeAllResources() {
	g.mu.Lock()
	conns := make([]*control, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	streams := make([]*stream, 0, len(g.streams))
	for _, s := range g.streams {
		streams = append(streams, s)
	}
	g.mu.Unlock()
	if g.tailnet != nil {
		g.tailnet.close()
	}
	if g.lan != nil {
		g.lan.close()
	}
	for _, c := range conns {
		c.closeWith(1001, "data provider canceled")
	}
	for _, s := range streams {
		s.closeWith(1001, "data provider canceled")
	}
	g.dropDownloads(nil)
	g.closeAllTunnels()
}
