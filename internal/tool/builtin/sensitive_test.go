package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepFromHomeSkipsCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".ssh", "id_x"), []byte("BEGIN KEY\n"), 0o600)
	_ = os.WriteFile(filepath.Join(home, "notes.txt"), []byte("BEGIN KEY\n"), 0o644)
	res, err := GrepTool{}.Run(context.Background(), json.RawMessage(fmt.Sprintf(`{"pattern":"BEGIN","path":%q}`, home)))
	if err != nil || !strings.Contains(res.Content, "notes.txt") || strings.Contains(res.Content, "id_x") {
		t.Errorf("grep = %q, %v", res.Content, err)
	}
	if !(GrepTool{}).RiskyCall(json.RawMessage(fmt.Sprintf(`{"pattern":"x","path":%q}`, filepath.Join(home, ".ssh")))) {
		t.Error("searching ~/.ssh directly didn't ask")
	}
}

func TestCredentialFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	writeFile(t, filepath.Join(home, ".ssh", "id_x"), "key")
	writeFile(t, filepath.Join(dir, "notes.txt"), "text")
	if err := os.Symlink(filepath.Join(home, ".ssh", "id_x"), filepath.Join(dir, "innocent")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		".env":         true,
		".env.example": false,
		"notes.txt":    false,
		"innocent":     true, // a symlink into ~/.ssh
	} {
		if got := credentialFile(filepath.Join(dir, name)); got != want {
			t.Errorf("credentialFile(%s) = %v, want %v", name, got, want)
		}
	}
}
