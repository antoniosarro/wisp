package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

func result(id, content string, isError bool) model.Message {
	return model.Message{Role: model.RoleTool, ToolCallID: id, Content: content, IsError: isError}
}

func user(s string) model.Message { return model.Message{Role: model.RoleUser, Content: s} }

func calls(cs ...model.ToolCall) model.Message {
	return model.Message{Role: model.RoleAssistant, ToolCalls: cs}
}

// ledgerSession is two turns covering every section.
var ledgerSession = []model.Message{
	user("fix the failing test in loop.go, don't touch the docs"),
	calls(call("r1", "read", `{"path":"loop.go"}`), call("r2", "read", `{"path":"elide.go","offset":10,"limit":5}`)),
	result("r1", "     1\tpackage core\n     2\t", false),
	result("r2", "    11\ta\n    12\tb\n    13\tc", false),
	calls(call("t1", "todo", `{"todos":[{"content":"find the bug","status":"completed"},{"content":"fix it","status":"in_progress"}]}`)),
	result("t1", "ok", false),
	calls(call("b1", "bash", `{"command":"go test ./internal/core"}`)),
	result("b1", "--- FAIL: TestX\n[exit code 1]", true),
	calls(call("b0", "bash", `{"command":"go test ./internal/core"}`)),
	result("b0", "--- FAIL: TestX\n[exit code 1]", true),
	calls(call("e1", "edit", `{"path":"loop.go","old_string":"a","new_string":"b"}`)),
	result("e1", "ok", false),
	calls(call("e2", "edit", `{"path":"docs/x.md","old_string":"zzz","new_string":"b"}`)),
	result("e2", "old_string not found", true),
	model.Message{Role: model.RoleAssistant, Content: "fixed"},

	user("run it again"),
	model.Message{Role: model.RoleUser, Content: ReminderPrefix + "not a user message"},
	calls(call("b2", "bash", `{"command":"go  test ./internal/core"}`), call("b3", "bash", `{"command":"sleep 999"}`)),
	result("b2", "ok", false),
	result("b3", "timed out", true),
	calls(call("r3", "read", `{"path":"elide.go","offset":13,"limit":5}`), call("w1", "write", `{"path":"new.go","content":"x"}`)),
	result("r3", "    14\td\n    15\te", false),
	result("w1", "ok", false),
	model.Message{Role: model.RoleAssistant, Content: "done"},
}

const ledgerRendered = "<files-modified>\n" +
	"loop.go: 1 edit; last check `go test ./internal/core` exit 0\n" +
	"new.go: 1 edit; not checked since\n" +
	"</files-modified>\n" +
	"<files-read>\n" +
	"elide.go lines 11-15\n" +
	"</files-read>\n" +
	"<commands>\n" +
	"exit 0 (3 runs, 2 failed): go test ./internal/core\n" +
	"failed: sleep 999\n" +
	"</commands>\n" +
	"<todo>\n" +
	"[x] find the bug\n" +
	"[>] fix it\n" +
	"</todo>\n" +
	"<user-messages>\n" +
	"1. fix the failing test in loop.go, don't touch the docs\n" +
	"2. run it again\n" +
	"</user-messages>\n"

func TestLedgerRender(t *testing.T) {
	var lg Ledger
	lg.Update(ledgerSession)
	if got := lg.Render(maxUserTokens); got != ledgerRendered {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, ledgerRendered)
	}
}

// A ledger stored at one compaction and updated with the messages after
// its cut must equal the ledger of the whole history.
func TestLedgerIsIncremental(t *testing.T) {
	var whole Ledger
	whole.Update(ledgerSession)

	cut := 15 // the second turn's user message
	var first Ledger
	first.Update(ledgerSession[:cut])
	stored, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var resumed Ledger
	if err := json.Unmarshal(stored, &resumed); err != nil {
		t.Fatal(err)
	}
	resumed.Update(ledgerSession[cut:])
	if got, want := resumed.Render(maxUserTokens), whole.Render(maxUserTokens); got != want {
		t.Errorf("incremental:\n%s\nwhole:\n%s", got, want)
	}
}

func TestLedgerUserMessageBudget(t *testing.T) {
	var lg Ledger
	for i := range 5 {
		lg.Update([]model.Message{user(fmt.Sprintf("message %d %s", i+1, strings.Repeat("word ", 50)))})
	}
	// About 53 tokens each: the newest two fit.
	out := lg.Render(150)
	if !strings.Contains(out, "(3 earlier messages left out)\n4. message 4") || strings.Contains(out, "message 3 ") || !strings.Contains(out, "5. message 5") {
		t.Errorf("rendered:\n%s", out)
	}
	lg.Update([]model.Message{user(strings.Repeat("x", 3000))})
	if got := lg.User[5]; len(got) > maxUserBytes+len("…") {
		t.Errorf("long message kept at %d bytes", len(got))
	}
}

func TestLedgerLimitsLists(t *testing.T) {
	var lg Ledger
	for i := range maxLedgerCmds + 3 {
		id := fmt.Sprint("b", i)
		lg.Update([]model.Message{calls(call(id, "bash", fmt.Sprintf(`{"command":"echo %d"}`, i))), result(id, "", false)})
	}
	out := lg.Render(0)
	if !strings.Contains(out, "(3 earlier left out)\nexit 0: echo 3\n") || strings.Contains(out, "echo 2\n") {
		t.Errorf("rendered:\n%s", out)
	}
}

func TestMergeRange(t *testing.T) {
	for _, tc := range []struct {
		in   [][2]int
		add  [2]int
		want [][2]int
	}{
		{nil, [2]int{1, 5}, [][2]int{{1, 5}}},
		{[][2]int{{1, 5}}, [2]int{6, 9}, [][2]int{{1, 9}}},
		{[][2]int{{1, 5}}, [2]int{8, 9}, [][2]int{{1, 5}, {8, 9}}},
		{[][2]int{{1, 2}, {8, 9}, {20, 30}}, [2]int{2, 8}, [][2]int{{1, 9}, {20, 30}}},
		{[][2]int{{10, 20}}, [2]int{1, 3}, [][2]int{{1, 3}, {10, 20}}},
	} {
		if got := mergeRange(tc.in, tc.add); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("mergeRange(%v, %v) = %v, want %v", tc.in, tc.add, got, tc.want)
		}
	}
}
