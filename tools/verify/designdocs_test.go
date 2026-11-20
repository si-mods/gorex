package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testToday = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

const goodDesc = `"Says what the doc decides. Use when you change the thing."`

func docWith(id, desc, created, updated string) string {
	return "---\nname: " + id + "\ndescription: " + desc + "\nmetadata:\n  created: \"" + created + "\"\n  updated: \"" + updated + "\"\n---\n\n# " + id + ": Title\n\nStatus: draft\n\n" + strings.Repeat("Body line of the design doc.\n", 20)
}

func doc(id, created, updated string) string {
	return docWith(id, goodDesc, created, updated)
}

func TestDesignDocsPlan(t *testing.T) {
	f := facts{goos: "linux"}
	if c := find(plan([]string{"docs/IOS-001-x.md"}, f), "design docs"); c.fn == nil {
		t.Error("docs change should add the design docs check")
	}
	if c := find(plan([]string{"main.go"}, f), "design docs"); c.name != "" {
		t.Error("no docs change should not add it")
	}
	f.readOnly = true
	if c := find(plan([]string{"docs/a.md"}, f), "design docs"); c.skip != "" {
		t.Error("design docs check reads only; it must run in read-only mode")
	}
}

func TestParseCategories(t *testing.T) {
	md := "# T\r\n\r\n## Other\r\n\r\n| `ZZZ` | no |\r\n\r\n## Categories\r\n\r\n| Code | Name |\r\n|---|---|\r\n| `IOS` | iOS |\r\n| `DSK` | Desktop |\r\n\r\n## Next\r\n\r\n| `NOP` | x |\r\n"
	got, err := parseCategories(md)
	if err != nil || len(got) != 2 || !got["IOS"] || !got["DSK"] {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseCategories("# T\n\n## Other\n"); err == nil {
		t.Error("missing table should fail")
	}
}

func TestValidateDoc(t *testing.T) {
	cats := map[string]bool{"IOS": true}
	tests := []struct {
		name, file, content, want string // want "" means no error
	}{
		{"ok", "IOS-001-tailnet.md", doc("IOS-001", "2026-11-09", "2026-11-20"), ""},
		{"ok crlf", "IOS-001-tailnet.md", strings.ReplaceAll(doc("IOS-001", "2026-11-09", "2026-11-20"), "\n", "\r\n"), ""},
		{"ok tomorrow", "IOS-001-tailnet.md", doc("IOS-001", "2026-11-09", "2026-11-21"), ""},
		{"bad name caps", "IOS-001-Tailnet.md", doc("IOS-001", "2026-11-09", "2026-11-20"), "name must look like"},
		{"bad name digits", "IOS-01-tailnet.md", doc("IOS-01", "2026-11-09", "2026-11-20"), "name must look like"},
		{"bad name code", "IOSXX-001-tailnet.md", doc("IOSXX-001", "2026-11-09", "2026-11-20"), "name must look like"},
		{"bad name ext", "IOS-001-tailnet.txt", doc("IOS-001", "2026-11-09", "2026-11-20"), "name must look like"},
		{"unknown code", "NET-001-x.md", doc("NET-001", "2026-11-09", "2026-11-20"), "code NET is not in the Categories"},
		{"title id", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "# IOS-001:", "# IOS-002:", 1), "names IOS-002 but the file name says IOS-001"},
		{"title form", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "# IOS-001: Title", "IOS-001: Title", 1), "line 9 must be \"# IOS-001: <title>\""},
		{"no blank after front matter", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "---\n\n#", "---\n#", 1), "line 8 must be blank"},
		{"no front matter", "IOS-001-x.md", "# IOS-001: T\n\nStatus: x\n", "line 1 must be \"---\""},
		{"old format", "IOS-001-x.md", "# IOS-001: T\n\nStatus: x\n\nCreated: 2026-11-09. Updated: 2026-11-20.\n", "line 1 must be \"---\""},
		{"unclosed front matter", "IOS-001-x.md", "---\nname: IOS-001\n", "second \"---\" line"},
		{"missing description", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "description: "+goodDesc+"\n", "", 1), "exactly the keys name, description, metadata"},
		{"description without Use when", "IOS-001-x.md", docWith("IOS-001", `"Says what the doc decides."`, "2026-11-09", "2026-11-20"), "must say when to read the doc; add a sentence that starts with \"Use when\""},
		{"description empty", "IOS-001-x.md", docWith("IOS-001", `""`, "2026-11-09", "2026-11-20"), "description is empty"},
		{"description unquoted", "IOS-001-x.md", docWith("IOS-001", `Use when you read it.`, "2026-11-09", "2026-11-20"), "double quotes"},
		{"description escaped quote", "IOS-001-x.md", docWith("IOS-001", `"Sets the \"x\" flag. Use when you change it."`, "2026-11-09", "2026-11-20"), ""},
		{"description bare quote", "IOS-001-x.md", docWith("IOS-001", `"Sets the "x" flag. Use when you change it."`, "2026-11-09", "2026-11-20"), "double quote inside"},
		{"metadata missing", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "metadata:\n  created: \"2026-11-09\"\n  updated: \"2026-11-20\"\n", "", 1), "exactly the keys name, description, metadata"},
		{"top-level created (old format)", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "metadata:\n  created: \"2026-11-09\"\n  updated: \"2026-11-20\"\n", "created: 2026-11-09\nupdated: 2026-11-20\n", 1), "exactly the keys name, description, metadata"},
		{"key order", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "description: "+goodDesc+"\nmetadata:", "metadata:", 1), "exactly the keys name, description, metadata"},
		{"metadata key order", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "  created: \"2026-11-09\"\n  updated: \"2026-11-20\"", "  updated: \"2026-11-20\"\n  created: \"2026-11-09\"", 1), "indented keys created, updated in this order"},
		{"extra metadata key", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "  updated: \"2026-11-20\"\n", "  updated: \"2026-11-20\"\n  tags: x\n", 1), "no others"},
		{"extra key", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "\n---\n\n#", "\ntags: x\n---\n\n#", 1), "no others"},
		{"unquoted date", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "created: \"2026-11-09\"", "created: 2026-11-09", 1), "metadata.created: \"YYYY-MM-DD\""},
		{"description 1024 runes", "IOS-001-x.md", docWith("IOS-001", "\""+strings.Repeat("é", 1024-9)+" Use when\"", "2026-11-09", "2026-11-20"), ""},
		{"description over 1024 runes", "IOS-001-x.md", docWith("IOS-001", "\""+strings.Repeat("é", 1025-9)+" Use when\"", "2026-11-09", "2026-11-20"), "description has 1025 characters"},
		{"name over 64", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "name: IOS-001", "name: IOS-001"+strings.Repeat("x", 60), 1), "line 2 must be \"name: IOS-001\""},
		{"name mismatch", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "name: IOS-001", "name: IOS-002", 1), "line 2 must be \"name: IOS-001\""},
		{"missing status", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "Status: draft", "status: draft", 1), "line 11 must be \"Status: <text>\""},
		{"empty status", "IOS-001-x.md", strings.Replace(doc("IOS-001", "2026-11-09", "2026-11-20"), "Status: draft", "Status: ", 1), "line 11 must be \"Status: <text>\""},
		{"short file", "IOS-001-x.md", "---\nname: IOS-001\ndescription: \"Use when x.\"\nmetadata:\n  created: \"2026-11-09\"\n  updated: \"2026-11-20\"\n---\n\n# IOS-001: T\n", "line 11 must be \"Status: <text>\""},
		{"bad date", "IOS-001-x.md", doc("IOS-001", "2026-02-30", "2026-11-20"), "not a real date"},
		{"created after updated", "IOS-001-x.md", doc("IOS-001", "2026-11-20", "2026-11-19"), "metadata.created 2026-11-20 is after metadata.updated 2026-11-19"},
		{"future", "IOS-001-x.md", doc("IOS-001", "2026-11-09", "2026-11-22"), "set metadata.updated: \"2026-11-20\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := strings.Join(validateDoc(tt.file, tt.content, cats, testToday), "\n")
			if tt.want == "" && errs != "" || !strings.Contains(errs, tt.want) {
				t.Errorf("errors %q, want %q", errs, tt.want)
			}
			if errs != "" && !strings.Contains(errs, "docs/"+tt.file+":") {
				t.Errorf("error does not name the file: %q", errs)
			}
		})
	}
}

func writeDoc(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, "docs", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const testCategories = "# Design docs\n\n## Categories\n\n| Code | Name |\n|---|---|\n| `IOS` | iOS |\n"

func TestCheckDesignDocsFiles(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "DOC-001-design-docs.md", testCategories)
	writeDoc(t, root, "IOS-001-a.md", doc("IOS-001", "2026-11-01", "2026-11-02"))
	writeDoc(t, root, "IOS-001-b.md", doc("IOS-001", "2026-11-01", "2026-11-02"))
	writeDoc(t, root, "notes.md", "free form\n")
	err := checkDesignDocs(root, testToday)
	if err == nil {
		t.Fatal("want errors")
	}
	msg := err.Error()
	for _, want := range []string{"IOS-001 is used by 2 files", "docs/DOC-001-design-docs.md: line 1 must be \"---\""} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "notes.md") {
		t.Errorf("free-form doc was checked:\n%s", msg)
	}
}

// gitRepo makes a repo with docs/ and origin/main at the first commit.
type gitRepo struct {
	t    *testing.T
	root string
}

func (r gitRepo) git(date string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date+"T12:00:00Z", "GIT_COMMITTER_DATE="+date+"T12:00:00Z",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r gitRepo) commit(date, msg string) {
	r.git(date, "add", "-A")
	r.git(date, "commit", "-q", "-m", msg)
}

func newGitRepo(t *testing.T) gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	r := gitRepo{t, t.TempDir()}
	r.git("2026-01-01", "init", "-q", "-b", "main")
	writeDoc(t, r.root, "DOC-001-design-docs.md", testCategories+"\nbody\n")
	writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-01-01", "2026-01-05"))
	// DOC-001 has no header of its own here; the check reports that, so
	// give it a real one.
	writeDoc(t, r.root, "DOC-001-design-docs.md", docWith("DOC-001", goodDesc, "2026-01-01", "2026-01-01")+"\n## Categories\n\n| Code | Name |\n|---|---|\n| `DOC` | Docs |\n| `IOS` | iOS |\n")
	r.commit("2026-01-05", "base")
	r.git("2026-01-05", "update-ref", "refs/remotes/origin/main", "HEAD")
	r.git("2026-01-05", "checkout", "-q", "-b", "topic")
	return r
}

func TestCheckDesignDocsChanges(t *testing.T) {
	today := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)
	run := func(t *testing.T, r gitRepo) string {
		t.Helper()
		if err := checkDesignDocs(r.root, today); err != nil {
			return err.Error()
		}
		return ""
	}

	t.Run("untouched", func(t *testing.T) {
		if got := run(t, newGitRepo(t)); got != "" {
			t.Errorf("unexpected: %s", got)
		}
	})
	t.Run("updated not bumped", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-01-01", "2026-01-05")+"more\n")
		r.commit("2026-03-10", "edit")
		got := run(t, r)
		if !strings.Contains(got, "docs/IOS-001-a.md: metadata.updated is 2026-01-05, but this branch changes the file on 2026-03-10; set metadata.updated: \"2026-03-10\"") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("one day behind is allowed", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-01-01", "2026-03-09")+"more\n")
		r.commit("2026-03-10", "edit")
		if got := run(t, r); got != "" {
			t.Errorf("unexpected: %s", got)
		}
	})
	t.Run("bumped", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-01-01", "2026-03-10")+"more\n")
		r.commit("2026-03-10", "edit")
		if got := run(t, r); got != "" {
			t.Errorf("unexpected: %s", got)
		}
	})
	t.Run("updated goes back", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-01-01", "2026-01-02")+"more\n")
		r.commit("2026-01-02", "edit")
		got := run(t, r)
		if !strings.Contains(got, "the base version says 2026-01-05") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("created changed", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-02-01", "2026-03-10"))
		r.commit("2026-03-10", "edit")
		got := run(t, r)
		if !strings.Contains(got, "do not change it, set metadata.created: \"2026-01-01\"") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("uncommitted edit needs today", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-01-01", "2026-01-05")+"more\n")
		got := run(t, r)
		if !strings.Contains(got, "uncommitted changes") || !strings.Contains(got, "set metadata.updated: \"2026-03-11\"") {
			t.Errorf("got %q", got)
		}
		writeDoc(t, r.root, "IOS-001-a.md", doc("IOS-001", "2026-01-01", "2026-03-11")+"more\n")
		if got := run(t, r); got != "" {
			t.Errorf("unexpected: %s", got)
		}
	})
	t.Run("rename keeps created", func(t *testing.T) {
		r := newGitRepo(t)
		r.git("2026-03-10", "mv", "docs/IOS-001-a.md", "docs/IOS-002-a.md")
		writeDoc(t, r.root, "IOS-002-a.md", doc("IOS-002", "2026-01-01", "2026-03-10"))
		r.commit("2026-03-10", "rename")
		if got := run(t, r); got != "" {
			t.Errorf("unexpected: %s", got)
		}
	})
	t.Run("rename changes created", func(t *testing.T) {
		r := newGitRepo(t)
		r.git("2026-03-10", "mv", "docs/IOS-001-a.md", "docs/IOS-002-a.md")
		writeDoc(t, r.root, "IOS-002-a.md", doc("IOS-002", "2026-03-10", "2026-03-10"))
		r.commit("2026-03-10", "rename")
		got := run(t, r)
		if !strings.Contains(got, "docs/IOS-002-a.md: metadata.created is 2026-03-10, but the base version says 2026-01-01") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("base without header skips date comparison", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-003-old.md", "# IOS-003: Old\n\nStatus: x\n\nOld body.\n")
		r.commit("2026-01-05", "old")
		r.git("2026-01-05", "update-ref", "refs/remotes/origin/main", "HEAD")
		r.git("2026-03-10", "mv", "docs/IOS-003-old.md", "docs/IOS-004-old.md")
		writeDoc(t, r.root, "IOS-004-old.md", doc("IOS-004", "2025-12-01", "2026-03-10"))
		r.commit("2026-03-10", "rename")
		if got := run(t, r); got != "" {
			t.Errorf("unexpected: %s", got)
		}
	})
	t.Run("base with top-level dates skips date comparison", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-003-old.md", "---\nname: IOS-003\ndescription: \"Use when x.\"\ncreated: 2026-01-01\nupdated: 2026-01-05\n---\n\n# IOS-003: Old\n\nStatus: x\n")
		r.commit("2026-01-05", "old")
		r.git("2026-01-05", "update-ref", "refs/remotes/origin/main", "HEAD")
		r.git("2026-03-10", "mv", "docs/IOS-003-old.md", "docs/IOS-004-old.md")
		writeDoc(t, r.root, "IOS-004-old.md", doc("IOS-004", "2025-12-01", "2026-03-10"))
		r.commit("2026-03-10", "rename")
		if got := run(t, r); got != "" {
			t.Errorf("unexpected: %s", got)
		}
	})
	t.Run("new file", func(t *testing.T) {
		r := newGitRepo(t)
		writeDoc(t, r.root, "IOS-002-b.md", doc("IOS-002", "2026-03-01", "2026-03-01"))
		r.commit("2026-03-10", "add")
		got := run(t, r)
		if !strings.Contains(got, "docs/IOS-002-b.md: metadata.updated is 2026-03-01, but this branch changes the file on 2026-03-10") {
			t.Errorf("got %q", got)
		}
	})
}

func TestBodyLines(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{
		{"---\nname: X\n---\n\n# X\n", 2},
		{"---\r\nname: X\r\n---\r\na\r\nb\r\nc\r\n", 3},
		{"# no front matter\nb\n", 2},
	} {
		if got := bodyLines(tt.in); got != tt.want {
			t.Errorf("bodyLines(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestLongDocsWarnsOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	long := "---\nname: REF-001\n---\n" + strings.Repeat("line\n", maxBodyLines+1)
	short := "---\nname: REF-002\n---\n" + strings.Repeat("line\n", maxBodyLines)
	for n, s := range map[string]string{"REF-001-long.md": long, "REF-002-short.md": short} {
		if err := os.WriteFile(filepath.Join(root, "docs", n), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws := longDocs(root)
	if len(ws) != 1 || !strings.Contains(ws[0], "docs/REF-001-long.md") || !strings.Contains(ws[0], "501 lines") {
		t.Errorf("longDocs = %q, want one warning for REF-001-long.md with 501 lines", ws)
	}
}
