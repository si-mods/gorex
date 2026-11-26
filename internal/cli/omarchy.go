package cli

import (
	"fmt"
	"io"

	"gorex/internal/omarchy"
)

// omarchyCmd is `gorex omarchy`: it reads an Omarchy desktop, and keeps the
// optional key binding (docs/DSK-015-omarchy.md).
func omarchyCmd(args []string, out, errOut io.Writer) (int, bool) {
	if serverTarget != "" {
		fmt.Fprintln(errOut, "gorex omarchy: this changes this machine only")
		return ExitUsage, true
	}
	if len(args) == 0 {
		return omarchyUsage(errOut)
	}
	switch args[0] {
	case "status":
		return omarchyStatus(args[1:], out, errOut)
	case "bind":
		return omarchyBind(args[1:], out, errOut)
	case "unbind":
		return omarchyUnbind(args[1:], out, errOut)
	}
	return omarchyUsage(errOut)
}

// omarchyUsage says how the command is used.
func omarchyUsage(errOut io.Writer) (int, bool) {
	fmt.Fprintln(errOut, "gorex omarchy: status [--json] | bind [--keys KEYS] | unbind [--json]")
	return ExitUsage, true
}

// omarchyFail prints why the command failed, and returns its code.
func omarchyFail(errOut io.Writer, err error) (int, bool) {
	fmt.Fprintf(errOut, "gorex omarchy: %v\n", err)
	return ExitFailed, true
}

func omarchyStatus(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("omarchy status", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errOut, "gorex omarchy status: unexpected argument")
		return ExitUsage, true
	}
	p := omarchy.DefaultPaths()
	r := omarchy.Status(p, omarchy.ReadSettings(p.SettingsPath).Follows())
	if *asJSON {
		WriteJSON(out, r)
	} else {
		writeReport(out, r)
	}
	// A machine without Omarchy still has a report, so status exits 0.
	return ExitOK, true
}

// writeReport prints a status report as lines.
func writeReport(out io.Writer, r omarchy.Report) {
	if r.Omarchy {
		fmt.Fprintf(out, "omarchy: yes (%s)\n", r.Root)
	} else {
		fmt.Fprintln(out, "omarchy: no")
	}
	if r.Theme != "" {
		fmt.Fprintf(out, "theme: %s (%s)\n", r.Theme, r.Mode)
	}
	fmt.Fprintf(out, "following: %s\n", yesNo(r.Following))
	if r.BindingInstalled {
		fmt.Fprintf(out, "binding: %s\n", r.Binding)
	} else {
		fmt.Fprintln(out, "binding: none")
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(out, "warning:", w)
	}
}

// yesNo spells a flag.
func yesNo(on bool) string {
	if on {
		return "yes"
	}
	return "no"
}

func omarchyBind(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("omarchy bind", errOut)
	keys := fs.String("keys", omarchy.DefaultBinding, "Hyprland keys to bind")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errOut, "gorex omarchy bind: unexpected argument")
		return ExitUsage, true
	}
	if !omarchy.ValidKeys(*keys) {
		fmt.Fprintln(errOut, "gorex omarchy bind: --keys needs keys without a quote or a control character")
		return ExitUsage, true
	}
	p := omarchy.DefaultPaths()
	if err := omarchy.Detect(p); err != nil {
		return omarchyFail(errOut, err)
	}
	c, err := omarchy.Bind(p, *keys)
	if err != nil {
		return omarchyFail(errOut, err)
	}
	writeChange(out, c)
	return ExitOK, true
}

func omarchyUnbind(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("omarchy unbind", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errOut, "gorex omarchy unbind: unexpected argument")
		return ExitUsage, true
	}
	p := omarchy.DefaultPaths()
	if err := omarchy.Detect(p); err != nil {
		return omarchyFail(errOut, err)
	}
	c, err := omarchy.Unbind(p)
	if err != nil {
		return omarchyFail(errOut, err)
	}
	if *asJSON {
		WriteJSON(out, c)
		return ExitOK, true
	}
	writeChange(out, c)
	return ExitOK, true
}

// writeChange says what bind or unbind made of the file.
func writeChange(out io.Writer, c omarchy.Change) {
	switch c.Action {
	case omarchy.ActionRemove:
		fmt.Fprintf(out, "removed %s\n", c.Path)
	case omarchy.ActionWrite:
		fmt.Fprintf(out, "wrote %s\n", c.Path)
	default:
		fmt.Fprintf(out, "unchanged %s\n", c.Path)
	}
}
