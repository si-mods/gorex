package rex

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (st *programStatus) summary() *ProgramStatus { return MostUrgent(st.list(), time.Time{}) }

func osc(body string) string { return "\x1b]7501;" + body + "\x1b\\" }

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestStatusReports(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     *ProgramStatus // nil: no record
	}{
		{"working", osc("state=working:progress=40:app=claude"), &ProgramStatus{State: "working", Progress: 40, App: "claude"}},
		{"BEL", "\x1b]7501;state=done\x07", &ProgramStatus{State: "done", Progress: -1}},
		{"in text", "ab" + osc("state=idle") + "cd", &ProgramStatus{State: "idle", Progress: -1}},
		{"blocked", osc(" state = blocked :kind=question:msg=" + b64("Allow rm?") + ":title=" + strings.TrimRight(b64("Fix"), "=")),
			&ProgramStatus{State: "blocked", Kind: "question", Progress: -1, Title: "Fix", Msg: "Allow rm?"}},
		{"unknown kind", osc("state=blocked:kind=nope"), &ProgramStatus{State: "blocked", Progress: -1}},
		{"kind only blocked", osc("state=working:kind=auth"), &ProgramStatus{State: "working", Progress: -1}},
		{"progress range", osc("state=working:progress=101"), &ProgramStatus{State: "working", Progress: -1}},
		{"progress only working", osc("state=done:progress=5"), &ProgramStatus{State: "done", Progress: -1}},
		{"last wins", osc("state=idle:state=error"), &ProgramStatus{State: "error", Progress: -1}},
		{"malformed pair", osc("state=done:bad:X=1:foo=a b"), &ProgramStatus{State: "done", Progress: -1}},
		{"bidi dropped", osc("state=done:msg=" + b64("a‮b")), &ProgramStatus{State: "done", Progress: -1, Msg: "ab"}},
		{"unknown state", osc("state=sleeping"), nil},
		{"no state", osc("app=x"), nil},
		{"bad base64", osc("state=done:msg=a"), nil},
		{"bad character", osc("state=done:msg=!!!"), &ProgramStatus{State: "done", Progress: -1}}, // a malformed pair
		{"control char", osc("state=done:msg=" + b64("a\nb")), nil},
		{"msg too long", osc("state=done:msg=" + b64(strings.Repeat("x", 2049))), nil},
		{"title too long", osc("state=done:title=" + b64(strings.Repeat("x", 193))), nil},
		{"app too long", osc("state=done:app=" + strings.Repeat("a", 33)), nil},
		{"key too long", osc("state=done:" + strings.Repeat("k", 17) + "=1"), nil},
		{"id too deep", osc("state=done:id=a/b/c/d/e/f/g/h/i"), nil},
		{"id empty segment", osc("state=done:id=a//b"), nil},
		{"sequence too long", osc("state=done:pad=" + strings.Repeat("a", 4096)), nil},
		{"other OSC", "\x1b]7502;state=done\x07", nil},
		{"cut by ESC", "\x1b]7501;state=done\x1b[m", nil},
	} {
		var st programStatus
		st.feed([]byte(c.in))
		got := st.summary()
		if got != nil {
			got.Updated = time.Time{}
		}
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestStatusRecords(t *testing.T) {
	var st programStatus
	st.feed([]byte(osc("state=working:app=claude") + osc("state=blocked:id=a") + osc("state=done:id=a/b") + osc("state=error:id=c")))
	if got := st.summary(); got.State != "blocked" || got.App != "claude" || got.ID != "a" {
		t.Fatalf("summary %+v, want blocked a of claude (inherited)", got)
	}
	list := st.list()
	if len(list) != 4 || list[0].ID != "" || list[2].ID != "a/b" || list[2].App != "claude" {
		t.Fatalf("list %+v", list)
	}
	// Done and error seen no longer count.
	if got := MostUrgent(list[2:], list[3].Updated); got != nil {
		t.Fatalf("seen: %+v", got)
	}
	if got := MostUrgent(list[2:], list[3].Updated.Add(-time.Nanosecond)); got == nil || got.ID != "c" {
		t.Fatalf("seen but c: %+v", got)
	}
	st.feed([]byte(osc("state=clear:id=a")))
	if len(st.records) != 2 || st.summary().State != "error" {
		t.Fatalf("clear a: %d records, summary %+v", len(st.records), st.summary())
	}
	st.feed([]byte(osc("state=clear")))
	if len(st.records) != 0 {
		t.Fatalf("clear: %d records left", len(st.records))
	}

	// A prompt (OSC 133;A) and the end of the program drop what was
	// running; done and error stay. RIS drops all.
	all := osc("state=working:id=w") + osc("state=blocked:id=b") + osc("state=idle:id=i") + osc("state=done:id=d") + osc("state=error:id=e")
	st.feed([]byte(all + "\x1b]133;A\x07"))
	if len(st.records) != 2 || st.records["d"] == nil || st.records["e"] == nil {
		t.Fatalf("after prompt: %v", st.records)
	}
	st.feed([]byte("\x1b]133;B\x07" + osc("state=working")))
	if len(st.records) != 3 {
		t.Fatalf("133;B dropped records: %v", st.records)
	}
	st.feed([]byte("\x1bc"))
	if len(st.records) != 0 {
		t.Fatalf("after RIS: %v", st.records)
	}

	// Past the cap, the least recently updated goes.
	for i := range maxStatusRecords + 1 {
		st.feed([]byte(osc("state=idle:id=r" + strconv.Itoa(i))))
	}
	if len(st.records) != maxStatusRecords || st.records["r0"] != nil || st.records["r1"] == nil {
		t.Fatalf("cap: %d records, r0 kept: %v", len(st.records), st.records["r0"] != nil)
	}
}

func TestStatusSplitAndQuery(t *testing.T) {
	in := "x" + osc("?") + "\x1b[c" + "y" + osc("state=done:msg="+b64("ok")) + "\x1b]133;A\x07" + osc("state=working:id=w") + "\x1b]7501;?\x07"
	// Cut at every byte, the reads find what one read does.
	for cut := range len(in) {
		var st programStatus
		q := st.feed([]byte(in[:cut])) + st.feed([]byte(in[cut:]))
		if q != 2 {
			t.Fatalf("cut %d: %d queries, want 2", cut, q)
		}
		if s := st.summary(); s == nil || s.State != "done" || s.Msg != "ok" || len(st.records) != 2 {
			t.Fatalf("cut %d: summary %+v, %d records", cut, s, len(st.records))
		}
	}
	// A sequence never finished is given up past the limit.
	var st programStatus
	st.feed([]byte("\x1b]7501;state=done:pad="))
	for range 10 {
		st.feed([]byte(strings.Repeat("a", 1000)))
	}
	if len(st.carry) != 0 {
		t.Fatalf("carry %d bytes", len(st.carry))
	}
	st.feed([]byte("\x07" + osc("?")))
	if st.summary() != nil {
		t.Fatal("the tail of a sequence given up made a record")
	}
	// Other strings are not kept across reads.
	st.feed([]byte("\x1b]52;c;" + strings.Repeat("A", 100)))
	if len(st.carry) != 0 {
		t.Fatalf("carried an OSC 52")
	}
}

func TestStatusRISClears(t *testing.T) {
	var st programStatus
	st.report("state=working")
	before := st.updates
	st.feed([]byte("\x1bc"))
	if st.updates != before+1 || len(st.records) != 0 {
		t.Fatalf("updates %d -> %d, records %d", before, st.updates, len(st.records))
	}
}
