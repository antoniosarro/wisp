package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniosarro/wisp/internal/model"
)

// Pastes are numbered per session, and expanding and collapsing their
// labels round-trips.
func TestPastesRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateSession("m")
	if err := s.AppendMessage(id, model.Message{Role: model.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("x\n", 11)
	tp, err := s.SavePaste(id, text, nil)
	if err != nil || tp.Label != "[Pasted text #1 +12 lines]" {
		t.Fatalf("text paste = %+v, %v", tp, err)
	}
	ip, err := s.SavePaste(id, "", &model.Image{MIME: "image/jpeg", Data: []byte("jpg")})
	if err != nil || ip.Label != "[Image #2]" || filepath.Base(ip.Path) != "2.jpg" {
		t.Fatalf("image paste = %+v, %v", ip, err)
	}
	pastes, err := s.Pastes(id)
	if err != nil || len(pastes) != 2 {
		t.Fatalf("Pastes = %+v, %v", pastes, err)
	}

	prompt := "see " + tp.Label + " and " + ip.Label
	sent, images, err := Expand(prompt, pastes)
	if err != nil || sent != "see "+text+" and [Image #2: "+ip.Path+"]" {
		t.Fatalf("Expand = %q, %v", sent, err)
	}
	if len(images) != 1 || images[0].MIME != "image/jpeg" || string(images[0].Data) != "jpg" {
		t.Errorf("images = %+v", images)
	}
	if got := Collapse(sent, pastes); got != prompt {
		t.Errorf("Collapse = %q, want %q", got, prompt)
	}
}
