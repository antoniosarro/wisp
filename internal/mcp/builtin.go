package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
)

// goplsInstructions stands in for gopls's own, which it doesn't send over
// MCP: the long text `gopls mcp -instructions` prints insists on
// go_vulncheck, which needs the network.
const goplsInstructions = "Go code intelligence from gopls, cheaper than grep and reading whole files: " +
	"go_search finds symbols by fuzzy name; go_file_context summarizes what a file uses from the rest of its package; " +
	"go_package_api lists a package's exported API; go_symbol_references finds every use of a symbol " +
	"(a file and a name like Type.Method) before you change it, numbering lines from 0, so add 1 for read; " +
	"go_diagnostics reports errors in the files you edited; " +
	"go_workspace describes the module layout. go_rename_symbol returns the edits for a rename without applying them. " +
	"go_vulncheck needs network access."

// Builtin returns the servers wisp starts without configuration in
// workDir: gopls's MCP server (gopls v0.20 or later), in a Go module or
// workspace when gopls is on PATH. Its tools are in the tool list and run
// without asking: they only read, and a rename returns its edits. An
// mcp.json entry named gopls replaces it, and "disabled": true turns it
// off.
func Builtin(workDir string) map[string]ServerConfig {
	path, err := exec.LookPath("gopls")
	if err != nil || !inGoModule(workDir) {
		return nil
	}
	instructions := goplsInstructions
	return map[string]ServerConfig{"gopls": {
		Command: path, Args: []string{"mcp"}, Direct: true, ReadOnly: true, Instructions: &instructions,
	}}
}

// inGoModule reports whether dir or a parent holds a go.mod or go.work.
func inGoModule(dir string) bool {
	for {
		for _, name := range []string{"go.mod", "go.work"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
