package rexapi

// change is one event a model change makes, and the session it belongs
// to. A server-level event, such as session_created, has server set.
type change struct {
	server  bool
	session string
	typ     string
	payload any
}

// diffModels describes what changed between two models as the events the
// doc's Event Reference lists. A nil old model describes the first
// read, which creates every session it sees.
func diffModels(old, new *model) []change {
	if old == nil {
		return nil
	}
	var events []change
	for _, s := range new.sessions {
		before := old.byID[s.id]
		if before == nil {
			events = append(events, change{server: true, typ: "session_created",
				payload: sessionCreated{Session: sessionListEntry{SessionID: s.id, Label: s.label}}})
			for _, w := range s.windows {
				events = append(events, change{session: s.id, typ: "window_created",
					payload: windowPayload{WindowID: w.id, Label: w.label}})
			}
			for _, b := range s.blocks {
				events = append(events, change{session: s.id, typ: "block_created",
					payload: blockIDPayload{BlockID: b.id}})
			}
			events = append(events, change{session: s.id, typ: "session_view_changed",
				payload: revisionPayload{Revision: s.revision}})
			continue
		}
		if before.sig == s.sig {
			continue
		}
		if before.label != s.label {
			events = append(events, change{session: s.id, typ: "session_label_changed",
				payload: labelPayload{Label: s.label}})
		}
		for _, b := range s.blocks {
			if before.findBlock(b.id) == nil {
				events = append(events, change{session: s.id, typ: "block_created",
					payload: blockIDPayload{BlockID: b.id}})
			}
		}
		for _, b := range before.blocks {
			if s.findBlock(b.id) == nil {
				events = append(events, change{session: s.id, typ: "block_closed",
					payload: blockIDPayload{BlockID: b.id}})
			}
		}
		for _, w := range s.windows {
			was := before.window(w.id)
			if was == nil {
				events = append(events, change{session: s.id, typ: "window_created",
					payload: windowPayload{WindowID: w.id, Label: w.label}})
				continue
			}
			if was.label != w.label {
				events = append(events, change{session: s.id, typ: "window_label_changed",
					payload: windowPayload{WindowID: w.id, Label: w.label}})
			}
		}
		for _, w := range before.windows {
			if s.window(w.id) == nil {
				events = append(events, change{session: s.id, typ: "window_closed",
					payload: windowIDPayload{WindowID: w.id}})
			}
		}
		if before.active != s.active {
			events = append(events, change{session: s.id, typ: "active_window_changed",
				payload: activeWindowPayload{WindowID: s.active}})
		}
		if before.revision != s.revision {
			events = append(events, change{session: s.id, typ: "session_view_changed",
				payload: revisionPayload{Revision: s.revision}})
		}
	}
	for _, was := range old.sessions {
		if new.byID[was.id] == nil {
			events = append(events, change{server: true, typ: "session_destroyed",
				payload: sessionIDPayload{SessionID: was.id}})
		}
	}
	return events
}
