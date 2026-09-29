package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/antoniosarro/wisp/internal/mcp"
	"github.com/antoniosarro/wisp/internal/termsafe"
)

// A cloned repository's .wisp config could otherwise act as soon as wisp
// starts: its mcp.json runs commands as you. So it is used only once the
// user trusts it, and trusting it is remembered by the config's hash.

// projectConfigFiles lists the files of workDir's .wisp that act on
// launch: mcp.json starts servers.
func projectConfigFiles(workDir string) []string {
	if _, err := os.Stat(projectPath(workDir, "mcp.json")); err == nil {
		return []string{projectPath(workDir, "mcp.json")}
	}
	return nil
}

// configHash identifies the files' names and contents, so an edit asks
// again.
func configHash(files []string) string {
	h := sha256.New()
	for _, f := range files {
		data, _ := os.ReadFile(f)
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", f, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// trustProject decides whether workDir's .wisp config may be used. The
// user is asked once; the answer is remembered by the files' hash, so a
// change asks again, and a "no" asks again next time. Without a terminal
// to ask on, the config is ignored unless force (--trust-project) is set,
// which trusts it and remembers that.
func trustProject(workDir string, force bool, in io.Reader, out io.Writer, interactive bool) bool {
	files := projectConfigFiles(workDir)
	if len(files) == 0 {
		return true
	}
	sum, s := configHash(files), loadState()
	if s.Trusted[workDir] == sum {
		return true
	}
	if !force {
		if !interactive {
			_, _ = fmt.Fprintf(out, "wisp: ignoring %s: not trusted yet (run wisp in a terminal to review it, or pass --trust-project)\n",
				projectPath(workDir))
			return false
		}
		_, _ = fmt.Fprintf(out, "wisp: this project has its own configuration in %s:\n", projectPath(workDir))
		describeProjectConfig(workDir, out)
		_, _ = fmt.Fprint(out, "Trust it? Its MCP servers run as you. [y/N] ")
		if a := strings.ToLower(strings.TrimSpace(readLine(in))); a != "y" && a != "yes" {
			_, _ = fmt.Fprintln(out, "wisp: ignoring it this time; you'll be asked again")
			return false
		}
	}
	if s.Trusted == nil {
		s.Trusted = map[string]string{}
	}
	s.Trusted[workDir] = sum
	saveState(s)
	return true
}

// readLine reads up to a newline, a byte at a time: a buffered reader
// could swallow what the next prompt on stdin should read.
func readLine(in io.Reader) string {
	var line strings.Builder
	var b [1]byte
	for {
		n, err := in.Read(b[:])
		if n > 0 && b[0] != '\n' {
			line.WriteByte(b[0])
		}
		if err != nil || n > 0 && b[0] == '\n' {
			return line.String()
		}
	}
}

// interactiveTerminal says whether the user can be asked on stdin.
func interactiveTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// describeProjectConfig lists what the project config would do, before
// any variable is expanded. Everything shown comes from the repository,
// so control characters are shown, not acted on: an escape sequence could
// otherwise redraw the very lines the user is reviewing.
func describeProjectConfig(workDir string, out io.Writer) {
	servers, err := mcp.ReadConfig(projectPath(workDir, "mcp.json"))
	if err != nil {
		_, _ = fmt.Fprintf(out, "  mcp.json: unreadable: %s\n", termsafe.Show(err.Error()))
	}
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		s := servers[name]
		switch {
		case s.Disabled:
			continue
		case s.Command != "":
			_, _ = fmt.Fprintf(out, "  MCP server %s runs: %s\n", termsafe.Show(name), termsafe.Show(strings.Join(append([]string{s.Command}, s.Args...), " ")))
		default:
			_, _ = fmt.Fprintf(out, "  MCP server %s connects to: %s\n", termsafe.Show(name), termsafe.Show(s.URL))
		}
	}
}
