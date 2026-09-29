// Package prompt builds the system prompt sent ahead of every request. It
// is assembled from sections, in order: the persona (wisp's own, or a
// sub-agent's instructions), the tool rules, the safety rules, a snapshot
// of the environment, and the project's AGENTS.md when there is one.
package prompt

import (
	_ "embed"
	"strings"
	"time"
)

var (
	// mainPrompt is wisp's own persona and working style.
	//go:embed prompts/main.md
	mainPrompt string
	// subAgentNote follows a sub-agent's instructions, telling it that
	// another agent, not the user, reads its final message.
	//go:embed prompts/subagent.md
	subAgentNote string
	// toolRules explains how to use the tools well; every agent gets it.
	//go:embed prompts/tools.md
	toolRules string
	// safetyRules sets what needs approval and what counts as an
	// instruction; every agent gets it, and nothing else overrides it.
	//go:embed prompts/safety.md
	safetyRules string
	// Compact asks the model for a summary of the conversation, before
	// its earlier part is replaced by it.
	//go:embed prompts/compact.md
	Compact string
)

// Build returns the main agent's system prompt for a session in workDir.
func Build(workDir string, now time.Time) string {
	return build(mainPrompt, workDir, now)
}

// BuildAgent returns a sub-agent's system prompt around its own
// instructions, which take the place of wisp's persona.
func BuildAgent(instructions, workDir string, now time.Time) string {
	return build(strings.TrimSpace(instructions)+"\n\n"+subAgentNote, workDir, now)
}

// build joins the sections around persona, separated by blank lines.
func build(persona, workDir string, now time.Time) string {
	sections := []string{persona, toolRules, safetyRules, environment(workDir, now)}
	if text := readInstructions(workDir); text != "" {
		sections = append(sections, instructionsHeader+text)
	}
	for i, s := range sections {
		sections[i] = strings.TrimSpace(s)
	}
	return strings.Join(sections, "\n\n")
}
