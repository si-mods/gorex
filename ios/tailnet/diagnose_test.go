package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
)

func TestFromPeer(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, tc := range []struct {
		p    ipnstate.PeerStatus
		want string
	}{
		{ipnstate.PeerStatus{DNSName: "mac-gorex.example.ts.net.", Online: true, Active: true, CurAddr: "203.0.113.5:41641", Relay: "fra",
			LastHandshake: now.Add(-12 * time.Second), RxBytes: 900, TxBytes: 1500},
			`{"name":"mac-gorex.example.ts.net","online":true,"path":"direct","endpoint":"203.0.113.5:41641","relay":"fra","handshake":12,"rx":900,"tx":1500}`},
		{ipnstate.PeerStatus{Online: true, Active: true, Relay: "fra"},
			`{"online":true,"path":"derp","relay":"fra","rx":0,"tx":0}`},
		{ipnstate.PeerStatus{Online: true, Active: true, PeerRelay: "198.51.100.2:7000:vni:1", Relay: "fra"},
			`{"online":true,"path":"peer-relay","endpoint":"198.51.100.2:7000:vni:1","relay":"fra","rx":0,"tx":0}`},
		{ipnstate.PeerStatus{}, `{"online":false,"rx":0,"tx":0}`},
	} {
		if b, _ := json.Marshal(fromPeer(&tc.p, now)); string(b) != tc.want {
			t.Errorf("fromPeer(%+v) = %s, want %s", tc.p, b, tc.want)
		}
	}
}

func TestFromPing(t *testing.T) {
	for _, tc := range []struct {
		r    *ipnstate.PingResult
		err  error
		want string
	}{
		{&ipnstate.PingResult{LatencySeconds: 0.01234, Endpoint: "203.0.113.5:41641", DERPRegionCode: "fra"}, nil,
			`{"size":1200,"ms":12.3,"path":"direct","endpoint":"203.0.113.5:41641"}`},
		{&ipnstate.PingResult{LatencySeconds: 0.2, DERPRegionID: 4, DERPRegionCode: "fra"}, nil,
			`{"size":1200,"ms":200,"path":"derp","endpoint":"fra"}`},
		{&ipnstate.PingResult{LatencySeconds: 0.05, PeerRelay: "198.51.100.2:7000:vni:1"}, nil,
			`{"size":1200,"ms":50,"path":"peer-relay","endpoint":"198.51.100.2:7000:vni:1"}`},
		{&ipnstate.PingResult{Err: "ping too big"}, nil, `{"size":1200,"error":"ping too big"}`},
		// The local client wraps the context's error.
		{nil, fmt.Errorf("error %w: ", context.DeadlineExceeded), `{"size":1200,"error":"no pong in 5s"}`},
		{nil, errors.New("tailnet not started"), `{"size":1200,"error":"tailnet not started"}`},
	} {
		if b, _ := json.Marshal(fromPing(1200, tc.r, tc.err)); string(b) != tc.want {
			t.Errorf("fromPing(%+v, %v) = %s, want %s", tc.r, tc.err, b, tc.want)
		}
	}
}

// Before Start, both tell that the tailnet is not started.
func TestDiagnoseNotStarted(t *testing.T) {
	if got := PeerPath("mac-gorex"); got != `{"online":false,"rx":0,"tx":0,"error":"tailnet not started"}` {
		t.Errorf("PeerPath() = %s", got)
	}
	if got := Ping("mac-gorex", 64); got != `{"size":64,"error":"tailnet not started"}` {
		t.Errorf("Ping() = %s", got)
	}
}
