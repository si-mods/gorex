package main

import (
	"os"
	"regexp"
	"strings"
)

// Cheap hook guards for commands that target a GoRex checkout. They deny with
// a reason and never run verify or count toward maxBlocks.

var (
	sepRe     = regexp.MustCompile(`^(&&|\|\||;|\||\r?\n)`)
	ghWriteRe = regexp.MustCompile(`^gh\s+(pr\s+(create|merge|comment|edit|close|reopen|review)|issue\s+(create|comment|edit|close)|release\s+create)\b`)
	ghRepoRe  = regexp.MustCompile(`(^|\s)(-R|--repo)(\s+|=)["']?si-mods/gorex["']?(\s|$)`)
	remoteRe  = regexp.MustCompile(`^git(\s+-C\s+("[^"]*"|'[^']*'|\S+))?\s+(push|remote\s+(add|set-url))\b`)
	envRe     = regexp.MustCompile(`^(\$env:)?[A-Za-z_][A-Za-z0-9_]*=\S*$`)
	verifyRe  = regexp.MustCompile(`^go\s+run\s+\S*tools/verify\S*\s+(.*\s)?--?full\b`)
	sshRe     = regexp.MustCompile(`(^|[\s;&|(])ssh\s`)
)

const ghReason = "PRs and issues go to si-mods/gorex only, never egoist/gorex. Pass `-R si-mods/gorex` to every gh write command (set GOREX_VERIFY=off to bypass this check)."

const heavyReason = "Full test suites run in CI (test-linux shards, test-windows). Locally run only the changed packages' tests, ideally with -run. If the user asked for a full run, prefix the command with GOREX_HEAVY_TESTS=1."

// valueFlags are go test flags that take a separate value word.
var valueFlags = map[string]bool{"-run": true, "-count": true, "-timeout": true, "-tags": true, "-p": true,
	"-parallel": true, "-bench": true, "-benchtime": true, "-cpu": true, "-coverprofile": true, "-covermode": true,
	"-coverpkg": true, "-shuffle": true, "-skip": true, "-vet": true, "-ldflags": true, "-gcflags": true,
	"-mod": true, "-exec": true, "-C": true, "-o": true, "-fuzz": true, "-fuzztime": true}

// segments splits a shell command at ; && || | and newlines outside quotes,
// and strips each segment's leading `(`, `&` and VAR=x words.
func segments(cmd string) []string {
	var out []string
	var quote byte
	start := 0
	flush := func(end int) {
		seg := strings.TrimSpace(cmd[start:end])
		for {
			f := strings.TrimLeft(seg, "(& \t")
			if w := strings.Fields(f); len(w) > 1 && envRe.MatchString(w[0]) {
				f = strings.TrimSpace(strings.TrimPrefix(f, w[0]))
			}
			if f == seg {
				break
			}
			seg = f
		}
		if seg != "" {
			out = append(out, seg)
		}
	}
	for i := 0; i < len(cmd); {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		default:
			if m := sepRe.FindString(cmd[i:]); m != "" {
				flush(i)
				i += len(m)
				start = i
				continue
			}
		}
		i++
	}
	flush(len(cmd))
	return out
}

// heavyTest reports whether a segment is a `go test` over ./... or the root
// package without -run.
func heavyTest(seg string) bool {
	w := strings.Fields(seg)
	if len(w) < 2 || w[0] != "go" || w[1] != "test" {
		return false
	}
	var pkgs []string
	for i := 2; i < len(w); i++ {
		a := strings.Trim(w[i], `"'`)
		switch {
		case a == "-args":
			i = len(w)
		case a == "-run" || a == "--run" || strings.HasPrefix(a, "-run=") || strings.HasPrefix(a, "--run="):
			return false
		case strings.HasPrefix(a, "-"):
			if valueFlags["-"+strings.TrimLeft(a, "-")] && !strings.Contains(a, "=") {
				i++
			}
		default:
			pkgs = append(pkgs, a)
		}
	}
	if len(pkgs) == 0 {
		return true
	}
	for _, p := range pkgs {
		if strings.Contains(p, "...") || p == "." || p == "./" {
			return true
		}
	}
	return false
}

// guardReason returns why a command must not run in a GoRex checkout, or "".
func guardReason(cmd string) string {
	heavyOK := os.Getenv("CI") != "" || strings.Contains(cmd, "GOREX_HEAVY_TESTS=1") || sshRe.MatchString(cmd)
	for _, seg := range segments(cmd) {
		isGH := strings.HasPrefix(seg, "gh ")
		if (isGH || remoteRe.MatchString(seg)) && strings.Contains(strings.ToLower(seg), "egoist/gorex") {
			return ghReason
		}
		if isGH && ghWriteRe.MatchString(seg) && !ghRepoRe.MatchString(seg) {
			return ghReason
		}
		if !heavyOK && (heavyTest(seg) || verifyRe.MatchString(seg)) {
			return heavyReason
		}
	}
	return ""
}
