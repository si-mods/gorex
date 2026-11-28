package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Cheap hook guards for commands that target a GoRex checkout. They deny with
// a reason and never run verify or count toward maxBlocks.

var (
	sepRe     = regexp.MustCompile(`^(&&|\|\||;|\||\r?\n)`)
	contRe    = regexp.MustCompile("(\\\\|`)\r?\n")
	heredocRe = regexp.MustCompile(`<<-?\s*(?:'([^']+)'|"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))`)
	remoteRe  = regexp.MustCompile(`^git(\s+-C\s+\S+)?\s+(push|remote\s+(add|set-url))\b`)
	envRe     = regexp.MustCompile(`^(\$env:)?[A-Za-z_][A-Za-z0-9_]*=`)
	verifyRe  = regexp.MustCompile(`^go\s+run\s+\S*tools/verify\S*\s+(.*\s)?--?full\b`)
)

const ghReason = "gh writes in this repo must name their repo with -R; PRs and issues for GoRex go to si-mods/gorex, never egoist/gorex. If the user asked for something else, ask them."

const heavyReason = "CI runs the tests of every package a change reaches (tools/ciplan): the Linux job those and the root package when reached, test-windows those but the root. Locally run only the changed packages' tests, ideally with -run. Only if the user asked for a full run, prefix the command: `GOREX_HEAVY_TESTS=1 go test ...` (bash) or `$env:GOREX_HEAVY_TESTS=1; go test ...` (PowerShell)."

// ghWrites are the gh subcommands that change GitHub state.
var ghWrites = map[string]string{
	"pr":      " create merge comment edit close reopen review ready ",
	"issue":   " create comment edit close reopen delete transfer ",
	"release": " create edit upload delete ",
	"repo":    " edit ",
}

// valueFlags are go test flags that take a separate value word.
var valueFlags = map[string]bool{"-run": true, "-count": true, "-timeout": true, "-tags": true, "-p": true,
	"-parallel": true, "-bench": true, "-benchtime": true, "-cpu": true, "-coverprofile": true, "-covermode": true,
	"-coverpkg": true, "-shuffle": true, "-skip": true, "-vet": true, "-ldflags": true, "-gcflags": true,
	"-mod": true, "-exec": true, "-o": true, "-fuzz": true, "-fuzztime": true, "-cpuprofile": true,
	"-memprofile": true, "-memprofilerate": true, "-blockprofile": true, "-mutexprofile": true, "-trace": true,
	"-list": true, "-outputdir": true}

// normalize joins line continuations and drops heredoc bodies.
func normalize(cmd string) string {
	cmd = contRe.ReplaceAllString(cmd, " ")
	var out, delims []string
	for _, line := range strings.Split(cmd, "\n") {
		if len(delims) > 0 {
			if strings.TrimSpace(line) == delims[0] {
				delims = delims[1:]
			}
			continue
		}
		out = append(out, line)
		for _, m := range heredocRe.FindAllStringSubmatchIndex(line, -1) {
			if m[0] > 0 && line[m[0]-1] == '<' { // here-string <<<
				continue
			}
			for g := 1; g <= 3; g++ {
				if m[2*g] >= 0 {
					delims = append(delims, line[m[2*g]:m[2*g+1]])
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

// splitWords splits on whitespace outside quotes and removes the quotes.
func splitWords(s string) []string {
	var words []string
	var cur strings.Builder
	var quote byte
	has := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, has = c, true
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			if has || cur.Len() > 0 {
				words = append(words, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteByte(c)
		}
	}
	if has || cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// segments splits a command at ; && || | and newlines outside quotes, drops
// # comments, and returns each segment's words without leading `(`, `&` and
// VAR=x words.
func segments(cmd string) [][]string {
	cmd = normalize(cmd)
	var out [][]string
	var cur strings.Builder
	var quote byte
	flush := func() {
		w := splitWords(cur.String())
		cur.Reset()
		for len(w) > 0 {
			w[0] = strings.TrimLeft(w[0], "(")
			if w[0] == "" || w[0] == "&" || len(w) > 1 && envRe.MatchString(w[0]) {
				w = w[1:]
				continue
			}
			break
		}
		if len(w) > 0 {
			out = append(out, w)
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
		case c == '#' && (i == 0 || strings.ContainsRune(" \t\r\n;&|(", rune(cmd[i-1]))):
			for i < len(cmd) && cmd[i] != '\n' {
				i++
			}
			continue
		default:
			if m := sepRe.FindString(cmd[i:]); m != "" {
				flush()
				i += len(m)
				continue
			}
		}
		cur.WriteByte(c)
		i++
	}
	flush()
	return out
}

// ghRepoOwner returns the owner named by the segment's -R/--repo flag. Only
// real arguments count, not text inside a quoted value.
func ghRepoOwner(w []string) (owner string, ok bool) {
	for i, a := range w {
		v := ""
		switch {
		case (a == "-R" || a == "--repo") && i+1 < len(w):
			v = w[i+1]
		case strings.HasPrefix(a, "--repo="):
			v = a[len("--repo="):]
		case strings.HasPrefix(a, "-R="):
			v = a[len("-R="):]
		default:
			continue
		}
		v = strings.ToLower(strings.TrimSuffix(v, ".git"))
		v = strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
		v = strings.TrimPrefix(v, "github.com/")
		parts := strings.Split(v, "/")
		if len(parts) >= 2 { // OWNER/REPO or HOST/OWNER/REPO
			return parts[len(parts)-2], true
		}
		return v, true
	}
	return "", false
}

func isGH(w []string) bool {
	h := strings.ToLower(w[0])
	h = h[strings.LastIndexAny(h, `/\`)+1:]
	return h == "gh" || h == "gh.exe"
}

// heavyTest reports whether a segment is a `go test` that runs the root
// package or a top-level subtree without -run. root is the repo root and rel
// the directory the command runs in, relative to it.
func heavyTest(w []string, root, rel string) bool {
	join := func(d, p string) string {
		if filepath.IsAbs(p) {
			r, err := filepath.Rel(root, p)
			if err != nil {
				return ".."
			}
			return filepath.ToSlash(r)
		}
		return path.Clean(path.Join(d, filepath.ToSlash(p)))
	}
	d, i := rel, 1
	if len(w) > 3 && w[1] == "-C" { // go -C dir test
		d, i = join(d, w[2]), 3
	}
	if i >= len(w) || w[0] != "go" || w[i] != "test" {
		return false
	}
	var pkgs []string
	for i++; i < len(w); i++ {
		a := w[i]
		switch {
		case a == "-args":
			i = len(w)
		case a == "-run" || a == "--run" || strings.HasPrefix(a, "-run=") || strings.HasPrefix(a, "--run="):
			return false
		case a == "-C" && i+1 < len(w):
			d = join(d, w[i+1])
			i++
		case strings.HasPrefix(a, "-C="):
			d = join(d, a[3:])
		case strings.HasPrefix(a, "-"):
			if valueFlags["-"+strings.TrimLeft(a, "-")] {
				i++
			}
		default:
			pkgs = append(pkgs, a)
		}
	}
	if len(pkgs) == 0 {
		pkgs = []string{"."}
	}
	for _, p := range pkgs {
		p = filepath.ToSlash(p)
		switch {
		case p == "gorex" || strings.HasPrefix(p, "gorex/"):
			p = "./" + strings.TrimPrefix(strings.TrimPrefix(p, "gorex"), "/")
		case p != "." && p != ".." && !strings.HasPrefix(p, "./") && !strings.HasPrefix(p, "../") && !filepath.IsAbs(p):
			continue // an import path or a keyword we do not judge
		}
		pat := strings.HasSuffix(p, "...")
		if pat {
			p = strings.TrimSuffix(strings.TrimSuffix(p, "..."), "/")
			if p == "" || p == "./" {
				p = "."
			}
		}
		full := join(d, p)
		if full == ".." || strings.HasPrefix(full, "../") {
			continue
		}
		if full == "." || pat && !strings.Contains(full, "/") {
			return true
		}
	}
	return false
}

// guardReason returns why a command must not run in a GoRex checkout, or "".
// where is called only when a command looks suspect; it returns the repo
// root and the directory the command runs in, and false outside GoRex.
func guardReason(cmd string, where func() (root, dir string, ok bool)) string {
	heavyOK := os.Getenv("CI") != "" || strings.Contains(cmd, "GOREX_HEAVY_TESTS=1")
	for _, w := range segments(cmd) {
		line := strings.Join(w, " ")
		gh := isGH(w)
		if (gh || remoteRe.MatchString(line)) && strings.Contains(strings.ToLower(line), "egoist/gorex") {
			if _, _, ok := where(); ok {
				return ghReason
			}
			return ""
		}
		if gh && len(w) > 2 && strings.Contains(ghWrites[w[1]], " "+w[2]+" ") {
			if owner, ok := ghRepoOwner(w); !ok || owner == "egoist" {
				if _, _, ok := where(); ok {
					return ghReason
				}
				return ""
			}
		}
		if heavyOK || strings.ToLower(w[0]) == "ssh" {
			continue
		}
		heavy := verifyRe.MatchString(line)
		if !heavy && w[0] == "go" {
			root, dir, ok := where()
			if !ok {
				return ""
			}
			rel, err := filepath.Rel(root, dir)
			if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "..") {
				rel = "."
			}
			heavy = heavyTest(w, root, filepath.ToSlash(rel))
		}
		if heavy {
			if _, _, ok := where(); ok {
				return heavyReason
			}
			return ""
		}
	}
	return ""
}
