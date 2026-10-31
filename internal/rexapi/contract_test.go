package rexapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaFor returns the validator of a method's documented result.
func schemaFor(t *testing.T, method string) *jsonschema.Schema {
	t.Helper()
	var doc struct {
		Methods []struct {
			Name   string          `json:"name"`
			Output json.RawMessage `json:"output_schema"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(helpJSON, &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range doc.Methods {
		if m.Name != method {
			continue
		}
		if len(m.Output) == 0 || string(m.Output) == "null" {
			return nil
		}
		var v any
		if err := json.Unmarshal(m.Output, &v); err != nil {
			t.Fatal(err)
		}
		// session.bootstrap inlines the view schema twice, with the same
		// anchor. The two are identical, so the second anchor is dropped
		// and its refs resolve to the first.
		dedupeAnchors(v, map[string]bool{})
		c := jsonschema.NewCompiler()
		url := "mem://" + method + ".json"
		if err := c.AddResource(url, v); err != nil {
			t.Fatal(err)
		}
		sch, err := c.Compile(url)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return sch
	}
	t.Fatalf("%s is not in the help document", method)
	return nil
}

// dedupeAnchors drops repeated anchors from a schema, keeping the first.
func dedupeAnchors(v any, seen map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if name, ok := x["$anchor"].(string); ok {
			if seen[name] {
				delete(x, "$anchor")
			} else {
				seen[name] = true
			}
		}
		for _, child := range x {
			dedupeAnchors(child, seen)
		}
	case []any:
		for _, child := range x {
			dedupeAnchors(child, seen)
		}
	}
}

// checkResult validates a response's payload against the method's
// documented result.
func checkResult(t *testing.T, method string, res map[string]any) {
	t.Helper()
	if sch := schemaFor(t, method); sch != nil {
		if err := sch.Validate(res); err != nil {
			t.Errorf("%s result does not validate: %v\nresult: %v", method, err, res)
		}
	}
}

// callChecked calls a method, requires success, and validates its result.
func callChecked(t *testing.T, c *testConn, method string, payload any) map[string]any {
	t.Helper()
	res := result(t, c.request("r-"+method, method, payload))
	checkResult(t, method, res)
	return res
}

// TestContract checks every implemented method's result against the
// schema of the help document, and that bad arguments get the errors the
// doc names.
func TestContract(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	c := f.dialControl()
	w := c.hello(apiRevision)
	if w["protocol"] != controlProtocol || w["api_revision"] != float64(apiRevision) ||
		w["api_server_revision"] != float64(apiRevision) {
		t.Errorf("welcome: %v", w)
	}
	if limits := w["limits"].(map[string]any); limits["max_message_bytes"] != float64(maxMessage) {
		t.Errorf("limits: %v", limits)
	}
	clientID := welcomeID(t, w)

	ver := callChecked(t, c, "server.version", nil)
	if ver["rex_version"] != "0.0.0-gorex" || ver["platform"] != platform() || ver["dirty"] != false ||
		ver["api_revision"] != float64(apiRevision) {
		t.Errorf("version: %v", ver)
	}
	status := callChecked(t, c, "server.status", nil)
	if status["session_count"] != float64(2) || status["host_id"] == nil {
		t.Errorf("status: %v", status)
	}
	who := callChecked(t, c, "server.whoami", nil)
	if p := who["principal"].(map[string]any); p["kind"] != "anonymous" || p["transport"] != "tcp" {
		t.Errorf("whoami: %v", who)
	}
	help := callChecked(t, c, "help.all", nil)
	if _, ok := help["http"]; ok {
		t.Error("help.all has the http field")
	}
	if _, ok := help["methods"]; !ok {
		t.Error("help.all has no methods")
	}

	ids := sessionIDs(t, c)
	work, side := ids["work"], ids["side"]
	if work == "" || side == "" {
		t.Fatalf("sessions: %v", ids)
	}
	attach := callChecked(t, c, "session.attach", map[string]any{"session_id": work})
	if attach["client_id"] != clientID {
		t.Errorf("attach client %v, want %v", attach["client_id"], clientID)
	}
	view := attach["view"].(map[string]any)
	block := view["windows"].([]any)[0].(map[string]any)["layers"].([]any)[0].(map[string]any)["blocks"].([]any)[0].(map[string]any)["block_id"].(string)

	callChecked(t, c, "session.view", map[string]any{"session_id": work})
	callChecked(t, c, "session.attach_all", map[string]any{"event_scope": "session"})
	callChecked(t, c, "session.detach", map[string]any{"session_id": work})
	callChecked(t, c, "session.attach", map[string]any{"session_id": work})
	callChecked(t, c, "data.connect", map[string]any{
		"session_id": work, "block_id": block, "data_ref_id": "terminal",
		"subprotocol": terminalProtocol,
	})
	callChecked(t, c, "client.list", nil)
	callChecked(t, c, "client.list", map[string]any{"session_id": work, "kinds": []string{"app"}})
	callChecked(t, c, "client.inspect", map[string]any{"client_id": clientID})
	callChecked(t, c, "target.resolve", map[string]any{
		"kinds": []string{"block"}, "queries": []string{block},
		"context": map[string]any{"session_id": work},
	})
	callChecked(t, c, "target.complete", map[string]any{
		"kinds": []string{"window", "block"}, "prefix": "",
		"context": map[string]any{"session_id": work},
	})

	// A second connection bootstraps.
	c2 := f.dialControl()
	c2.hello(apiRevision)
	boot := callChecked(t, c2, "session.bootstrap", map[string]any{
		"event_scope": "all", "preferred_session_id": work,
	})
	if boot["selected_session"] == nil {
		t.Error("bootstrap chose no session")
	}
	callChecked(t, c2, "session.bootstrap", map[string]any{
		"event_scope": "session", "attach_session_ids": []string{work, "session:nope", work},
	})

	// Bad arguments and the documented errors.
	cases := []struct {
		method  string
		payload any
		code    string
		status  float64
	}{
		{"session.attach", map[string]any{}, "bad_request", 400},
		{"session.attach", map[string]any{"session_id": "session:nope"}, "session_not_found", 404},
		{"session.attach_all", map[string]any{}, "bad_request", 400},
		{"session.attach_all", map[string]any{"event_scope": "some"}, "bad_request", 400},
		{"session.bootstrap", map[string]any{"preferred_session_id": work}, "bad_request", 400},
		{"client.inspect", map[string]any{}, "bad_request", 400},
		{"client.inspect", map[string]any{"client_id": "nope"}, "bad_request", 400},
		{"client.inspect", map[string]any{"client_id": "client:nope"}, "bad_request", 400},
		{"client.inspect", map[string]any{"client_id": "client:01a191c5-96af-7b1f-b589-a7e9a714fc66"}, "not_found", 404},
		{"data.connect", map[string]any{"session_id": work, "block_id": block}, "bad_request", 400},
		{"data.connect", map[string]any{"session_id": work, "block_id": block, "data_ref_id": ""}, "bad_request", 400},
		{"target.resolve", map[string]any{"kinds": []string{}, "queries": []string{"x"}}, "bad_request", 400},
		{"target.resolve", map[string]any{"kinds": []string{"block"}, "queries": []string{}}, "bad_request", 400},
		{"target.resolve", map[string]any{"kinds": []string{"client", "block"}, "queries": []string{"x"}}, "bad_request", 400},
		{"target.resolve", map[string]any{"kinds": []string{"session"}, "queries": []string{"x"}, "scope": map[string]any{}}, "bad_request", 400},
		{"target.complete", map[string]any{"kinds": []string{"block"}}, "bad_request", 400},
		{"target.complete", map[string]any{"kinds": []string{"window"}, "prefix": "a", "scope": map[string]any{"window": "1"}}, "bad_request", 400},
		{"session.create", map[string]any{"label": "x"}, "not_supported", 400},
		{"com.superlogical.terminal.write", map[string]any{}, "not_supported", 400},
		{"no.such.method", map[string]any{}, "method_not_found", 404},
	}
	for i, tc := range cases {
		res := c.request("bad"+string(rune('a'+i)), tc.method, tc.payload)
		e := failure(t, res)
		if e["code"] != tc.code || e["status"] != tc.status {
			t.Errorf("%s %v: %v, want %s %v", tc.method, tc.payload, e, tc.code, tc.status)
		}
	}

	// A connection that did not attach gets conflict for a session view.
	c3 := f.dialControl()
	c3.hello(apiRevision)
	e := failure(t, c3.request("v", "session.view", map[string]any{"session_id": side}))
	if e["code"] != "conflict" {
		t.Errorf("unattached view: %v", e)
	}
}

// TestContractTargets checks target.resolve's documented steps.
func TestContractTargets(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	ids := sessionIDs(t, c)
	work := ids["work"]
	callChecked(t, c, "session.attach", map[string]any{"session_id": work})

	res := callChecked(t, c, "target.resolve", map[string]any{
		"kinds": []string{"session"}, "queries": []string{"work", "WORK", "nope"},
	})
	results := res["results"].([]any)
	if results[0].(map[string]any)["status"] != "resolved" || results[1].(map[string]any)["status"] != "resolved" ||
		results[2].(map[string]any)["status"] != "not_found" {
		t.Errorf("session queries: %v", results)
	}
	res = callChecked(t, c, "target.resolve", map[string]any{
		"kinds": []string{"window"}, "queries": []string{"1", "editor", "2", "3"},
		"context": map[string]any{"session_id": work},
	})
	results = res["results"].([]any)
	statuses := make([]string, len(results))
	for i, r := range results {
		statuses[i] = r.(map[string]any)["status"].(string)
	}
	if statuses[0] != "resolved" || statuses[1] != "resolved" || statuses[2] != "resolved" || statuses[3] != "not_found" {
		t.Errorf("window queries: %v", statuses)
	}
	// A block by label is ambiguous when the label repeats.
	res = callChecked(t, c, "target.resolve", map[string]any{
		"kinds": []string{"block"}, "queries": []string{"shell"},
		"context": map[string]any{"session_id": work},
	})
	r := res["results"].([]any)[0].(map[string]any)
	if r["status"] != "ambiguous" || len(r["candidates"].([]any)) != 3 {
		t.Errorf("ambiguous block: %v", r)
	}
	// An empty query asks for the default block of the context.
	res = callChecked(t, c, "target.resolve", map[string]any{
		"kinds": []string{"block"}, "queries": []string{""},
		"context": map[string]any{"session_id": work, "block_id": blockOf(t, c, work)},
	})
	if got := res["results"].([]any)[0].(map[string]any); got["status"] != "resolved" {
		t.Errorf("default block: %v", got)
	}
	// A scope that matches nothing leaves results not_found.
	res = callChecked(t, c, "target.resolve", map[string]any{
		"kinds": []string{"block"}, "queries": []string{"shell"},
		"scope": map[string]any{"session": "nope"},
	})
	if got := res["results"].([]any)[0].(map[string]any); got["status"] != "not_found" || got["reason"] == "" {
		t.Errorf("failed scope: %v", got)
	}
}

// blockOf returns the first block ID of a session.
func blockOf(t *testing.T, c *testConn, session string) string {
	t.Helper()
	res := result(t, c.request("bv", "session.view", map[string]any{"session_id": session}))
	return res["windows"].([]any)[0].(map[string]any)["layers"].([]any)[0].(map[string]any)["blocks"].([]any)[0].(map[string]any)["block_id"].(string)
}

// TestHelpDocument checks the endpoints' documents.
func TestHelpDocument(t *testing.T) {
	f := startFixture(t)
	for _, path := range []string{"/api/help", "/llms.txt", "/llms.html"} {
		resp, err := httpGet(f.srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.ContentLength == 0 {
			t.Errorf("%s: %v", path, resp.Status)
		}
	}
	resp, err := httpGet(f.srv.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("unknown path: %v", resp.Status)
	}
}

// httpGet is a small GET.
func httpGet(url string) (*http.Response, error) {
	return http.Get(url)
}

// TestSchemaCompiles checks that every implemented method has a result
// schema that compiles.
func TestSchemaCompiles(t *testing.T) {
	for _, m := range []string{
		"server.version", "server.status", "server.whoami", "session.list", "session.attach",
		"session.attach_all", "session.detach", "session.bootstrap", "session.view",
		"target.resolve", "target.complete", "client.list", "client.inspect", "data.connect",
	} {
		if schemaFor(t, m) == nil {
			t.Errorf("%s has no result schema", m)
		}
	}
}
