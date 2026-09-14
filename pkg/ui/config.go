package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Prefs are this computer's own settings. The learning settings live in
// the backup's SETTINGS and are shared with the phone (rwcore.Synced).
type Prefs struct {
	RevealAtOnce bool `json:"reveal_at_once"`
	// InvertedSwipes puts the positive answer on → and the negative on ←,
	// like the phone's setting of the same name, which it keeps per device.
	InvertedSwipes bool `json:"inverted_swipes"`
	Onboarded      bool `json:"onboarded"`
	// ShowTranscription mirrors the synced show_transcription setting.
	ShowTranscription bool `json:"-"`
}

func DefaultPrefs() Prefs {
	return Prefs{ShowTranscription: true}
}

func prefsPath() string {
	if p := os.Getenv("REWORD_TUI_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "reword-tui", "config.json")
}

func LoadPrefs() Prefs {
	p := DefaultPrefs()
	data, err := os.ReadFile(prefsPath())
	if err != nil {
		return p
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return p
	}
	get := func(key string, dst any) {
		if b, ok := raw[key]; ok {
			json.Unmarshal(b, dst)
		}
	}
	get("reveal_at_once", &p.RevealAtOnce)
	get("inverted_swipes", &p.InvertedSwipes)
	get("onboarded", &p.Onboarded)
	return p
}

func (p Prefs) Save() error {
	path := prefsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
