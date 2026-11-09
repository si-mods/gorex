package rexapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestWebBrowserPreview is an opt-in, disposable loopback fixture for browser
// QA. Its HTTP origin exercises localhost's secure-cookie exception without
// adding a trusted certificate or changing the user's real server/settings.
func TestWebBrowserPreview(t *testing.T) {
	root := os.Getenv("GOREX_WEB_PREVIEW")
	if root == "" {
		t.Skip("set GOREX_WEB_PREVIEW to a temporary output directory")
	}
	f := startFixture(t)
	f.g.hello.Host.Name = "studio"
	p, err := newPublicAccess(f.g, filepath.Join(rex.Dir(), "browser-preview"))
	if err != nil {
		t.Fatal(err)
	}
	f.g.public = p
	makeSession := func(message string) string {
		info, err := f.desk.Create(rex.CreateOptions{Dir: root, Command: []string{"/bin/sh", "-c", "printf '" + message + "\\n'; export PS1='demo $ '; exec /bin/sh -i"}})
		if err != nil {
			t.Fatal(err)
		}
		return info.ID
	}
	shell := makeSession("\\033[1;35mGoRex Web\\033[0m · browser terminal ready\\n\\nA terminal that stays with you.\\n中文输入与历史回看测试\\n")
	build := makeSession("\\033[32m✓ Build passed\\033[0m · 12 checks\\n")
	lab := makeSession("Sandbox · a separate space\\n")
	f.setLayout(layout.Store{Version: layout.Version, Sessions: []layout.Workspace{
		{ID: "web-work", Name: "Workbench", Layout: layout.Layout{Tabs: []layout.Tab{{ID: "web-shell", Name: "Shell", Root: pane(shell)}, {ID: "web-build", Name: "Build", Root: pane(build)}}}},
		{ID: "web-lab", Name: "Sandbox", Layout: layout.Layout{Tabs: []layout.Tab{{ID: "web-lab-shell", Name: "Console", Root: pane(lab)}}}},
	}})
	srv := httptest.NewUnstartedServer(f.g.HandlerFor(transportPublic))
	srv.Listener = newTrackedListener(srv.Listener)
	srv.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context { return context.WithValue(ctx, connKey{}, c) }
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	base := "http://localhost:" + port
	p.config = PublicConfig{Enabled: true, URL: base}
	srv.Start()
	t.Cleanup(srv.Close)
	code, _, err := p.pairCode()
	if err != nil {
		t.Fatal(err)
	}
	ready, _ := json.Marshal(map[string]string{"url": base + "/#gorex-pair=" + code, "base": base, "dir": rex.Dir(), "socket": rex.SocketPath(), "shell": shell})
	if err := os.WriteFile(filepath.Join(root, "ready.json"), ready, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("Browser preview ready")
	for {
		if _, err := os.Stat(filepath.Join(root, "stop")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
