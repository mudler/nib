package endpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/nib/types"
)

// Saved is the endpoint a previous session picked, kept next to
// credentials.json so the next session starts on it. It is nib-managed
// state rather than a config.yaml edit: config.yaml keeps describing its own
// endpoints, and nib never writes to it.
type Saved struct {
	ID    string `json:"id"`
	Model string `json:"model"`

	// ConfigFingerprint is Fingerprint of the config when the pick was
	// written, so a later start can tell whether config.yaml changed since
	// (see Set.Reconcile). Empty in a file written by an older nib.
	ConfigFingerprint string `json:"config_fingerprint,omitempty"`

	// Provider is the pre-endpoints field name, read for backward
	// compatibility and never written. An install saved before named
	// endpoints carries a registry ID here.
	Provider string `json:"provider,omitempty"`
}

// LoadSaved reads the saved pick. Any problem (absent, unreadable, corrupt)
// is the zero value: a session always has the config.yaml default to fall
// back to, and a startup must not fail over this file.
func LoadSaved(path string) Saved {
	if path == "" {
		return Saved{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Saved{}
	}
	var sv Saved
	if err := json.Unmarshal(data, &sv); err != nil {
		return Saved{}
	}
	if sv.ID == "" && sv.Provider != "" {
		sv.ID = sv.Provider
	}
	sv.Provider = ""
	return sv
}

// WriteSaved records the pick atomically, creating the directory if needed.
// An empty path is a no-op, mirroring LoadSaved's treatment of "": a Session
// built without a state directory (as some low-level tests do) has nowhere
// to persist to, and must not go writing a stray file into the process's
// working directory instead.
func WriteSaved(path string, sv Saved) error {
	if path == "" {
		return nil
	}
	sv.Provider = ""
	data, err := json.MarshalIndent(sv, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ClearSaved removes the saved pick. A missing file (or an empty path) is
// not an error: there is nothing left to forget.
func ClearSaved(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Startup resolves the endpoint a new session starts on: the saved pick when
// it still resolves, otherwise the config.yaml default. The third return is
// a user-visible note, non-empty only when a saved pick had to be dropped —
// silently changing the model between sessions is what this replaces.
func (s *Set) Startup(sv Saved) (Entry, types.ModelProviderConfig, string) {
	fallback := func(note string) (Entry, types.ModelProviderConfig, string) {
		e, _ := s.Lookup(DefaultID)
		cfg, _ := s.Config(DefaultID)
		return e, cfg, note
	}
	if sv.ID == "" || sv.ID == DefaultID {
		e, cfg, _ := fallback("")
		if sv.Model != "" {
			cfg.Model = sv.Model
			e.Model = sv.Model
		}
		return e, cfg, ""
	}
	e, ok := s.Lookup(sv.ID)
	if !ok {
		def, _ := s.Config(DefaultID)
		return fallback(fmt.Sprintf("%s is no longer in config.yaml · using the default (%s)", sv.ID, def.Model))
	}
	cfg, err := s.Config(sv.ID)
	if err != nil {
		def, _ := s.Config(DefaultID)
		return fallback(fmt.Sprintf("%s could not be resolved (%v) · using the default (%s)", sv.ID, err, def.Model))
	}
	if sv.Model != "" {
		cfg.Model = sv.Model
		e.Model = sv.Model
	}
	return e, cfg, ""
}
