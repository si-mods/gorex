package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gorex/internal/layout"
)

const maxCLIIconBytes = 32 * 1024

func iconCmd(args []string, out, errOut io.Writer) (int, bool) {
	fs := flagSet("icon", errOut)
	bg := fs.String("bg", "", "tile background color as 6-digit hex (e.g. d97757)")
	fg := fs.String("fg", "", "tile foreground color as 6-digit hex (e.g. ffffff)")
	mode := fs.String("mode", "", "icon color mode: mono or color")
	format := fs.String("format", "", "icon format: svg or png (defaults to file extension)")
	if code, stop := parse(fs, args); stop {
		return code, true
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errOut, "gorex icon: pass an icon file path or 'clear'")
		return ExitUsage, true
	}
	arg := fs.Arg(0)
	if arg == "clear" {
		fmt.Fprint(out, "\x1b]7502;clear\x1b\\")
		return ExitOK, true
	}
	fmtName := strings.ToLower(strings.TrimSpace(*format))
	if fmtName == "" {
		switch strings.ToLower(filepath.Ext(arg)) {
		case ".svg":
			fmtName = "svg"
		case ".png":
			fmtName = "png"
		}
	}
	if fmtName != "svg" && fmtName != "png" {
		fmt.Fprintln(errOut, "gorex icon: format must be svg or png")
		return ExitUsage, true
	}
	m := strings.ToLower(strings.TrimSpace(*mode))
	if m != "" && m != "mono" && m != "color" {
		fmt.Fprintln(errOut, "gorex icon: mode must be mono or color")
		return ExitUsage, true
	}
	if fmtName == "png" && m == "mono" {
		fmt.Fprintln(errOut, "gorex icon: png icons only support color mode")
		return ExitUsage, true
	}
	parts := []string{"format=" + fmtName}
	if m != "" {
		parts = append(parts, "mode="+m)
	}
	if *bg != "" {
		h, ok := normalizeHexFlag(*bg)
		if !ok {
			fmt.Fprintln(errOut, "gorex icon: invalid -bg hex color")
			return ExitUsage, true
		}
		parts = append(parts, "bg="+h)
	}
	if *fg != "" {
		h, ok := normalizeHexFlag(*fg)
		if !ok {
			fmt.Fprintln(errOut, "gorex icon: invalid -fg hex color")
			return ExitUsage, true
		}
		parts = append(parts, "fg="+h)
	}
	raw, err := os.ReadFile(arg)
	if err != nil {
		fmt.Fprintf(errOut, "gorex icon: %v\n", err)
		return ExitFailed, true
	}
	if len(raw) == 0 || len(raw) > maxCLIIconBytes {
		fmt.Fprintf(errOut, "gorex icon: file must be between 1 and %d bytes\n", maxCLIIconBytes)
		return ExitUsage, true
	}
	parts = append(parts, "data="+base64.StdEncoding.EncodeToString(raw))
	fmt.Fprintf(out, "\x1b]7502;%s\x1b\\", strings.Join(parts, ":"))
	return ExitOK, true
}

func normalizeHexFlag(s string) (string, bool) {
	return layout.NormalizeHex6(strings.TrimSpace(s))
}
