package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/antoniosarro/wisp/internal/model"
)

// stripANSI is rendered text without its styling.
var stripANSI = ansi.Strip

// testOptions are the options of a model already chosen, on a local
// endpoint, as the screen tests show it.
var testOptions = Options{Model: model.Info{ID: "fast-agent"}, BaseURL: "http://localhost:8091/v1"}

// keyRunes is s typed as one key message.
func keyRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
