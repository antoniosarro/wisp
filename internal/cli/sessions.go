package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/antoniosarro/wisp/internal/model"
	"github.com/antoniosarro/wisp/internal/session"
	"github.com/antoniosarro/wisp/internal/termsafe"
)

// openStore opens the session database, scoped to the working directory.
// A project's sessions from an older version, kept in its own database,
// are moved into it first.
func openStore() (*session.Store, error) {
	path, err := sessionDBPath()
	if err != nil {
		return nil, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	store, err := session.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening session store: %w", err)
	}
	store.Dir = wd
	if _, err := os.Stat(legacySessionDB); err == nil {
		if err := store.Import(legacySessionDB, wd); err != nil {
			_ = store.Close()
			return nil, err
		}
		// Imported in one transaction, so the old copy can go.
		for _, f := range []string{legacySessionDB, legacySessionDB + "-wal", legacySessionDB + "-shm"} {
			_ = os.Remove(f)
		}
		fmt.Fprintf(os.Stderr, "wisp: moved this project's sessions from %s to %s\n", legacySessionDB, path)
	}
	return store, nil
}

// openSession opens the session store and either resumes resumeID or
// creates a new session for modelName.
func openSession(resumeID, modelName string) (*session.Store, []model.Message, string, error) {
	store, err := openStore()
	if err != nil {
		return nil, nil, "", err
	}
	history, id, err := loadOrCreate(store, resumeID, modelName)
	if err != nil {
		_ = store.Close()
		return nil, nil, "", err
	}
	return store, history, id, nil
}

// loadOrCreate resumes resumeID, or creates a session when it is "".
func loadOrCreate(store *session.Store, resumeID, modelName string) ([]model.Message, string, error) {
	if resumeID == "" {
		id, err := store.CreateSession(modelName)
		return nil, id, err
	}
	history, err := store.Resume(resumeID)
	return history, resumeID, err
}

// printSessions lists this directory's sessions for --sessions.
func printSessions() error {
	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	sessions, err := store.ListSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("No saved sessions in this directory.")
	}
	for _, s := range sessions {
		// The model name comes from the endpoint, the preview from a prompt:
		// neither may drive the terminal.
		fmt.Printf("%s  %s  %s  %s\n", s.ID, s.CreatedAt.Format("2006-01-02 15:04"), termsafe.Strip(s.Model), termsafe.Strip(s.OneLinePreview()))
	}
	return nil
}

// resumedModel is the model a session last used, or "".
func resumedModel(id string) string {
	if id == "" {
		return ""
	}
	store, err := openStore()
	if err != nil {
		return ""
	}
	defer func() { _ = store.Close() }()
	name, _ := store.SessionModel(id)
	return name
}
