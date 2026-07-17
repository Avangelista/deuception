// Package prefs loads and saves a room's persisted preferences - the house ruleset,
// reaction labels, and each identity's preferred letter - to a JSON file beside the
// binary. It performs no game logic; the room owns validation.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Avangelista/big2-tui/internal/game"
)

const fileName = "big2-tui.prefs.json"

// Prefs is the persisted room configuration.
type Prefs struct {
	Rules     game.Rules        `json:"rules"`
	Reactions []string          `json:"reactions"`
	Letters   map[string]string `json:"letters"` // identity -> preferred A-Z letter
}

// DefaultPath is the prefs file beside the running binary, falling back to the working
// directory if the executable path can't be resolved.
func DefaultPath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), fileName)
	}
	return fileName
}

// Load reads prefs from path. A missing, unreadable, or corrupt file yields the zero
// value (classic rules, no remembered letters), so a first run just works.
func Load(path string) Prefs {
	data, err := os.ReadFile(path)
	if err != nil {
		return Prefs{}
	}
	var p Prefs
	if err := json.Unmarshal(data, &p); err != nil {
		return Prefs{}
	}
	return p
}

// Save writes prefs to path atomically (temp file then rename), so a crash mid-write
// can't corrupt an existing file.
func Save(path string, p Prefs) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// 0o600: the letters map records per-user SSH-key identities, so keep it owner-only.
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // don't leave a stray temp file behind
		return err
	}
	return nil
}
