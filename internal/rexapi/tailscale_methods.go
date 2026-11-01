package rexapi

// The server.tailscale methods turn built-in Tailscale on and off, sign
// it in and out, and rename its device. As on Rex's server, they are for
// the local socket only: over TCP or the tailnet they fail with 403.

// tailnetFor returns built-in Tailscale for an administering call, or
// the error to answer it with.
func (c *control) tailnetFor(method string) (*tailnet, *apiError) {
	if c.transport != transportUnix {
		return nil, errorf("internal", 403, "%s is accepted only over the server's local socket", method)
	}
	if c.g.tailnet == nil {
		return nil, errNotSupported("this server can't run built-in Tailscale")
	}
	return c.g.tailnet, nil
}

func methodTailscaleEnable(c *control, a args) (any, *apiError) {
	t, aerr := c.tailnetFor("server.tailscale.enable")
	if aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("hostname", "allow_tailnet_members", "auth_key", "advertise_tags"); aerr != nil {
		return nil, aerr
	}
	allow, ok, aerr := a.boolean("allow_tailnet_members")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("allow_tailnet_members is required")
	}
	o := enableOptions{allowMembers: allow}
	if o.hostname, _, aerr = a.str("hostname"); aerr != nil {
		return nil, aerr
	}
	if o.authKey, _, aerr = a.str("auth_key"); aerr != nil {
		return nil, aerr
	}
	if o.tags, _, aerr = a.strs("advertise_tags"); aerr != nil {
		return nil, aerr
	}
	st, aerr := t.enable(o)
	if aerr != nil {
		return nil, aerr
	}
	return tailscaleResult{Tailscale: st}, nil
}

func methodTailscaleDisable(c *control, a args) (any, *apiError) {
	t, aerr := c.tailnetFor("server.tailscale.disable")
	if aerr != nil {
		return nil, aerr
	}
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	return tailscaleResult{Tailscale: t.disable()}, nil
}

func methodTailscaleLogin(c *control, a args) (any, *apiError) {
	t, aerr := c.tailnetFor("server.tailscale.login")
	if aerr != nil {
		return nil, aerr
	}
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	st, aerr := t.login()
	if aerr != nil {
		return nil, aerr
	}
	return tailscaleResult{Tailscale: st}, nil
}

func methodTailscaleLogout(c *control, a args) (any, *apiError) {
	t, aerr := c.tailnetFor("server.tailscale.logout")
	if aerr != nil {
		return nil, aerr
	}
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	st, aerr := t.logout()
	if aerr != nil {
		return nil, aerr
	}
	return tailscaleResult{Tailscale: st}, nil
}

func methodTailscaleRename(c *control, a args) (any, *apiError) {
	t, aerr := c.tailnetFor("server.tailscale.rename")
	if aerr != nil {
		return nil, aerr
	}
	if aerr := a.only("hostname"); aerr != nil {
		return nil, aerr
	}
	name, ok, aerr := a.str("hostname")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("hostname is required")
	}
	st, aerr := t.rename(name)
	if aerr != nil {
		return nil, aerr
	}
	return tailscaleResult{Tailscale: st}, nil
}
