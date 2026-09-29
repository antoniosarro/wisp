// Package agent runs configured sub-agents: separate loops with their own
// model, instructions, and tools, started by the main agent's agent tool.
//   - spec.go: agent files, Markdown with YAML front matter
//   - tool.go: the agent tool, which runs a sub-agent's loop for a task
//   - run.go: a run's progress, reported to frontends as it changes
package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is one sub-agent, defined by a Markdown file whose YAML front
// matter holds the settings and whose body holds its instructions.
type Spec struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"` // tells the main agent when to delegate
	Model         string   `yaml:"model"`       // defaults to the main model
	BaseURL       string   `yaml:"base_url"`    // defaults to the main endpoint
	APIKeyEnv     string   `yaml:"api_key_env"` // env var holding the key; defaults to the main key
	Tools         []string `yaml:"tools"`       // defaults to every tool except agent
	MaxIterations int      `yaml:"max_iterations"`

	Instructions string `yaml:"-"`
	Path         string `yaml:"-"`
}

// validName is an agent's name: it names the agent to the model, in
// prompts, and in traces.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Load reads *.md agent files from dirs in order; a later file overrides
// an earlier one with the same name. Missing directories are skipped.
func Load(dirs ...string) ([]Spec, error) {
	byName := map[string]Spec{}
	for _, dir := range dirs {
		paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			spec, err := parseFile(path)
			if err != nil {
				return nil, err
			}
			byName[spec.Name] = spec
		}
	}
	specs := make([]Spec, 0, len(byName))
	for _, s := range byName {
		specs = append(specs, s)
	}
	slices.SortFunc(specs, func(a, b Spec) int { return strings.Compare(a.Name, b.Name) })
	return specs, nil
}

// parseFile reads and parses one agent file.
func parseFile(path string) (Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, err
	}
	spec, err := parse(data)
	if err != nil {
		return Spec{}, fmt.Errorf("agent file %s: %w", path, err)
	}
	spec.Path = path
	return spec, nil
}

// parse reads an agent file's front matter and instructions. Unknown
// fields are errors: a typo would otherwise silently use a default.
func parse(data []byte) (Spec, error) {
	rest, ok := bytes.CutPrefix(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("---\n"))
	if !ok {
		return Spec{}, fmt.Errorf("must start with a --- front matter block")
	}
	header, body, ok := bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return Spec{}, fmt.Errorf("front matter is not closed with ---")
	}

	var spec Spec
	dec := yaml.NewDecoder(bytes.NewReader(header))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("front matter: %w", err)
	}
	spec.Instructions = strings.TrimSpace(strings.TrimPrefix(string(body), "\n"))

	switch {
	case !validName.MatchString(spec.Name):
		return Spec{}, fmt.Errorf("name %q must be lowercase letters, digits, - or _", spec.Name)
	case strings.TrimSpace(spec.Description) == "":
		return Spec{}, fmt.Errorf("description is required")
	case spec.Instructions == "":
		return Spec{}, fmt.Errorf("instructions (the text after the front matter) are required")
	case spec.MaxIterations < 0:
		return Spec{}, fmt.Errorf("max_iterations must be positive")
	}
	return spec, nil
}
