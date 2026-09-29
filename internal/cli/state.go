package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// state is what wisp remembers between runs, outside any project. Every
// field is kept even where this version doesn't use it yet: saving the
// state rewrites the whole file, which other versions of wisp share.
type state struct {
	LastModel map[string]string `json:"last_model"`        // base URL -> model id
	Trusted   map[string]string `json:"trusted,omitempty"` // project dir -> hash of its trusted .wisp config
	// The newest release and when it was last asked for.
	LatestRelease string    `json:"latest_release,omitempty"`
	ReleaseCheck  time.Time `json:"release_check,omitzero"`
}

// loadState reads the state; a missing or unreadable file is an empty one.
func loadState() state {
	var s state
	if data, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// rememberModel records id as the model last used with baseURL. A failure
// only costs the convenience next time, so it is ignored.
func rememberModel(baseURL, id string) {
	s := loadState()
	if id == "" || s.LastModel[baseURL] == id {
		return
	}
	if s.LastModel == nil {
		s.LastModel = map[string]string{}
	}
	s.LastModel[baseURL] = id
	saveState(s)
}

// saveState writes the state through a temporary file, so a concurrent
// wisp never reads half of it. Failures are ignored, as for rememberModel.
func saveState(s state) {
	path := statePath()
	data, err := json.MarshalIndent(s, "", "  ")
	if path == "" || err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return
	}
	_, err = tmp.Write(append(data, '\n'))
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
}
