package queue

import (
	"os"
	"path/filepath"
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
