package queue

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type Intent struct {
	Op            string          `json:"op"`
	App           string          `json:"app,omitempty"`
	Word          string          `json:"word,omitempty"`
	ID            int64           `json:"id,omitzero"`
	Mode          string          `json:"mode,omitempty"`
	Result        string          `json:"result,omitempty"`
	Decision      string          `json:"decision,omitempty"`
	Tr            []string        `json:"tr,omitempty"`
	Transcription string          `json:"transcription,omitempty"`
	Enroll        bool            `json:"enroll,omitempty"`
	Category      string          `json:"category,omitempty"`
	Selected      bool            `json:"selected,omitempty"`
	Positive      bool            `json:"positive,omitempty"`
	TS            int64           `json:"ts,omitzero"`
	Name          string          `json:"name,omitempty"`
	Value         string          `json:"value,omitempty"`
	Row           json.RawMessage `json:"row,omitempty"`
	At            int64           `json:"at,omitzero"`
	Goal          int64           `json:"goal,omitzero"`
	By            int64           `json:"by,omitzero"`
}

func (it Intent) ApplyBody() map[string]any {
	m := map[string]any{"op": it.Op}
	if it.TS != 0 {
		m["ts"] = it.TS
	}
	switch it.Op {
	case "answer":
		m["word"] = it.ref()
		m["mode"] = it.Mode
		m["positive"] = it.Positive
	case "select":
		m["category"] = it.Category
		m["selected"] = it.Selected
	case "setting":
		m["name"] = it.Name
		m["value"] = it.Value
	case "restore":
		m["word"] = it.ref()
		m["row"] = it.Row
		m["at"] = it.At
	case "goal":
		m["goal"] = it.Goal
	case "raise_goal":
		m["by"] = it.By
	case "grade":
		m["word"] = it.ref()
		m["mode"] = it.Mode
		m["result"] = it.Result
	case "triage", "enroll", "remove", "reset", "postpone":
		m["word"] = it.ref()
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

func (it Intent) ref() string {
	if it.ID != 0 {
		return strconv.FormatInt(it.ID, 10)
	}
	return it.Word
}

func (it Intent) Label() string {
	switch it.Op {
	case "answer":
		side := "right"
		if it.Positive {
			side = "left"
		}
		return "answer " + it.Word + " " + it.Mode + "/" + side
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
	case "setting":
		return "setting " + it.Name + " = " + it.Value
	case "restore":
		return "undo " + it.Word
	case "goal":
		return "daily goal " + strconv.FormatInt(it.Goal, 10)
	case "raise_goal":
		return "today's goal +" + strconv.FormatInt(it.By, 10)
	}
	return it.Op
}

func PathFor(base, app string) string {
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext) + "-" + app + ext
}

func Split(items []Intent, owner func(Intent) string) (map[string][]Intent, []Intent) {
	byApp := map[string][]Intent{}
	var rest []Intent
	for _, it := range items {
		app := owner(it)
		if app == "" {
			rest = append(rest, it)
			continue
		}
		it.App = app
		byApp[app] = append(byApp[app], it)
	}
	return byApp, rest
}

type Mark struct {
	TS  int64
	App string
}

func PlaceByTime(rest []Intent, marks []Mark, fits func(app string, it Intent) bool) (map[string][]Intent, []Intent) {
	placed := map[string][]Intent{}
	var left []Intent
	for _, it := range rest {
		var best Mark
		for _, mk := range marks {
			if mk.App != "" && mk.TS > 0 && mk.TS <= it.TS && mk.TS >= best.TS {
				best = mk
			}
		}
		if it.TS != 0 && best.App != "" && fits(best.App, it) {
			it.App = best.App
			placed[best.App] = append(placed[best.App], it)
			continue
		}
		left = append(left, it)
	}
	return placed, left
}

type Store struct {
	Path  string
	Items []Intent
}

func (s *Store) Rewrite(items []Intent) error {
	if len(items) == 0 {
		return s.Clear()
	}
	var buf bytes.Buffer
	for _, it := range items {
		line, err := json.Marshal(it)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return err
	}
	s.Items = slices.Clone(items)
	return nil
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
	if dir := filepath.Dir(s.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".queue-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(keep); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		os.Remove(tmpName)
		return err
	}
	if n >= len(s.Items) {
		s.Items = nil
	} else {
		s.Items = append([]Intent{}, s.Items[n:]...)
	}
	return nil
}
