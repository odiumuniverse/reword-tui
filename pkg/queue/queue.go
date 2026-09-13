package queue

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

type Intent struct {
	Op            string   `json:"op"`
	Word          string   `json:"word,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	Result        string   `json:"result,omitempty"`
	Decision      string   `json:"decision,omitempty"`
	Tr            []string `json:"tr,omitempty"`
	Transcription string   `json:"transcription,omitempty"`
	Enroll        bool     `json:"enroll,omitempty"`
	Category      string   `json:"category,omitempty"`
	Selected      bool     `json:"selected,omitempty"`
}

func (it Intent) ApplyBody() map[string]any {
	m := map[string]any{"op": it.Op}
	switch it.Op {
	case "grade":
		m["word"] = it.Word
		m["mode"] = it.Mode
		m["result"] = it.Result
	case "triage", "enroll", "remove", "reset", "postpone":
		m["word"] = it.Word
		if it.Op == "triage" {
			m["decision"] = it.Decision
		}
	case "add":
		m["word"] = it.Word
		m["tr"] = it.Tr
		if it.Transcription != "" {
			m["transcription"] = it.Transcription
		}
		if it.Enroll {
			m["enroll"] = true
		}
	}
	return m
}

func (it Intent) Label() string {
	switch it.Op {
	case "grade":
		return "grade " + it.Word + " " + it.Mode + "/" + it.Result
	case "triage":
		return "triage " + it.Word + " " + it.Decision
	case "enroll":
		return "enroll " + it.Word
	case "add":
		return "add " + it.Word
	case "select":
		if it.Selected {
			return "select " + it.Category + " on"
		}
		return "select " + it.Category + " off"
	case "remove":
		return "remove " + it.Word
	case "reset":
		return "reset " + it.Word
	case "postpone":
		return "postpone " + it.Word
	}
	return it.Op
}

type Store struct {
	Path  string
	Items []Intent
}

func Load(path string) Store {
	s := Store{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var it Intent
		if json.Unmarshal(line, &it) == nil && it.Op != "" {
			s.Items = append(s.Items, it)
		}
	}
	return s
}

func (s *Store) Append(it Intent) error {
	if dir := filepath.Dir(s.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	line, err := json.Marshal(it)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	s.Items = append(s.Items, it)
	return nil
}

func (s *Store) Clear() error {
	s.Items = nil
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Consume drops the first n applied intents, keeping intents appended
// while the write was in flight. Unparseable lines are preserved as-is.
func (s *Store) Consume(n int) error {
	if n <= 0 {
		return nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			s.Items = nil
			return nil
		}
		return err
	}
	var keep []byte
	skipped := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var it Intent
		if skipped < n && json.Unmarshal(line, &it) == nil && it.Op != "" {
			skipped++
			continue
		}
		keep = append(keep, line...)
		keep = append(keep, '\n')
	}
	if len(keep) == 0 {
		s.Items = nil
		if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.WriteFile(s.Path, keep, 0o644); err != nil {
		return err
	}
	if n >= len(s.Items) {
		s.Items = nil
	} else {
		s.Items = append([]Intent{}, s.Items[n:]...)
	}
	return nil
}
