package session

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/antoniosarro/wisp/internal/model"
)

// A session's long pastes and pasted images get labels, [Pasted text #1
// +40 lines] and [Image #2], numbered per session. Prompts are stored as
// the model got them, labels expanded (Expand); frontends show the labels
// (Collapse). Images are files in pasted/<session id> next to the
// database, so later turns can read them again; they go with the session.

// Paste is one labelled paste: text, or an image file.
type Paste struct {
	Label string
	Text  string // "" for an image
	Path  string // the image's file; "" for text
}

// expanded is what the model gets in place of the label: the text, or the
// label naming the image's file.
func (p Paste) expanded() string {
	if p.Path == "" {
		return p.Text
	}
	return strings.TrimSuffix(p.Label, "]") + ": " + p.Path + "]"
}

// imageExts are the file extensions of the image types a model takes.
var imageExts = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// pasteDir is where a session's pasted images are kept.
func (s *Store) pasteDir(sessionID string) string {
	return filepath.Join(filepath.Dir(s.path), "pasted", sessionID)
}

// SavePaste labels text pasted into one of the session's prompts, or img
// when it is set, saving the image's file.
func (s *Store) SavePaste(sessionID, text string, img *model.Image) (Paste, error) {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM pastes WHERE session_id = ?`, sessionID).Scan(&n); err != nil {
		return Paste{}, fmt.Errorf("counting pastes: %w", err)
	}
	p := Paste{Label: fmt.Sprintf("[Pasted text #%d +%d lines]", n+1, strings.Count(text, "\n")+1), Text: text}
	if img != nil {
		ext, ok := imageExts[img.MIME]
		if !ok {
			return Paste{}, fmt.Errorf("unsupported image type %s", img.MIME)
		}
		dir := s.pasteDir(sessionID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Paste{}, err
		}
		p = Paste{Label: fmt.Sprintf("[Image #%d]", n+1), Path: filepath.Join(dir, fmt.Sprint(n+1)+ext)}
		if err := os.WriteFile(p.Path, img.Data, 0o600); err != nil {
			return Paste{}, err
		}
	}
	if _, err := s.db.Exec(`INSERT INTO pastes (session_id, label, text, path) VALUES (?, ?, ?, ?)`, sessionID, p.Label, p.Text, p.Path); err != nil {
		return Paste{}, fmt.Errorf("saving paste: %w", err)
	}
	return p, nil
}

// Pastes returns the session's pastes, in the order they were made.
func (s *Store) Pastes(sessionID string) ([]Paste, error) {
	rows, err := s.db.Query(`SELECT label, text, path FROM pastes WHERE session_id = ? ORDER BY rowid`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("loading pastes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var all []Paste
	for rows.Next() {
		var p Paste
		if err := rows.Scan(&p.Label, &p.Text, &p.Path); err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	return all, rows.Err()
}

// Expand replaces the labels of pastes in text with what the model gets,
// returning the images to attach.
func Expand(text string, pastes []Paste) (string, []model.Image, error) {
	var images []model.Image
	for _, p := range pastes {
		if !strings.Contains(text, p.Label) {
			continue
		}
		if p.Path != "" {
			data, err := os.ReadFile(p.Path)
			if err != nil {
				return "", nil, fmt.Errorf("%s: %w", p.Label, err)
			}
			images = append(images, model.Image{MIME: mime.TypeByExtension(filepath.Ext(p.Path)), Data: data})
		}
		text = strings.ReplaceAll(text, p.Label, p.expanded())
	}
	return text, images, nil
}

// Collapse puts the labels of pastes back in text the model got.
func Collapse(text string, pastes []Paste) string {
	for _, p := range pastes {
		if e := p.expanded(); e != "" {
			text = strings.ReplaceAll(text, e, p.Label)
		}
	}
	return text
}
