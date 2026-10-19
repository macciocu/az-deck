// Package config persists small bits of UI state between runs.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

const maxRecent = 9

// State is remembered across sessions.
type State struct {
	Subscription string   `json:"subscription,omitempty"`
	RG           string   `json:"resourceGroup,omitempty"`
	RecentRGs    []string `json:"recentResourceGroups,omitempty"`

	path string
}

// Dir returns the azd config directory ($XDG_CONFIG_HOME/azd or ~/.config/azd).
func Dir() string {
	if d := os.Getenv("AZD_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "azd")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "azd")
	}
	return filepath.Join(home, ".config", "azd")
}

// Load reads the saved state; a missing or broken file yields an empty state.
func Load() *State {
	s := &State{path: filepath.Join(Dir(), "state.json")}
	if raw, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(raw, s)
	}
	return s
}

// Save writes the state to disk.
func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}

// TouchRG records a resource group as most recently used.
func (s *State) TouchRG(rg string) {
	s.RecentRGs = slices.DeleteFunc(s.RecentRGs, func(x string) bool { return x == rg })
	s.RecentRGs = append([]string{rg}, s.RecentRGs...)
	if len(s.RecentRGs) > maxRecent {
		s.RecentRGs = s.RecentRGs[:maxRecent]
	}
}
