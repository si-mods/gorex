// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// The web client's tailnet engine (docs/web-tailscale.md): a Tailscale node
// compiled to WebAssembly that runs in the page, and reaches GoRex
// gateways over its own userspace network stack. It starts from
// tailscale.com/cmd/tsconnect/wasm (BSD-3-Clause), without SSH, LocalAPI,
// log policy or log upload, and adds HTTPS and WebSockets to a gateway
// (conn.go) and the network map the page reads its hosts from.
//
// It sets one global, gorexTailnet(config, emit), which makes a node and
// returns its calls (see newNode). The page (web/src/tailnet.ts) loads
// the module only when the user turns Tailscale on. Desktop GoRex never
// builds or imports it.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"tailscale.com/control/controlclient"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnauth"
	"tailscale.com/ipn/ipnlocal"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/netns"
	"tailscale.com/net/tsdial"
	"tailscale.com/tailcfg"
	"tailscale.com/tsd"
	"tailscale.com/types/logger"
	"tailscale.com/types/logid"
	"tailscale.com/types/netmap"
	"tailscale.com/wgengine"
	"tailscale.com/wgengine/netstack"
)

func main() {
	js.Global().Set("gorexTailnet", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 2 {
			panic("usage: gorexTailnet(config, emit)")
		}
		n, err := newNode(args[0], args[1])
		if err != nil {
			panic(err.Error())
		}
		return n.api()
	}))
	// Keep the runtime alive for the calls to come.
	select {}
}

// node is one Tailscale device of the page, ephemeral and kept in memory:
// "This tab only" (docs/web-tailscale.md, State).
type node struct {
	lb       *ipnlocal.LocalBackend
	dialer   *tsdial.Dialer
	ns       *netstack.Impl
	control  string
	hostname string
	// emit sends the page an event: its type and a string.
	emit func(kind, data string)

	mu      sync.Mutex
	stopped bool

	transportOnce sync.Once
	transport     *http.Transport
}

func newNode(config, emit js.Value) (*node, error) {
	netns.SetEnabled(false)
	str := func(k string) string {
		if v := config.Get(k); v.Type() == js.TypeString {
			return v.String()
		}
		return ""
	}
	control := str("controlURL")
	if control == "" {
		control = ipn.DefaultControlURL
	}
	logf := logger.Logf(log.Printf)
	sys := tsd.NewSystem()
	sys.Set(new(mem.Store))
	dialer := &tsdial.Dialer{Logf: logf}
	dialer.SetBus(sys.Bus.Get())
	eng, err := wgengine.NewUserspaceEngine(logf, wgengine.Config{
		Dialer:        dialer,
		SetSubsystem:  sys.Set,
		ControlKnobs:  sys.ControlKnobs(),
		HealthTracker: sys.HealthTracker.Get(),
		ExtraRootCAs:  sys.ExtraRootCAs,
		Metrics:       sys.UserMetricsRegistry(),
		EventBus:      sys.Bus.Get(),
	})
	if err != nil {
		return nil, err
	}
	sys.Set(eng)
	ns, err := netstack.Create(logf, sys.Tun.Get(), eng, sys.MagicSock.Get(), dialer, sys.DNSManager.Get(), sys.ProxyMapper())
	if err != nil {
		return nil, err
	}
	sys.Set(ns)
	ns.ProcessLocalIPs = true
	ns.ProcessSubnets = true
	dialer.UseNetstackForIP = func(netip.Addr) bool { return true }
	dialer.NetstackDialTCP = func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		c, err := ns.DialContextTCP(ctx, dst)
		if err != nil {
			// Not the typed nil of *gonet.TCPConn.
			return nil, err
		}
		return c, nil
	}
	dialer.NetstackDialUDP = func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		c, err := ns.DialContextUDP(ctx, dst)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	sys.NetstackRouter.Set(true)
	sys.Tun.Get().Start()
	// A zero log ID and no log policy: the node uploads no logs.
	var logID logid.PublicID
	lb, err := ipnlocal.NewLocalBackend(logf, logID, sys, controlclient.LoginEphemeral)
	if err != nil {
		return nil, err
	}
	if err := ns.Start(lb); err != nil {
		return nil, err
	}
	return &node{
		lb: lb, dialer: dialer, ns: ns, control: control, hostname: str("hostname"),
		emit: func(kind, data string) { emit.Invoke(kind, data) },
	}, nil
}

var stateNames = map[ipn.State]string{
	ipn.NoState:          "NoState",
	ipn.InUseOtherUser:   "InUseOtherUser",
	ipn.NeedsLogin:       "NeedsLogin",
	ipn.NeedsMachineAuth: "NeedsMachineAuth",
	ipn.Stopped:          "Stopped",
	ipn.Starting:         "Starting",
	ipn.Running:          "Running",
}

// api is the node's calls, as the page sees them:
//
//	start()                    starts the node; events follow
//	login()                    asks control for a login URL ("browse" event)
//	logout(): Promise          expires the node's key at control
//	stop()                     shuts the node down
//	rename(name): Promise      sets the device name
//	netmap(): string           the network map as JSON (netmapJSON)
//	request(...): Promise      HTTPS to a gateway (conn.go)
//	socket(...): object        a WebSocket to a gateway (conn.go)
//
// Events, emit(kind, data): "state" (an ipn.State's name), "browse" (the
// login URL), "netmap" (the map or its peers changed; read netmap()),
// "error" (control's message), "login" (the login finished).
func (n *node) api() map[string]any {
	fn := func(f func(args []js.Value) any) js.Func {
		return js.FuncOf(func(this js.Value, args []js.Value) any { return f(args) })
	}
	return map[string]any{
		"start": fn(func([]js.Value) any { n.start(); return nil }),
		"login": fn(func([]js.Value) any {
			go func() {
				if err := n.lb.StartLoginInteractive(context.Background()); err != nil {
					n.emit("error", err.Error())
				}
			}()
			return nil
		}),
		"logout": fn(func([]js.Value) any {
			return promise(func() (any, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				return nil, n.lb.Logout(ctx, ipnauth.Self)
			})
		}),
		"stop": fn(func([]js.Value) any {
			n.mu.Lock()
			defer n.mu.Unlock()
			if !n.stopped {
				n.stopped = true
				go n.lb.Shutdown()
			}
			return nil
		}),
		"rename": fn(func(args []js.Value) any {
			name := args[0].String()
			return promise(func() (any, error) {
				_, err := n.lb.EditPrefs(&ipn.MaskedPrefs{Prefs: ipn.Prefs{Hostname: name}, HostnameSet: true})
				return nil, err
			})
		}),
		"netmap":  fn(func([]js.Value) any { return n.netmapJSON() }),
		"request": fn(n.jsRequest),
		"socket":  fn(n.jsSocket),
	}
}

func (n *node) start() {
	n.emit("state", stateNames[ipn.NoState])
	n.lb.SetNotifyCallback(func(no ipn.Notify) {
		defer func() {
			// A panic here is the bridge's bug, not the node's: tell the page.
			if r := recover(); r != nil {
				log.Printf("tailnet: notify panic: %v", r)
			}
		}()
		if no.State != nil {
			n.emit("state", stateNames[*no.State])
		}
		if no.ErrMessage != nil {
			n.emit("error", *no.ErrMessage)
		}
		if no.BrowseToURL != nil {
			n.emit("browse", *no.BrowseToURL)
		}
		if no.LoginFinished != nil {
			n.emit("login", "")
		}
		if no.SelfChange != nil || len(no.PeersChanged) > 0 || len(no.PeersRemoved) > 0 || len(no.PeerChangedPatch) > 0 || no.State != nil {
			n.emit("netmap", "")
		}
	})
	go func() {
		err := n.lb.Start(ipn.Options{UpdatePrefs: &ipn.Prefs{
			ControlURL:  n.control,
			RouteAll:    false,
			WantRunning: true,
			Hostname:    n.hostname,
		}})
		if err != nil {
			n.emit("error", err.Error())
		}
	}()
}

// The network map as the page reads it: this device, its user and
// tailnet, and the peers it may see.
type jsMap struct {
	State     string   `json:"state"`
	Self      jsSelf   `json:"self"`
	Tailnet   string   `json:"tailnet"`
	Suffix    string   `json:"suffix"`
	LockedOut bool     `json:"lockedOut"`
	Peers     []jsPeer `json:"peers"`
}

type jsSelf struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	UserID      int64  `json:"userID"`
	Login       string `json:"login"`
	DisplayName string `json:"displayName"`
	KeyExpiry   int64  `json:"keyExpiry,omitempty"`
	Machine     string `json:"machineStatus"`
}

type jsPeer struct {
	// ID is the stable node ID.
	ID string `json:"id"`
	// Name is the MagicDNS name, without the final dot.
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	App      string `json:"app"`
	OS       string `json:"os"`
	UserID   int64  `json:"userID"`
	Tagged   bool   `json:"tagged"`
	Online   *bool  `json:"online,omitempty"`
}

var machineNames = map[tailcfg.MachineStatus]string{
	tailcfg.MachineUnknown:      "unknown",
	tailcfg.MachineUnauthorized: "unauthorized",
	tailcfg.MachineAuthorized:   "authorized",
	tailcfg.MachineInvalid:      "invalid",
}

func (n *node) netmapJSON() string {
	nm := n.lb.NetMapWithPeers()
	m := jsMap{State: stateNames[n.lb.State()]}
	if nm != nil && nm.SelfNode.Valid() {
		m.Self = jsSelf{
			Name:    strings.TrimSuffix(nm.SelfName(), "."),
			UserID:  int64(nm.User()),
			Machine: machineNames[nm.GetMachineStatus()],
		}
		if a := nm.GetAddresses(); a.Len() > 0 {
			m.Self.Address = a.At(0).Addr().String()
		}
		if up, ok := nm.UserProfiles[nm.User()]; ok {
			m.Self.Login, m.Self.DisplayName = up.LoginName(), up.DisplayName()
		}
		if t := nm.SelfKeyExpiry(); !t.IsZero() {
			m.Self.KeyExpiry = t.UnixMilli()
		}
		m.Tailnet = nm.TailnetDisplayName()
		if m.Tailnet == "" {
			m.Tailnet = nm.DomainName()
		}
		m.Suffix = nm.MagicDNSSuffix()
		m.LockedOut = nm.TKAEnabled && nm.SelfNode.KeySignature().Len() == 0
		for _, p := range nm.Peers {
			m.Peers = append(m.Peers, jsPeer{
				ID: string(p.StableID()), Name: strings.TrimSuffix(p.Name(), "."),
				Hostname: p.Hostinfo().Hostname(), App: p.Hostinfo().App(), OS: p.Hostinfo().OS(),
				UserID: int64(p.User()), Tagged: p.IsTagged(), Online: p.Online().Clone(),
			})
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// peerAddr is the tailnet address of the peer whose MagicDNS name is
// name, which is the only kind of host the engine connects to.
func (n *node) peerAddr(name string) (netip.Addr, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	nm := n.lb.NetMapWithPeers()
	if nm == nil {
		return netip.Addr{}, false
	}
	return peerAddrIn(nm, name)
}

func peerAddrIn(nm *netmap.NetworkMap, name string) (netip.Addr, bool) {
	for _, p := range nm.Peers {
		if strings.ToLower(strings.TrimSuffix(p.Name(), ".")) != name {
			continue
		}
		var v6 netip.Addr
		for _, a := range p.Addresses().All() {
			if !a.IsSingleIP() {
				continue
			}
			if a.Addr().Is4() {
				return a.Addr(), true
			}
			v6 = a.Addr()
		}
		return v6, v6.IsValid()
	}
	return netip.Addr{}, false
}

// promise runs f on a goroutine and settles a JS promise with its result,
// or rejects it with its error's text.
func promise(f func() (any, error)) js.Value {
	var handler js.Func
	handler = js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			defer handler.Release()
			if v, err := f(); err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
			} else {
				resolve.Invoke(v)
			}
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}
