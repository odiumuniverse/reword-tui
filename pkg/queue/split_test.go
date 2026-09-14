package queue

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPathForSitsNextToTheBase(t *testing.T) {
	if got := PathFor("/a/queue.jsonl", "es"); got != "/a/queue-es.jsonl" {
		t.Fatalf("got %s", got)
	}
	if got := PathFor("/a/q", "en"); got != "/a/q-en" {
		t.Fatalf("got %s", got)
	}
}

func TestSplitKeepsOrderAndLeftovers(t *testing.T) {
	items := []Intent{
		{Op: "grade", Word: "la manzana"},
		{Op: "triage", Word: "trend"},
		{Op: "select", Category: "anatomy"},
		{Op: "answer", Word: "la carne"},
	}
	owner := map[string]string{"la manzana": "es", "trend": "en", "la carne": "es"}
	byApp, rest := Split(items, func(it Intent) string { return owner[it.Word] })
	labels := func(its []Intent) []string {
		var out []string
		for _, it := range its {
			out = append(out, it.App+":"+it.Label())
		}
		return out
	}
	if got := labels(byApp["es"]); !slices.Equal(got, []string{"es:grade la manzana /", "es:answer la carne /right"}) {
		t.Fatalf("es: %v", got)
	}
	if got := labels(byApp["en"]); !slices.Equal(got, []string{"en:triage trend "}) {
		t.Fatalf("en: %v", got)
	}
	if len(rest) != 1 || rest[0].Category != "anatomy" || rest[0].App != "" {
		t.Fatalf("an intent no app owns stays apart: %+v", rest)
	}
}

func TestRewriteReplacesAndEmptyRemoves(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "q.jsonl")}
	if err := s.Append(Intent{Op: "grade", Word: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rewrite([]Intent{{Op: "grade", Word: "b", App: "es"}}); err != nil {
		t.Fatal(err)
	}
	if got := Load(s.Path).Items; len(got) != 1 || got[0].Word != "b" || got[0].App != "es" {
		t.Fatalf("rewrite must replace the file: %+v", got)
	}
	if err := s.Rewrite(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatal("an empty rewrite removes the queue")
	}
}
