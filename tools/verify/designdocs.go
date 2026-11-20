package main

// The design docs check. A design doc is docs/<CODE>-<NNN>-<slug>.md. It
// starts with fixed front matter, its code is a row of the Categories table in
// docs/DOC-001-design-docs.md, and its updated date moves when the branch
// changes the file. It runs in the process, not as a command.

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	docsDir       = "docs"
	categoriesDoc = "docs/DOC-001-design-docs.md"
	dateLayout    = "2006-01-02"
)

var (
	// docLike picks the files that must follow the rules: a capital first
	// letter and a dash followed by digits.
	docLike  = regexp.MustCompile(`^[A-Z].*-\d`)
	docName  = regexp.MustCompile(`^([A-Z]{2,4})-(\d{3,})-[a-z0-9]+(-[a-z0-9]+)*\.md$`)
	docTitle = regexp.MustCompile(`^# ([A-Z]{2,4}-\d{3,}): \S.*$`)
	fmKey    = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):`)
	fmSubKey = regexp.MustCompile(`^  ([A-Za-z_][A-Za-z0-9_-]*):`)
	fmName   = regexp.MustCompile(`^name: (\S+)$`)
	fmDesc   = regexp.MustCompile(`^description: "(.*)"$`)
	fmQuoted = regexp.MustCompile(`^(?:[^"\\]|\\.)*$`)
	fmDate   = regexp.MustCompile(`^  (created|updated): "(\d{4}-\d{2}-\d{2})"$`)
	catCell  = regexp.MustCompile("^`([A-Z]{2,4})`$")
)

// docLines splits a file into lines without line endings (LF or CRLF).
func docLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// parseCategories reads the codes from the table under "## Categories".
func parseCategories(content string) (map[string]bool, error) {
	cats := map[string]bool{}
	in := false
	for _, l := range docLines(content) {
		if strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(l[3:]) == "Categories"
			continue
		}
		if !in || !strings.HasPrefix(l, "|") {
			continue
		}
		cell, _, _ := strings.Cut(strings.TrimPrefix(l, "|"), "|")
		if m := catCell.FindStringSubmatch(strings.TrimSpace(cell)); m != nil {
			cats[m[1]] = true
		}
	}
	if len(cats) == 0 {
		return nil, fmt.Errorf("%s: no category rows under \"## Categories\"; add a table whose rows start with the code in backticks, like | `IOS` | iOS |", categoriesDoc)
	}
	return cats, nil
}

var (
	fmKeys    = []string{"name", "description", "metadata"}
	fmSubKeys = []string{"created", "updated"}
)

const (
	maxNameLen = 64
	maxDescLen = 1024
)

// frontMatterEnd returns the index of the closing "---" line, or -1.
func frontMatterEnd(ls []string) int {
	if len(ls) == 0 || ls[0] != "---" {
		return -1
	}
	for i := 1; i < len(ls); i++ {
		if ls[i] == "---" {
			return i
		}
	}
	return -1
}

// docDatesOf returns the metadata.created and metadata.updated dates of the
// front matter, if it has them in the fixed form. A base version in an older
// format (top-level created and updated keys, a Created line, or no header)
// has none.
func docDatesOf(content string) (created, updated string, ok bool) {
	ls := docLines(content)
	end := frontMatterEnd(ls)
	if end < 0 {
		return "", "", false
	}
	meta := false
	for _, l := range ls[1:end] {
		if l == "metadata:" {
			meta = true
		}
		if m := fmDate.FindStringSubmatch(l); m != nil && meta {
			if _, err := time.Parse(dateLayout, m[2]); err != nil {
				return "", "", false
			}
			if m[1] == "created" {
				created = m[2]
			} else {
				updated = m[2]
			}
		}
	}
	return created, updated, created != "" && updated != ""
}

// statusLineError checks the "Status: <text>" line. It is its own function
// so the rule is easy to delete. s is the line, n its 1-based number.
func statusLineError(p, s string, n int) string {
	if !strings.HasPrefix(s, "Status: ") || strings.TrimSpace(s[len("Status: "):]) == "" {
		return fmt.Sprintf("%s: line %d must be \"Status: <text>\"", p, n)
	}
	return ""
}

// validateDoc checks one design doc's name and content. name is the base
// name; cats is the set of known codes, or nil to skip the code rule.
func validateDoc(name, content string, cats map[string]bool, today time.Time) []string {
	p := docsDir + "/" + name
	m := docName.FindStringSubmatch(name)
	if m == nil {
		return []string{p + ": the name must look like <CODE>-<NNN>-<slug>.md with a 2 to 4 letter code, 3 or more digits and a lower-case slug (for example IOS-001-tailnet.md)"}
	}
	var errs []string
	if cats != nil && !cats[m[1]] {
		errs = append(errs, fmt.Sprintf("%s: code %s is not in the Categories table of %s; use a listed code or add a row", p, m[1], categoriesDoc))
	}
	id := m[1] + "-" + m[2]
	ls := docLines(content)
	end := frontMatterEnd(ls)
	if end < 0 {
		return append(errs, fmt.Sprintf("%s: line 1 must be \"---\" and the front matter must end with a second \"---\" line; start the file with name, description and metadata (see docs/DOC-001-design-docs.md)", p))
	}
	fm := ls[1:end]
	var found []string
	for _, l := range fm {
		if strings.HasPrefix(l, " ") {
			continue
		}
		if k := fmKey.FindStringSubmatch(l); k != nil {
			found = append(found, k[1])
		} else {
			found = append(found, fmt.Sprintf("%q", l))
		}
	}
	if strings.Join(found, ",") != strings.Join(fmKeys, ",") {
		return append(errs, fmt.Sprintf("%s: the front matter must have exactly the keys name, description, metadata in this order and no others; it has %s", p, strings.Join(found, ", ")))
	}
	if len(fm) < 3 || fm[2] != "metadata:" {
		return append(errs, p+": metadata must be one line \"metadata:\" after description, followed by two indented lines")
	}
	if n := fmName.FindStringSubmatch(fm[0]); n == nil || n[1] != id {
		errs = append(errs, fmt.Sprintf("%s: line 2 must be \"name: %s\" (the id from the file name)", p, id))
	} else if len(n[1]) > maxNameLen {
		errs = append(errs, fmt.Sprintf("%s: name is longer than %d characters; shorten it", p, maxNameLen))
	}
	if d := fmDesc.FindStringSubmatch(fm[1]); d == nil {
		errs = append(errs, fmt.Sprintf("%s: line 3 must be description: \"...\" with the text in double quotes", p))
	} else if !fmQuoted.MatchString(d[1]) {
		errs = append(errs, p+": the description has a double quote inside; write \\\" for it")
	} else if strings.TrimSpace(d[1]) == "" {
		errs = append(errs, p+": the description is empty; say what the doc decides and when to read it")
	} else if n := utf8.RuneCountInString(strings.ReplaceAll(d[1], `\"`, `"`)); n > maxDescLen {
		errs = append(errs, fmt.Sprintf("%s: the description has %d characters; shorten it to %d or fewer", p, n, maxDescLen))
	} else if !strings.Contains(d[1], "Use when") {
		errs = append(errs, p+": the description must say when to read the doc; add a sentence that starts with \"Use when\"")
	}
	dates := map[string]time.Time{}
	sub := fm[3:]
	var subFound []string
	for _, l := range sub {
		if k := fmSubKey.FindStringSubmatch(l); k != nil {
			subFound = append(subFound, k[1])
		} else {
			subFound = append(subFound, fmt.Sprintf("%q", l))
		}
	}
	if strings.Join(subFound, ",") != strings.Join(fmSubKeys, ",") {
		return append(errs, fmt.Sprintf("%s: metadata must have exactly the indented keys created, updated in this order and no others; it has %s", p, strings.Join(subFound, ", ")))
	}
	for i, k := range fmSubKeys {
		dm := fmDate.FindStringSubmatch(sub[i])
		if dm == nil || dm[1] != k {
			errs = append(errs, fmt.Sprintf("%s: line %d must be two spaces and metadata.%s: \"YYYY-MM-DD\" (date in double quotes)", p, 5+i, k))
			continue
		}
		t, err := time.Parse(dateLayout, dm[2])
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: metadata.%s %s is not a real date; use a real date in YYYY-MM-DD form", p, k, dm[2]))
			continue
		}
		dates[k] = t
	}
	c, u := dates["created"], dates["updated"]
	if !c.IsZero() && !u.IsZero() && c.After(u) {
		errs = append(errs, fmt.Sprintf("%s: metadata.created %s is after metadata.updated %s; fix the dates", p, c.Format(dateLayout), u.Format(dateLayout)))
	}
	if !u.IsZero() && u.After(today.AddDate(0, 0, 1)) {
		errs = append(errs, fmt.Sprintf("%s: metadata.updated %s is in the future (today is %s); set metadata.updated: \"%s\"", p, u.Format(dateLayout), today.Format(dateLayout), today.Format(dateLayout)))
	}
	line := func(i int) string {
		if i < len(ls) {
			return ls[i]
		}
		return ""
	}
	t := end + 2 // index of the title line
	if line(end+1) != "" {
		errs = append(errs, fmt.Sprintf("%s: line %d must be blank (one blank line after the closing \"---\")", p, end+2))
	}
	if tm := docTitle.FindStringSubmatch(line(t)); tm == nil {
		errs = append(errs, fmt.Sprintf("%s: line %d must be \"# %s: <title>\"", p, t+1, id))
	} else if tm[1] != id {
		errs = append(errs, fmt.Sprintf("%s: line %d names %s but the file name says %s; make them match", p, t+1, tm[1], id))
	}
	if line(t+1) != "" {
		errs = append(errs, fmt.Sprintf("%s: line %d must be blank (one blank line after the title)", p, t+2))
	}
	if e := statusLineError(p, line(t+2), t+3); e != "" {
		errs = append(errs, e)
	}
	return errs
}

// docChange is a design doc that differs from the merge base.
type docChange struct {
	path    string // current path, with slashes
	oldPath string // path in the base; "" for a new file
}

func gitOut(root string, args ...string) string {
	out, _ := git(root, args...)
	return out
}

// changedDocs lists the design docs added, modified or renamed since base,
// untracked ones included.
func changedDocs(root, base string) ([]docChange, error) {
	out, err := git(root, "diff", "--name-status", "-M", base, "--", docsDir)
	if err != nil {
		return nil, err
	}
	var cs []docChange
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(l, "\r"), "\t")
		if len(f) < 2 || f[0] == "" {
			continue
		}
		switch f[0][0] {
		case 'A':
			cs = append(cs, docChange{path: f[1]})
		case 'M', 'T':
			cs = append(cs, docChange{path: f[1], oldPath: f[1]})
		case 'R':
			if len(f) >= 3 {
				cs = append(cs, docChange{path: f[2], oldPath: f[1]})
			}
		}
	}
	for _, c := range cs {
		seen[c.path] = true
	}
	for _, p := range strings.Fields(gitOut(root, "ls-files", "--others", "--exclude-standard", "--", docsDir)) {
		if !seen[p] {
			cs = append(cs, docChange{path: p})
		}
	}
	var keep []docChange
	for _, c := range cs {
		if path.Dir(c.path) == docsDir && docLike.MatchString(path.Base(c.path)) {
			keep = append(keep, c)
		}
	}
	return keep, nil
}

// checkChange applies the rules on changed docs to one doc.
func checkChange(root, base string, c docChange, today time.Time) []string {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.path)))
	if err != nil {
		return nil
	}
	created, updated, ok := docDatesOf(string(b))
	if !ok {
		return nil // validateDoc reports the front matter
	}
	var errs []string
	if c.oldPath != "" {
		if bc, bu, ok := docDatesOf(gitOut(root, "show", base+":"+c.oldPath)); ok {
			if updated < bu {
				errs = append(errs, fmt.Sprintf("%s: metadata.updated is %s, but the base version says %s; it must not go back, set metadata.updated: \"%s\" or later", c.path, updated, bu, bu))
			}
			if created != bc {
				errs = append(errs, fmt.Sprintf("%s: metadata.created is %s, but the base version says %s; do not change it, set metadata.created: \"%s\"", c.path, created, bc, bc))
			}
		}
	}
	var need time.Time
	why := ""
	if d, err := time.Parse(dateLayout, strings.TrimSpace(gitOut(root, "log", "-1", "--date=short", "--format=%cd", base+"..HEAD", "--", c.path))); err == nil {
		need, why = d, "this branch changes the file on "+d.Format(dateLayout)
	}
	if strings.TrimSpace(gitOut(root, "status", "--porcelain", "--", c.path)) != "" && !today.Before(need) {
		need, why = today, "the file has uncommitted changes (today is "+today.Format(dateLayout)+")"
	}
	if u, err := time.Parse(dateLayout, updated); why != "" && err == nil && u.Before(need.AddDate(0, 0, -1)) {
		errs = append(errs, fmt.Sprintf("%s: metadata.updated is %s, but %s; set metadata.updated: \"%s\"", c.path, updated, why, need.Format(dateLayout)))
	}
	return errs
}

// checkDesignDocs runs every design doc rule for the repo at root. The
// rules on changes need the merge base with origin/main; without it, only
// the rules on the files themselves run.
func checkDesignDocs(root string, today time.Time) error {
	entries, err := os.ReadDir(filepath.Join(root, docsDir))
	if err != nil {
		return err
	}
	var errs []string
	var cats map[string]bool
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(categoriesDoc))); err != nil {
		errs = append(errs, fmt.Sprintf("%s: cannot read the category table: %v", categoriesDoc, err))
	} else if cats, err = parseCategories(string(b)); err != nil {
		errs = append(errs, err.Error())
	}
	ids := map[string][]string{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !docLike.MatchString(n) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, docsDir, n))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s/%s: %v", docsDir, n, err))
			continue
		}
		errs = append(errs, validateDoc(n, string(b), cats, today)...)
		if m := docName.FindStringSubmatch(n); m != nil {
			id := m[1] + "-" + m[2]
			ids[id] = append(ids[id], docsDir+"/"+n)
		}
	}
	var dups []string
	for id, fs := range ids {
		if len(fs) > 1 {
			dups = append(dups, id)
		}
	}
	sort.Strings(dups)
	for _, id := range dups {
		errs = append(errs, fmt.Sprintf("%s is used by %d files (%s); give each design doc its own number", id, len(ids[id]), strings.Join(ids[id], ", ")))
	}
	if base, err := git(root, "merge-base", "HEAD", "origin/main"); err == nil {
		base = strings.TrimSpace(base)
		cs, err := changedDocs(root, base)
		if err != nil {
			return err
		}
		for _, c := range cs {
			errs = append(errs, checkChange(root, base, c, today)...)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

// designDocsCheck is the in-process check that plan adds when the diff
// touches docs/.
func designDocsCheck(root string, log io.Writer) error {
	for _, w := range longDocs(root) {
		fmt.Fprintf(log, "warning: %s\n", w)
	}
	n := time.Now()
	return checkDesignDocs(root, time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC))
}

// maxBodyLines is the body length that DOC-001 recommends. A longer body
// gets a warning, not a failure.
const maxBodyLines = 500

// longDocs lists the docs whose body, the lines after the front matter,
// is longer than maxBodyLines.
func longDocs(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, docsDir))
	if err != nil {
		return nil
	}
	var ws []string
	for _, e := range entries {
		if e.IsDir() || !docLike.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, docsDir, e.Name()))
		if err != nil {
			continue
		}
		if n := bodyLines(string(b)); n > maxBodyLines {
			ws = append(ws, fmt.Sprintf("%s/%s: the body has %d lines; keep it under %d, or split the doc (DOC-001)", docsDir, e.Name(), n, maxBodyLines))
		}
	}
	return ws
}

// bodyLines counts the lines after the closing "---" of the front matter,
// or all lines when there is no front matter.
func bodyLines(content string) int {
	ls := docLines(strings.TrimRight(content, "\r\n"))
	if len(ls) > 0 && ls[0] == "---" {
		for i := 1; i < len(ls); i++ {
			if ls[i] == "---" {
				return len(ls) - i - 1
			}
		}
	}
	return len(ls)
}
