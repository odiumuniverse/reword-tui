package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Prefs struct {
	NewFirst          string `json:"new_first"`
	ReviewFirst       string `json:"review_first"`
	Guess             bool   `json:"guess"`
	Keyboard          bool   `json:"keyboard"`
	ShowTranscription bool   `json:"show_transcription"`
	RevealAtOnce      bool   `json:"reveal_at_once"`
	ReviewFrom        string `json:"review_from"`
	Onboarded         bool   `json:"onboarded"`
}

func DefaultPrefs() Prefs {
	return Prefs{
		NewFirst:          "target",
		ReviewFirst:       "target",
		Guess:             true,
		Keyboard:          true,
		ShowTranscription: true,
		RevealAtOnce:      false,
		ReviewFrom:        "chosen",
		Onboarded:         false,
	}
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
	q := DefaultPrefs()
	get("new_first", &q.NewFirst)
	get("review_first", &q.ReviewFirst)
	get("guess", &q.Guess)
	get("keyboard", &q.Keyboard)
	get("show_transcription", &q.ShowTranscription)
	get("reveal_at_once", &q.RevealAtOnce)
	get("review_from", &q.ReviewFrom)
	get("onboarded", &q.Onboarded)
	if q.NewFirst == "" {
		q.NewFirst = p.NewFirst
	}
	if q.ReviewFirst == "" {
		q.ReviewFirst = p.ReviewFirst
	}
	if q.ReviewFrom == "" {
		q.ReviewFrom = p.ReviewFrom
	}
	return q
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
