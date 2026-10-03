package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func call(id, name, args string) model.ToolCall {
	return model.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}
}

// step is an assistant message making one call and its result.
func step(c model.ToolCall, result string) []model.Message {
	return []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{c}},
		{Role: model.RoleTool, ToolCallID: c.ID, Content: result},
	}
}

func history(steps ...[]model.Message) []model.Message {
	h := []model.Message{{Role: model.RoleUser, Content: "go"}}
	for _, s := range steps {
		h = append(h, s...)
	}
	return append(h, model.Message{Role: model.RoleAssistant, Content: "done"})
}

var bigOutput = strings.Repeat("some line of output\n", 100)

func TestSuperseded(t *testing.T) {
	h := history(
		step(call("r1", "read", `{"path":"a.go"}`), "x"),
		step(call("r2", "read", `{"path":"b.go","offset":10,"limit":5}`), "x"),
		step(call("r3", "read", `{"path":"c.go","offset":10,"limit":5}`), "x"),
		step(call("b1", "bash", `{"command":"make"}`), "x"),
		step(call("t1", "todo", `{"todos":[]}`), "x"),
		step(call("w1", "edit", `{"path":"./a.go","old_string":"a","new_string":"b"}`), "x"),
		[]model.Message{calls(call("w2", "edit", `{"path":"c.go","old_string":"zzz","new_string":"b"}`)), result("w2", "old_string not found", true)},
		step(call("r4", "read", `{"path":"b.go"}`), "x"),
		step(call("r5", "read", `{"path":"c.go","offset":12,"limit":50}`), "x"),
		step(call("b2", "bash", `{ "command": "make" }`), "x"),
		step(call("t2", "todo", `{"todos":[{"content":"x","status":"pending"}]}`), "x"),
	)
	got := superseded(h)
	for id, want := range map[string]bool{
		"r1": true,  // a.go edited later
		"r2": true,  // covered by the later full read
		"r3": false, // the later read starts after it, and the edit failed
		"b1": true,  // the same command ran again
		"t1": true,  // a later todo list
		"w1": false, "r4": false, "r5": false, "b2": false, "t2": false,
	} {
		if got[id] != want {
			t.Errorf("superseded[%s] = %v, want %v", id, got[id], want)
		}
	}
}

func TestMaskOrder(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // masked output is saved there
	body, _ := json.Marshal(strings.Repeat("func f() {}\n", 100))
	l := &Loop{History: history(
		step(call("b1", "bash", `{"command":"make"}`), bigOutput),
		step(call("r1", "read", `{"path":"a.go"}`), bigOutput),
		step(call("w1", "write", `{"path":"a.go","content":`+string(body)+`}`), "ok"),
	)}
	maskOne := func() {
		t.Helper()
		if !l.mask(l.projectedHistory()-10, false, 0) {
			t.Fatal("nothing masked")
		}
	}

	maskOne()
	if l.History[4].Elided == "" || l.History[2].Elided != "" {
		t.Fatalf("first batch: read masked %v, bash masked %v; want the superseded read first", l.History[4].Elided != "", l.History[2].Elided != "")
	}
	if !strings.Contains(l.History[4].Elided, "a later call superseded it") {
		t.Errorf("read stand-in = %q", l.History[4].Elided)
	}
	maskOne()
	w := l.History[5].ToolCalls[0]
	if w.Elided == nil || l.History[2].Elided != "" {
		t.Fatalf("second batch: write body masked %v, bash masked %v; want the file body next", w.Elided != nil, l.History[2].Elided != "")
	}
	if string(w.Args) == string(w.Elided) || !strings.Contains(string(sent(l.History[5]).ToolCalls[0].Args), "<masked: 100 lines") {
		t.Errorf("sent write args = %s", sent(l.History[5]).ToolCalls[0].Args)
	}
	maskOne()
	if l.History[2].Elided == "" {
		t.Error("third batch: bash not masked")
	}
}

func TestMaskProtectsRecentSteps(t *testing.T) {
	var steps [][]model.Message
	for i := range 12 {
		steps = append(steps, step(call(fmt.Sprint("r", i), "read", fmt.Sprintf(`{"path":"f%d.go"}`, i)), bigOutput))
	}
	masked := func(window int) int {
		l := &Loop{ContextWindow: window, History: history(steps...)}
		l.mask(0, true, 0)
		n := 0
		for _, m := range l.History {
			if m.Elided != "" {
				n++
			}
		}
		return n
	}
	// The final answer and the last 9 reads are the last 10 steps.
	if n := masked(1 << 20); n != 3 {
		t.Errorf("large window: %d results masked, want 3", n)
	}
	if n := masked(16384); n <= 3 {
		t.Errorf("small window: %d results masked, want more than 3: recent steps may use only %d%% of the budget", n, protectedPct)
	}

	// Outputs so big the share covers less than one step: the last
	// minProtectedSteps steps, the answer and two reads, keep theirs anyway.
	var big [][]model.Message
	for i := range 6 {
		big = append(big, step(call(fmt.Sprint("r", i), "read", fmt.Sprintf(`{"path":"f%d.go"}`, i)), strings.Repeat(bigOutput, 4)))
	}
	l := &Loop{ContextWindow: 8192, History: history(big...)}
	l.mask(0, true, 0)
	for _, m := range l.History {
		if m.Role == model.RoleTool && (m.Elided == "") != (m.ToolCallID == "r4" || m.ToolCallID == "r5") {
			t.Errorf("big outputs: %s masked = %v, want r0-r3 masked and r4, r5 kept", m.ToolCallID, m.Elided != "")
		}
	}
}

func TestMaskSkipsSmallBatch(t *testing.T) {
	l := &Loop{History: history(step(call("r1", "read", `{"path":"a.go"}`), bigOutput))}
	if l.mask(0, false, 1_000_000) || l.History[2].Elided != "" {
		t.Error("masked a batch freeing less than minFree")
	}
	if !l.mask(0, false, 0) {
		t.Error("nothing masked without a minimum")
	}
}

func TestMaskArgs(t *testing.T) {
	long := strings.Repeat("x", minArgBytes)
	args, ok := maskArgs(json.RawMessage(fmt.Sprintf(`{"path":"a.go","edits":[{"old_string":%q,"new_string":"short"},{"old_string":"s","new_string":%q}]}`, long, long+strings.Repeat("\ny", 200))))
	if !ok {
		t.Fatal("multi_edit arguments not masked")
	}
	var got struct {
		Path  string
		Edits []struct{ Old_String, New_String string }
	}
	if err := json.Unmarshal(args, &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "a.go" || got.Edits[0].New_String != "short" || !strings.HasPrefix(got.Edits[0].Old_String, "<masked: 1 lines") || !strings.HasPrefix(got.Edits[1].New_String, "<masked: 201 lines") {
		t.Errorf("masked = %s", args)
	}
	if _, ok := maskArgs(json.RawMessage(`{"path":"a.go","content":"short"}`)); ok {
		t.Error("small arguments masked")
	}
}

func TestMaskStub(t *testing.T) {
	read := maskStub(call("r", "read", `{"path":"a.go"}`), model.Message{Content: "     5\tfoo\n     6\tbar\n... (file continues; read again with offset=6)"}, "", false)
	if want := "[read a.go lines 5-6: 68 B masked; call read again to see it]"; read != want {
		t.Errorf("read stub = %q, want %q", read, want)
	}
	grep := maskStub(call("g", "grep", `{"pattern":"x"}`), model.Message{Content: "a.go:1:x\nb.go:22:x:y\na.go:3:x"}, "", false)
	if !strings.HasPrefix(grep, `[grep "x": 3 matches in 2 files,`) {
		t.Errorf("grep stub = %q", grep)
	}
	bash := maskStub(call("b", "bash", `{"command":"go test"}`), model.Message{Content: "ok a\n--- FAIL: TestX\nx_test.go:3: boom\nFAIL wisp 0.1s\n[exit code 1]"}, "/tmp/s.txt", false)
	want := "[bash \"go test\": exit 1, 67 B masked; full output in /tmp/s.txt\nfirst error: --- FAIL: TestX\nlast line: FAIL wisp 0.1s]"
	if bash != want {
		t.Errorf("bash stub = %q, want %q", bash, want)
	}
	if spillFrom(bash) != "/tmp/s.txt" {
		t.Errorf("spillFrom = %q", spillFrom(bash))
	}
}

func TestRestoreSpills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone", "s.txt")
	l := &Loop{History: []model.Message{{Role: model.RoleTool, Content: "full", Elided: "[bash: 4 B masked" + spillMarker + path + "]"}}}
	l.restoreSpills()
	if b, err := os.ReadFile(path); err != nil || string(b) != "full" {
		t.Errorf("restored spill = %q, %v", b, err)
	}
}
