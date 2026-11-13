package tailnet

import (
	"encoding/json"
	"sync"

	"gorex/ios/share"
)

// ShareDelegate receives hotspot admission and device-list updates on iOS.
// gomobile binds this interface to a Swift protocol.
type ShareDelegate interface {
	OnPending(ip, userAgent string)
	OnChange()
}

var (
	shareMu  sync.Mutex
	shareSrv *share.Server
)

// ShareHotspotIP returns the iPhone's Personal Hotspot IPv4 address (172.20.10.0/28),
// or "" if Personal Hotspot is off.
func ShareHotspotIP() string {
	return share.HotspotIP()
}

// ShareStart starts the hotspot web share server on addr (for example "172.20.10.1:7300").
func ShareStart(addr, webDir, phoneName string, delegate ShareDelegate) error {
	shareMu.Lock()
	if shareSrv != nil {
		shareSrv.Stop()
		shareSrv = nil
	}
	shareMu.Unlock()

	srv, err := share.Start(share.Config{
		Addr:      addr,
		WebDir:    webDir,
		PhoneName: phoneName,
		DialProxy: dial,
		OnPending: func(d share.Device) {
			if delegate != nil {
				delegate.OnPending(d.IP, d.UserAgent)
			}
		},
		OnChange: func() {
			if delegate != nil {
				delegate.OnChange()
			}
		},
	})
	if err != nil {
		return err
	}
	shareMu.Lock()
	shareSrv = srv
	shareMu.Unlock()
	return nil
}

// ShareStop stops the hotspot web share server.
func ShareStop() {
	shareMu.Lock()
	srv := shareSrv
	shareSrv = nil
	shareMu.Unlock()
	if srv != nil {
		srv.Stop()
	}
}

// ShareAlive reports whether the hotspot web share server is still listening.
func ShareAlive() bool {
	shareMu.Lock()
	srv := shareSrv
	shareMu.Unlock()
	return srv != nil && srv.Alive()
}

// ShareSetHosts updates the shared host list from JSON.
func ShareSetHosts(hostsJSON string) error {
	shareMu.Lock()
	srv := shareSrv
	shareMu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.SetHosts(hostsJSON)
}

// ShareSetPhoneName updates the iPhone name shown in the web client's host list.
func ShareSetPhoneName(name string) {
	shareMu.Lock()
	srv := shareSrv
	shareMu.Unlock()
	if srv != nil {
		srv.SetPhoneName(name)
	}
}

// ShareAdmit allows a hotspot client IP to access the web client and shared hosts.
func ShareAdmit(ip string) {
	shareMu.Lock()
	srv := shareSrv
	shareMu.Unlock()
	if srv != nil {
		srv.Admit(ip)
	}
}

// ShareDeny denies a hotspot client IP and closes its connections.
func ShareDeny(ip string) {
	shareMu.Lock()
	srv := shareSrv
	shareMu.Unlock()
	if srv != nil {
		srv.Deny(ip)
	}
}

// ShareRevoke removes a hotspot client IP and closes its connections.
func ShareRevoke(ip string) {
	shareMu.Lock()
	srv := shareSrv
	shareMu.Unlock()
	if srv != nil {
		srv.Revoke(ip)
	}
}

// ShareDevicesJSON returns the current hotspot devices as a JSON array.
func ShareDevicesJSON() string {
	shareMu.Lock()
	srv := shareSrv
	shareMu.Unlock()
	if srv == nil {
		return "[]"
	}
	b, err := json.Marshal(srv.Devices())
	if err != nil {
		return "[]"
	}
	return string(b)
}
