package main

import (
	"log"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// Adoptions (docs/projects.md, Migration, "On the desktop"): the panes
// other hosts ran in this machine's tabs of v0.38.0 to v0.40.x, which the
// migration took out and keeps in this machine's layout (Store.Adopt),
// one per host and project. As each host connects, a host of projects,
// the app sends it its adoptions (project.adopt): the host makes the
// project, if any, and arranges the panes into its tabs, starting no
// shell. Once the host answered, the app tells this machine's server it
// took the adoption (adopt.taken), which forgets it. A host away, or one
// of before projects, keeps its adoptions until it connects as one; the
// sidebar shows them meanwhile, "Waiting for" the host.

// adoptionHost is the host an adoption is for: by its ID in hosts.json,
// else by its machine; nil for none of the user's hosts.
func (st *store) adoptionHost(ad layout.Adoption) *hostConn {
	for _, h := range st.hosts {
		if ad.Host != "" && h.ID == ad.Host {
			return h
		}
	}
	if ad.Machine == "" {
		return nil
	}
	// Of two hosts of the machine, as its LAN and tailnet addresses, the
	// connected one.
	var found *hostConn
	for _, h := range st.hosts {
		if h.Machine != ad.Machine {
			continue
		}
		if serverMade(h) {
			return h
		}
		if found == nil {
			found = h
		}
	}
	return found
}

// waitingOn are the adoptions that wait for host h: all of h's while it
// cannot take them (away, or of before projects), none once it can.
func (st *store) waitingOn(h *hostConn) []layout.Adoption {
	if st == nil || h == nil || serverMade(h) {
		return nil
	}
	var out []layout.Adoption
	for _, ad := range st.base.Adopt {
		if st.adoptionHost(ad) == h {
			out = append(out, ad)
		}
	}
	return out
}

// sendAdoptions sends each host of projects that is connected the
// adoptions for it that are not on their way already, and, once it took
// one, tells this machine's server (adopt.taken). One the host refuses
// stays, for the next time it connects. Call it on the main thread.
func (st *store) sendAdoptions() {
	if st.readOnly || st.client == nil {
		return
	}
	for _, ad := range st.base.Adopt {
		h := st.adoptionHost(ad)
		if h == nil || !serverMade(h) || st.adopting[ad.ID] {
			continue
		}
		if st.adopting == nil {
			st.adopting = map[string]bool{}
		}
		st.adopting[ad.ID] = true
		x := ad
		id := ad.ID
		log.Printf("gorex: sending %s its adoption %s: %d tabs", h.label(), id, len(x.Tabs))
		st.sendOn(h, rex.LayoutCommand{Op: "project.adopt", Adopt: &x}, func(res rex.LayoutCommand) {
			if res.Code != "" {
				// Not again on this connection, as its layout changes: at
				// the next (retryAdoptions).
				log.Printf("gorex: %s did not adopt %s: %s", h.label(), id, res.Error)
				if st.adoptRefused == nil {
					st.adoptRefused = map[string]*hostConn{}
				}
				st.adoptRefused[id] = h
				return
			}
			st.send(rex.LayoutCommand{Op: "adopt.taken", Target: id}, func(res rex.LayoutCommand) {
				delete(st.adopting, id)
				if res.Code != "" {
					log.Printf("gorex: adoption %s taken by %s, but not forgotten: %s", id, h.label(), res.Error)
				}
			})
		})
	}
}

// retryAdoptions has the adoptions host h refused go to it again, as it
// connects anew. Call it on the main thread.
func (st *store) retryAdoptions(h *hostConn) {
	for id, o := range st.adoptRefused {
		if o == h {
			delete(st.adoptRefused, id)
			delete(st.adopting, id)
		}
	}
}
