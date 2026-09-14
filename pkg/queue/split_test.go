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

func TestPlaceByTimeFollowsTheAppInUse(t *testing.T) {
	marks := []Mark{{TS: 100, App: "es"}, {TS: 200, App: "en"}, {TS: 300, App: "es"}, {TS: 0, App: "en"}}
	rest := []Intent{
		{Op: "select", Category: "anatomy", TS: 250},
		{Op: "select", Category: "animals", TS: 350},
		{Op: "select", Category: "early", TS: 50},
		{Op: "select", Category: "old"},
		{Op: "select", Category: "gone", TS: 260},
	}
	fits := func(app string, it Intent) bool { return it.Category != "gone" }
	placed, left := PlaceByTime(rest, marks, fits)
	if en := placed["en"]; len(en) != 1 || en[0].Category != "anatomy" || en[0].App != "en" {
		t.Fatalf("a change made while en was in use goes to en: %+v", en)
	}
	if es := placed["es"]; len(es) != 1 || es[0].Category != "animals" {
		t.Fatalf("the latest app before a change takes it: %+v", es)
	}
	var names []string
	for _, it := range left {
		names = append(names, it.Category)
	}
	if !slices.Equal(names, []string{"early", "old", "gone"}) {
		t.Fatalf("no mark before it, no time, or an app that cannot take it: left %v", names)
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
