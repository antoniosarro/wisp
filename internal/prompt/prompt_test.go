package prompt

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildIncludesEverySection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".git/HEAD", "ref: refs/heads/feature\n")
	writeFile(t, dir, instructionsFile, "Run just check before finishing.")

	got := Build(dir, time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"You are wisp", "# Tools", "# Safety",
		"Working directory: " + dir, "Date: 2026-09-22", "branch: feature",
		"# Project instructions", "Run just check before finishing.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(got, "\n\n\n") || got != strings.TrimSpace(got) {
		t.Error("sections should be separated by exactly one blank line, with none around the prompt")
	}
}

func TestBuildReadsInstructionsFromWorkDirOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, instructionsFile, "Run just check before finishing.")
	writeFile(t, dir, "pkg/.keep", "")

	if got := Build(filepath.Join(dir, "pkg"), time.Now()); strings.Contains(got, "just check") {
		t.Error("AGENTS.md from a parent directory was included")
	}
}

func TestBuildAgentUsesItsOwnInstructions(t *testing.T) {
	got := BuildAgent("You search code.", t.TempDir(), time.Now())
	if strings.Contains(got, "You are wisp") {
		t.Error("sub-agent prompt includes wisp's persona")
	}
	// A sub-agent keeps the shared rules: its own instructions replace
	// only the persona.
	for _, want := range []string{"You search code.", "sub-agent", "# Tools", "# Safety", "# Environment"} {
		if !strings.Contains(got, want) {
			t.Errorf("sub-agent prompt missing %q", want)
		}
	}
}
