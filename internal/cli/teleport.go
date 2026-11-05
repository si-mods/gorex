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
	if h, err := dst.Hello(); err != nil {
		fmt.Fprintln(errOut, err)
		return ExitServer, true
	} else if !h.Can(rex.CapabilityTeleport) {
		fmt.Fprintf(errOut, "gorex teleport: GoRex on %s is too old to take a teleport\n", host)
		return ExitFailed, true
	}
	steps := map[string]string{rex.TeleportPacking: "packing", rex.TeleportSending: "sending to " + host, rex.TeleportStarting: "starting on " + host}
	in, err := rex.Teleport(context.Background(), src, sid, dst, func(step string) {
		fmt.Fprintf(errOut, "gorex teleport: %s…\n", steps[step])
	})
	if err != nil {
		fmt.Fprintf(errOut, "gorex teleport: %s\n", strings.TrimPrefix(err.Error(), "teleport: "))
		return ExitFailed, true
	}
	// The window makes its pane the host's session, and ends this one;
	// without one showing the pane, the session ends here.
	res, err := src.Command(rex.LayoutCommand{Op: "pane.teleported", SID: sid, Target: in.ID, Name: host})
	if err != nil || res.Code != "" {
		src.Kill(sid)
		fmt.Fprintf(errOut, "gorex teleport: the session is on %s; no window showed it, open it there\n", host)
	}
	if *asJSON {
		WriteJSON(out, in)
	} else {
		fmt.Fprintln(out, in.ID)
	}
	return ExitOK, true
}
