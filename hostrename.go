package main

// Names given here go where the tab or pane lives: a tab of a host's
// project is in the host's layout, and its name goes there with the rest
// of the tab (store.queueHosts), for the host's other clients to show.

// setPaneName gives pane p the name the user gave it, "" to follow its
// program again. Call it on the main thread.
func (a *App) setPaneName(p *Pane, name string) {
	if p.Name == name {
		return
	}
	p.Name = name
	a.changed()
}

// setTabName gives tab t the name the user gave it, "" to follow its
// panes again. Call it on the main thread.
func (a *App) setTabName(t *Tab, name string) {
	if t.Name == name {
		return
	}
	t.Name = name
	a.changed()
}
