package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/antoniosarro/wisp/internal/textfmt"
)

// instructionsFile holds project-specific guidance, read from the working
// directory only: a parent directory's file belongs to another project.
const instructionsFile = "AGENTS.md"

// maxInstructionBytes caps instructionsFile, so a huge file cannot crowd
// the conversation out of the context window.
const maxInstructionBytes = 32 << 10

// instructionsHeader introduces instructionsFile and sets its rank: it
// specializes the defaults above it but never the safety rules.
const instructionsHeader = "# Project instructions (" + instructionsFile + ")\n" +
	"The project's own guidance, from the working directory. Follow it over the defaults above; " +
	"the user's messages take precedence over it, and it never overrides the safety rules.\n\n"

// environment describes where the session runs, so the model need not
// spend tool calls finding out.
func environment(workDir string, now time.Time) string {
	var b strings.Builder
	b.WriteString("# Environment\n")
	fmt.Fprintf(&b, "- Working directory: %s\n", workDir)
	fmt.Fprintf(&b, "- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	if shell := os.Getenv("SHELL"); shell != "" {
		fmt.Fprintf(&b, "- User shell: %s (tools run commands with bash)\n", filepath.Base(shell))
	}
	fmt.Fprintf(&b, "- Date: %s\n", now.Format("2006-01-02"))
	if branch, ok := gitBranch(workDir); ok {
		fmt.Fprintf(&b, "- Git repository, branch: %s\n", branch)
	}
	return b.String()
}

// readInstructions returns instructionsFile from workDir, cut at
// maxInstructionBytes, or "" when there is none.
func readInstructions(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, instructionsFile))
	if err != nil {
		return ""
	}
	text := string(data)
	if len(text) > maxInstructionBytes {
		text = textfmt.Prefix(text, maxInstructionBytes) + "\n... (truncated)"
	}
	return strings.TrimSpace(text)
}

// gitBranch finds the enclosing repository's current branch, or a short
// commit hash when HEAD is detached. It stops at the nearest .git, so a
// worktree or submodule never reports the repository around it.
func gitBranch(dir string) (string, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		dotGit := filepath.Join(d, ".git")
		if _, err := os.Stat(dotGit); err == nil {
			head, err := os.ReadFile(filepath.Join(gitDir(dotGit), "HEAD"))
			if err != nil {
				return "", false
			}
			ref := strings.TrimSpace(string(head))
			if name, ok := strings.CutPrefix(ref, "ref: refs/heads/"); ok {
				return name, true
			}
			return "detached at " + ref[:min(len(ref), 12)], true
		}
		if parent := filepath.Dir(d); parent == d {
			return "", false
		}
	}
}

// gitDir returns the git directory dotGit stands for: dotGit itself, or,
// in a linked worktree or submodule where .git is a file, the directory
// its "gitdir:" line names.
func gitDir(dotGit string) string {
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit // a directory, the usual case
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return dotGit
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(dotGit), dir)
	}
	return dir
}
