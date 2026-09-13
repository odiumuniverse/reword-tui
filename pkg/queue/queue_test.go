package queue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.jsonl")
	s := Load(path)
	if len(s.Items) != 0 {
		t.Fatal("fresh store must be empty")
	}
	intents := []Intent{
		{Op: "grade", Word: "el pan", Mode: "rec", Result: "ok"},
		{Op: "triage", Word: "la foca", Decision: "known"},
		{Op: "add", Word: "hola", Tr: []string{"ENG=hello"}, Enroll: true},
		{Op: "select", Category: "food", Selected: true},
	}
	for _, it := range intents {
		if err := s.Append(it); err != nil {
			t.Fatal(err)
		}
	}
	r := Load(path)
	if len(r.Items) != 4 {
		t.Fatalf("want 4, got %d", len(r.Items))
	}
	if r.Items[0].Mode != "rec" || r.Items[2].Tr[0] != "ENG=hello" || !r.Items[3].Selected {
		t.Fatal("payload changed")
	}
	body := r.Items[0].ApplyBody()
	if body["op"] != "grade" || body["mode"] != "rec" || body["result"] != "ok" {
		t.Fatalf("bad apply body: %v", body)
	}
	add := r.Items[2].ApplyBody()
	if _, ok := add["transcription"]; ok {
		t.Fatal("empty transcription must be omitted")
	}
	if err := r.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("queue file must be gone")
	}
	if Load(path).Items != nil {
		t.Fatal("reload after clear must be empty")
	}
}

func TestCorruptLinesSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.jsonl")
	os.WriteFile(path, []byte("{\"op\":\"grade\",\"word\":\"x\"}\nnope\n{\"op\":\"\"}\n"), 0o644)
	if n := len(Load(path).Items); n != 1 {
		t.Fatalf("want 1, got %d", n)
	}
}

func TestConsumeKeepsTailAndGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.jsonl")
	s := Load(path)
	for _, w := range []string{"a", "b", "c"} {
		if err := s.Append(Intent{Op: "grade", Word: w}); err != nil {
			t.Fatal(err)
		}
	}
	// intent appended while the write was in flight
	if err := s.Append(Intent{Op: "triage", Word: "d", Decision: "known"}); err != nil {
		t.Fatal(err)
	}
	// garbage line between applied and pending
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("nope\n")
	f.Close()
	if err := s.Consume(3); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 1 || s.Items[0].Word != "d" {
		t.Fatalf("tail lost: %+v", s.Items)
	}
	r := Load(path)
	if len(r.Items) != 1 || r.Items[0].Word != "d" {
		t.Fatalf("file tail lost: %+v", r.Items)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "nope") {
		t.Fatal("garbage line must survive consume")
	}
	if err := s.Consume(5); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 0 {
		t.Fatal("items must be empty after full consume")
	}
	if len(Load(path).Items) != 0 {
		t.Fatal("no valid intents may remain after full consume")
	}
}

func TestApplyBodyPrefersID(t *testing.T) {
	it := Intent{Op: "grade", Word: "la carne", ID: 7393, Mode: "rec", Result: "ok"}
	if got := it.ApplyBody()["word"]; got != "7393" {
		t.Fatalf("a known id must name the word, got %v", got)
	}
	if got := (Intent{Op: "triage", Word: "la carne", Decision: "learn"}).ApplyBody()["word"]; got != "la carne" {
		t.Fatalf("intents without an id must keep the text, got %v", got)
	}
	if lbl := it.Label(); !strings.Contains(lbl, "la carne") {
		t.Fatalf("labels stay readable: %q", lbl)
	}
}
