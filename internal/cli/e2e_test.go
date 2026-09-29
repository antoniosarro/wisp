package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/testutil/fakemodel"
)

// TestMain lets the test binary act as the wisp command: testscript runs
// scripts' "exec wisp" by re-running this binary under that name.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"wisp": func() { os.Exit(Main()) },
	})
}

// TestScripts runs the end-to-end scenarios in testdata/script. Each is a
// txtar archive: a script of commands followed by the files it works on.
// wisp runs as a real process against a fake model server; see the
// commands below for what scripts can check.
func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			// Keep wisp's remembered state and config inside the scenario.
			env.Setenv("XDG_STATE_HOME", filepath.Join(env.WorkDir, ".state"))
			env.Setenv("XDG_CONFIG_HOME", filepath.Join(env.WorkDir, ".config"))
			env.Setenv("XDG_DATA_HOME", filepath.Join(env.WorkDir, ".data"))
			env.Setenv("HOME", filepath.Join(env.WorkDir, ".home"))
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"fakemodel": cmdFakeModel,
			"requests":  cmdRequests,
			"session":   cmdSession,
		},
	})
}

// cmdFakeModel starts a fake model server for the rest of the script:
//
//	fakemodel SCRIPT.yaml
//
// It points WISP_BASE_URL at the server and appends each chat request
// wisp sends to requests.jsonl, for grep.
func cmdFakeModel(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: fakemodel SCRIPT.yaml")
	}
	script, err := fakemodel.Load(ts.MkAbs(args[0]))
	ts.Check(err)
	srv := fakemodel.Start(script, ts.MkAbs("requests.jsonl"))
	ts.Defer(srv.Close)
	ts.Setenv("WISP_BASE_URL", srv.URL)
}

// cmdRequests checks the chat requests in requests.jsonl:
//
//	requests count N   exactly N requests were sent
//	requests valid     every request passes fakemodel.Check
func cmdRequests(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) == 0 {
		ts.Fatalf("usage: requests count N | requests valid")
	}
	var reqs []json.RawMessage
	if f, err := os.Open(ts.MkAbs("requests.jsonl")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 16<<20) // a request carries the whole conversation
		for sc.Scan() {
			reqs = append(reqs, json.RawMessage(append([]byte(nil), sc.Bytes()...)))
		}
		_ = f.Close()
		ts.Check(sc.Err())
	}
	switch {
	case args[0] == "count" && len(args) == 2:
		want, err := strconv.Atoi(args[1])
		ts.Check(err)
		if len(reqs) != want {
			ts.Fatalf("%d requests sent, want %d", len(reqs), want)
		}
	case args[0] == "valid" && len(args) == 1:
		if err := fakemodel.Check(reqs); err != nil {
			ts.Fatalf("invalid request: %v", err)
		}
	default:
		ts.Fatalf("usage: requests count N | requests valid")
	}
}

// cmdSession reads the newest session in the scenario's session database:
//
//	session id        sets $SESSION to its id
//	session history   prints its messages, one "role: content" line each
func cmdSession(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: session id | session history")
	}
	store, err := session.Open(ts.MkAbs(".data/wisp/session.db"))
	ts.Check(err)
	defer func() { _ = store.Close() }()
	sessions, err := store.ListSessions()
	ts.Check(err)
	if len(sessions) == 0 {
		ts.Fatalf("no sessions")
	}
	id := sessions[0].ID
	switch args[0] {
	case "id":
		ts.Setenv("SESSION", id)
	case "history":
		history, err := store.LoadHistory(id)
		ts.Check(err)
		for _, m := range history {
			line := strings.ReplaceAll(m.Content, "\n", `\n`)
			for _, c := range m.ToolCalls {
				line += fmt.Sprintf(" [call %s %s]", c.Name, c.Args)
			}
			_, _ = fmt.Fprintf(ts.Stdout(), "%s: %s\n", m.Role, line)
		}
	default:
		ts.Fatalf("usage: session id | session history")
	}
}
