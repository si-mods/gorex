package tailnet

import "tailscale.com/net/netmon"

// setDefaultInterface is where netmon finds the default route on iOS
// (and macOS), as Tailscale's own app tells it.
func setDefaultInterface(name string) { netmon.UpdateLastKnownDefaultRouteInterface(name) }
