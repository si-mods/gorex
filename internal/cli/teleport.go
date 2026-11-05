package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gorex/internal/rex"
)

// teleportCmd moves a pane's work to a host (docs/teleport.md): this
// machine's server packs it, the host's starts it again, and the window
// shows the pane as the host's session; the pane's own session ends.
func teleportCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("teleport", errOut)
	asJSON := fs.Bool("json", false, "print the new session as JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if fs.NArg() != 2 || serverTarget != "" {
		fmt.Fprintln(errOut, "gorex teleport: use `gorex teleport [--json] PANE HOST`, PANE of this machine")
		return ExitUsage, true
	}
	src, code := connect(errOut)
	if src == nil {
		return code, true
	}
	defer src.Close()
	sid, err := resolveTarget(src, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return codeFor(err), true
	}
	host := fs.Arg(1)
	serverTarget = host
	dst, code := connect(errOut)
	serverTarget = ""
	if dst == nil {
		return code, true
	}
	defer dst.Close()
	// The window knows the host by its address, as hosts.json has it.
	addr := serverAddress
	if h, err := dst.Hello(); err != nil {
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	} else if !h.Can(rex.CapabilityTeleport) {
		fmt.Fprintf(errOut, "gorex teleport: GoRex on %s is too old to take a teleport\n", host)
		return ExitFailed, true
	}
	steps := map[string]string{rex.TeleportPacking: "packing", rex.TeleportSending: "sending to " + host, rex.TeleportStarting: "starting on " + host}
	in, err := rex.Teleport(context.Background(), src, sid, dst, func(step, newSID string) {
		fmt.Fprintf(errOut, "gorex teleport: %s…\n", steps[step])
		if step == rex.TeleportSending {
			// The window waits for the session, so as not to open it as a
			// tab of its own before the pane becomes it.
			src.Command(rex.LayoutCommand{Op: "pane.teleporting", SID: sid, Target: newSID, Host: addr})
		}
	})
	if err != nil {
		fmt.Fprintf(errOut, "gorex teleport: %s\n", strings.TrimPrefix(err.Error(), "teleport: "))
		return ExitFailed, true
	}
	// The window makes its pane the host's session, and ends this one.
	// With no window showing the pane, the session ends here; with one
	// that cannot show the new session, both stay, for the user to say.
	res, err := src.Command(rex.LayoutCommand{Op: "pane.teleported", SID: sid, Target: in.ID, Host: addr})
	code = ExitOK
	switch {
	case err == nil && res.Code == "":
	case err == nil && (res.Code == "desktop_unavailable" || res.Code == "pane_not_found"):
		src.Kill(sid)
		fmt.Fprintf(errOut, "gorex teleport: the session is on %s; no window showed the pane, open it there\n", host)
	default:
		why := res.Error
		if err != nil {
			why = err.Error()
		}
		fmt.Fprintf(errOut, "gorex teleport: the session is on %s as %s, and the pane here still runs: %s\n", host, in.ID, why)
		code = ExitFailed
	}
	if *asJSON {
		WriteJSON(out, in)
	} else {
		fmt.Fprintln(out, in.ID)
	}
	return code, true
}
