package ui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

var errTest = errors.New("test error")

func testModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("REWORD_TUI_CONFIG", filepath.Join(dir, "config.json"))
	cfg := Config{QueuePath: filepath.Join(dir, "q.jsonl")}
	m := New(cfg)
	m.appID = "es"
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(t *testing.T, m Model, k string) Model {
	t.Helper()
	next, _ := m.Update(key(k))
	return next.(Model)
}

func TestOverdueStr(t *testing.T) {
	cases := map[int64]string{45: "45s", 120: "2m", 7200: "2h", 90000: "25h", 200000: "2d"}
	for in, want := range cases {
		if got := overdueStr(in); got != want {
			t.Fatalf("overdueStr(%d)=%s want %s", in, got, want)
		}
	}
}

func TestParseExamples(t *testing.T) {
	raw := `[{"o":"Como #pescado# los viernes","t":"desc"}]`
	got := parseExamples(raw)
	if len(got) != 1 || got[0] != "Como pescado los viernes — desc" {
		t.Fatalf("bad parse: %v", got)
	}
	if parseExamples("") != nil || parseExamples("zzz") != nil {
		t.Fatal("empty/garbage must give nil")
	}
}

func TestR1Flow(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.cur = &card{kind: cR1, word: "el pan", mode: "rec", prompt: "el pan"}
	m = press(t, m, " ")
	if !m.sess.cur.reveal {
		t.Fatal("space must reveal")
	}
	m = press(t, m, "g")
	if len(m.q.Items) != 1 {
		t.Fatalf("want 1 queued, got %d", len(m.q.Items))
	}
	it := m.q.Items[0]
	if it.Op != "grade" || it.Mode != "rec" || it.Result != "ok" {
		t.Fatalf("bad intent: %+v", it)
	}
	if m.sess.ok != 1 || !m.sess.cur.done {
		t.Fatal("counters/card state wrong")
	}
}

func TestR1Missed(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.cur = &card{kind: cR1, word: "el pan", mode: "rec"}
	m = press(t, m, "m")
	if m.q.Items[0].Result != "fail" || m.sess.fail != 1 {
		t.Fatal("missed must queue fail")
	}
}

func TestL1LearnQueues(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.pool = []rwcore.Word{
		{Text: "a", Translations: map[string]string{"RUS": "а"}},
		{Text: "b", Translations: map[string]string{"RUS": "б"}},
		{Text: "c", Translations: map[string]string{"RUS": "в"}},
		{Text: "d", Translations: map[string]string{"RUS": "г"}},
		{Text: "e", Translations: map[string]string{"RUS": "д"}},
	}
	m.sess.cur = &card{kind: cL1, word: "a"}
	m = press(t, m, "l")
	if len(m.q.Items) != 1 || m.q.Items[0].Decision != "learn" {
		t.Fatalf("bad triage intent: %+v", m.q.Items)
	}
	if !m.sess.cur.done {
		t.Fatal("L1 learn must resolve the card")
	}
	m = press(t, m, "esc")
	if m.screen != sLearn {
		t.Fatal("esc must leave session")
	}
}

func TestMakeChoices(t *testing.T) {
	m := testModel(t)
	m.pool = []rwcore.Word{
		{Text: "a", Translations: map[string]string{"RUS": "а"}, Pos: i64p(1)},
		{Text: "b", Translations: map[string]string{"RUS": "б"}, Pos: i64p(1)},
		{Text: "c", Translations: map[string]string{"RUS": "в"}, Pos: i64p(1)},
		{Text: "d", Translations: map[string]string{"RUS": "г"}, Pos: i64p(1)},
	}
	w := rwcore.Word{Text: "a", Translations: map[string]string{"RUS": "а"}, Pos: i64p(1)}
	choices, ans, ok := m.makeChoices(w)
	if !ok || len(choices) != 4 || choices[ans] != "а" {
		t.Fatalf("bad choices: %v ans %d ok %v", choices, ans, ok)
	}
}

func i64p(v int64) *int64 { return &v }

func TestTypedFlow(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.cur = &card{kind: cR3, word: "el pan", mode: "rep", prompt: "хлеб", expected: []string{"el pan"}, attempts: 3}
	m.sess.typing = true
	m = press(t, m, "e")
	m = press(t, m, "l")
	m = press(t, m, " ")
	m = press(t, m, "p")
	m = press(t, m, "a")
	m = press(t, m, "n")
	m = press(t, m, "enter")
	if len(m.q.Items) != 1 || m.q.Items[0].Result != "ok" {
		t.Fatalf("typed correct must grade ok: %+v", m.q.Items)
	}
	m2 := testModel(t)
	m2.screen = sSession
	m2.sess.cur = &card{kind: cR3, word: "x", mode: "rep", expected: []string{"y"}, attempts: 3}
	for i := 0; i < 3; i++ {
		m2 = press(t, m2, "z")
		m2 = press(t, m2, "enter")
	}
	if len(m2.q.Items) != 1 || m2.q.Items[0].Result != "fail" {
		t.Fatalf("3 wrong attempts must grade fail: %+v", m2.q.Items)
	}
}

func TestNormalize(t *testing.T) {
	if normalizeAnswer("El Pan!") == normalizeAnswer("el  pan!") {
	} else {
		t.Fatal("case/space folding broken")
	}
	if normalizeAnswer("a‐b") != "a b" {
		t.Fatalf("dash folding broken: %q", normalizeAnswer("a‐b"))
	}
}

func TestBuildReviewNativeFirst(t *testing.T) {
	m := testModel(t)
	m.prefs.ReviewFirst = "native"
	m.due = []rwcore.DueItem{{ID: 1, Word: "w", Modes: []int64{1, 2}, OverdueSecs: 5}}
	m.buildReview()
	if len(m.sess.units) != 2 || m.sess.units[0].mode != 2 || m.sess.units[1].mode != 1 {
		t.Fatalf("native-first must order rep,rec: %+v", m.sess.units)
	}
	m.prefs.ReviewFirst = "target"
	m.buildReview()
	if m.sess.units[0].mode != 1 {
		t.Fatalf("target-first must order rec first: %+v", m.sess.units)
	}
}

func TestOnboardingTriggers(t *testing.T) {
	m := testModel(t)
	m.appID = "es"
	m.screen = sLearn
	m.today = &rwcore.Today{}
	next, _ := m.onCats(catsMsg{cats: []rwcore.Category{{ID: "c"}}})
	m = next.(Model)
	if m.obStep != 1 || m.screen != sVocab {
		t.Fatal("first run must enter category onboarding")
	}
}

func TestOnboardingSkipsKnownData(t *testing.T) {
	m := testModel(t)
	m.appID = "es"
	m.screen = sLearn
	g := int64(10)
	m.today = &rwcore.Today{Goal: &g}
	next, _ := m.onCats(catsMsg{cats: []rwcore.Category{{ID: "c", Selected: true}}})
	m = next.(Model)
	if m.obStep != 0 || !m.prefs.Onboarded {
		t.Fatal("existing selection and goal must skip onboarding silently")
	}
}

func TestOnboardingAsksGoalOnly(t *testing.T) {
	m := testModel(t)
	m.appID = "es"
	m.screen = sLearn
	m.today = &rwcore.Today{}
	next, _ := m.onCats(catsMsg{cats: []rwcore.Category{{ID: "c", Selected: true}}})
	m = next.(Model)
	if m.obStep != 2 || m.ov != oGoal {
		t.Fatal("existing selection without goal must ask goal only")
	}
}

func TestSettingsPersist(t *testing.T) {
	m := testModel(t)
	m.screen = sSettings
	m.setIdx = 3
	m = press(t, m, " ")
	if m.prefs.Guess {
		t.Fatal("guess must toggle off")
	}
	reread := LoadPrefs()
	if reread.Guess {
		t.Fatal("prefs must persist")
	}
}

func TestFullSessionFlow(t *testing.T) {
	m := testModel(t)
	m.prefs.ReviewFirst = "target"
	m.prefs.Keyboard = false
	m.due = []rwcore.DueItem{
		{ID: 1, Word: "w1", Modes: []int64{1, 2}, OverdueSecs: 90},
		{ID: 2, Word: "w2", Modes: []int64{2}, OverdueSecs: 10},
	}
	m.pool = []rwcore.Word{
		{Text: "w1", Translations: map[string]string{"RUS": "п1"}, Recognition: rwcore.ModeState{Level: 2}, Reproduction: rwcore.ModeState{Level: 2}},
		{Text: "w2", Translations: map[string]string{"RUS": "п2"}, Recognition: rwcore.ModeState{Level: 2}, Reproduction: rwcore.ModeState{Level: 2}},
		{Text: "n1", Translations: map[string]string{"RUS": "н1"}},
		{Text: "n2", Translations: map[string]string{"RUS": "н2"}},
		{Text: "n3", Translations: map[string]string{"RUS": "н3"}},
		{Text: "n4", Translations: map[string]string{"RUS": "н4"}},
	}
	m.buildReview()
	if len(m.sess.units) != 3 {
		t.Fatalf("want 3 review units, got %d", len(m.sess.units))
	}
	m.sess.mode = 2
	m.buildLearnQueue()
	seen := map[cardKind]int{}
	for i := 0; i < 40 && (len(m.sess.units) > 0 || m.sess.lpos < len(m.sess.learn)); i++ {
		c := m.nextCard()
		if c == nil {
			break
		}
		seen[c.kind]++
		m.sess.cur = c
		switch c.kind {
		case cR1:
			mm := m
			mm.sess.cur.reveal = true
			mm.gradeCurrent(true)
		case cR2:
			m.gradeCurrent(true)
		case cL1:
			m.enqueue(queue.Intent{Op: "triage", Word: c.word, Decision: "learn"})
			c.done = true
		case cL1b:
			c.done, c.wasOk = true, true
		case cL2:
			c.done, c.wasOk = true, true
		case cR3:
			m.gradeCurrent(true)
		}
	}
	if len(m.sess.units) != 0 {
		t.Fatalf("review units must drain, left %d", len(m.sess.units))
	}
	if seen[cR1] == 0 || seen[cL1] == 0 && seen[cL1b] == 0 {
		t.Fatalf("must cover review and learning cards: %v", seen)
	}
}

func TestViewAllCards(t *testing.T) {
	m := testModel(t)
	cards := []*card{
		{kind: cR1, prompt: "w", native: "н", tr: "t"},
		{kind: cR1, prompt: "w", native: "н", reveal: true, example: "ex"},
		{kind: cR1, prompt: "w", native: "н", done: true, wasOk: true},
		{kind: cR1, prompt: "w", native: "н", done: true},
		{kind: cR2, prompt: "н", choices: []string{"a", "b", "c", "d"}, answer: 1},
		{kind: cR2, prompt: "н", choices: []string{"a", "b", "c", "d"}, answer: 1, done: true},
		{kind: cR3, prompt: "н", attempts: 3},
		{kind: cR3, prompt: "н", done: true, wasOk: true},
		{kind: cL1, prompt: "w", native: "н", tr: "t", example: "ex"},
		{kind: cL1b, prompt: "w", native: "н", done: true, wasOk: true},
		{kind: cL2, prompt: "w", choices: []string{"a", "b"}, answer: 0, done: true},
	}
	for i, c := range cards {
		m.sess.cur = c
		m.sess.typing = c.kind == cR3 && !c.done
		m.sess.input = "te"
		if out := m.viewCard(); out == "" {
			t.Fatalf("card %d renders empty", i)
		}
	}
	m.sess.cur = nil
	for _, s := range []screen{sPicker, sLearn, sSession, sVocab, sWord, sStats, sSync, sAdd, sMenu, sSettings, sImport, sAddCat} {
		m.screen = s
		if out := m.View(); out == "" {
			t.Fatalf("screen %d renders empty", s)
		}
	}
}

func TestWordReturnEsc(t *testing.T) {
	m := testModel(t)
	m.screen = sWord
	m.word = &rwcore.Word{Text: "w"}
	m.wordReturn = sSession
	m.sess.cur = &card{kind: cR1, word: "w"}
	m = press(t, m, "esc")
	if m.screen != sSession {
		t.Fatal("esc from word must return to session")
	}
	if m.sess.cur == nil {
		t.Fatal("session card must survive")
	}
}

func TestStatsEsc(t *testing.T) {
	m := testModel(t)
	m.screen = sStats
	m = press(t, m, "esc")
	if m.screen != sLearn {
		t.Fatal("esc from stats must go learn")
	}
}

func TestQuitWriteConfirm(t *testing.T) {
	m := testModel(t)
	m.screen = sLearn
	m.q.Items = []queue.Intent{{Op: "grade", Word: "w", Mode: "rec", Result: "ok"}}
	m = press(t, m, "q")
	if m.ov != oQuit {
		t.Fatal("q must open guard")
	}
	m = press(t, m, "w")
	if m.ov != oConfirm || m.pending != "write-quit" {
		t.Fatal("w must ask confirm first")
	}
}

func TestLayoutFitsWidth(t *testing.T) {
	for _, w := range []int{40, 80, 200} {
		m := testModel(t)
		m.width = w
		m.screen = sPicker
		m.apps = []rwcore.App{
			{N: 1, ID: "en", SizeBytes: 60 << 20, MtimeSecs: 1789238645},
			{N: 2, ID: "es", SizeBytes: 19 << 20, MtimeSecs: 1789246418},
		}
		m.appMeta = map[string]appMeta{"en": {words: 11302, due: 68}, "es": {words: 7438, due: 181}}
		for _, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d: line overflows (%d): %q", w, got, line)
			}
		}
		m.screen = sLearn
		m.appID = "es"
		m.menuIdx = 0
		m.cats = []rwcore.Category{{ID: "custom", NameEn: strp("My words"), Words: 252}}
		m.catSel = map[string]bool{"custom": true}
		for _, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d learn overflows (%d): %q", w, got, line)
			}
		}
	}
}

func TestLayoutCentered(t *testing.T) {
	m := testModel(t)
	m.width = 200
	m.screen = sLearn
	m.appID = "es"
	m.menuIdx = 0
	out := m.View()
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "─") {
			continue
		}
		lead := len(line) - len(trimmed)
		if lead < 40 {
			t.Fatalf("wide terminal: content not centered, lead=%d: %q", lead, line)
		}
		break
	}
}

func strp(s string) *string { return &s }

func TestQuitGuard(t *testing.T) {
	m := testModel(t)
	m.screen = sLearn
	m = press(t, m, "q")
	if m.ov != oQuit {
		t.Fatal("q must open quit guard")
	}
	m = press(t, m, "esc")
	if m.ov != oNone {
		t.Fatal("esc must close guard")
	}
}

func TestEmptyStatesNoPanic(t *testing.T) {
	m := testModel(t)
	m.screen = sPicker
	m.apps = nil
	for _, k := range []string{"j", "l", "enter", " "} {
		m = press(t, m, k)
		_ = m.View()
	}
	if m.appIdx != 0 {
		t.Fatalf("appIdx must stay 0 on empty picker, got %d", m.appIdx)
	}
	m.screen = sVocab
	m.vocabMode = 0
	m.cats = nil
	for _, k := range []string{"j", "k", " ", "enter", "X", "R", "D"} {
		m = press(t, m, k)
		_ = m.View()
	}
	if m.vocabIdx != 0 {
		t.Fatalf("vocabIdx must stay 0 on empty cats, got %d", m.vocabIdx)
	}
	m.vocabMode = 1
	m.vocabWords = nil
	for _, k := range []string{"j", "k", "enter"} {
		m = press(t, m, k)
		_ = m.View()
	}
	if m.menuIdx != 0 {
		t.Fatalf("menuIdx must stay 0 on empty word list, got %d", m.menuIdx)
	}
}

func TestLearnEnterClampsMode(t *testing.T) {
	m := testModel(t)
	m.screen = sLearn
	m.menuIdx = 57
	m = press(t, m, "enter")
	if m.screen != sSession {
		t.Fatal("enter must start session")
	}
	if m.sess.mode < 0 || m.sess.mode > 2 {
		t.Fatalf("session mode must be 0-2, got %d", m.sess.mode)
	}
}

func TestOnWriteConsumesAppliedPrefix(t *testing.T) {
	mk := func(t *testing.T) Model {
		m := testModel(t)
		for _, w := range []string{"a", "b", "c"} {
			m.enqueue(queue.Intent{Op: "grade", Word: w, Mode: "rec", Result: "ok"})
		}
		return m
	}
	m := mk(t)
	next, _ := m.onWrite(writeMsg{written: 1, consumed: 1, err: errTest})
	m = next.(Model)
	if len(m.q.Items) != 2 || m.q.Items[0].Word != "b" {
		t.Fatalf("partial error must keep unapplied tail: %+v", m.q.Items)
	}
	m = mk(t)
	next, _ = m.onWrite(writeMsg{written: 1, consumed: 1, dirty: true})
	m = next.(Model)
	if len(m.q.Items) != 2 || m.screen != sSync {
		t.Fatalf("dirty must keep unapplied tail and go sync: %+v", m.q.Items)
	}
	m = mk(t)
	next, _ = m.onWrite(writeMsg{written: 3, consumed: 3})
	m = next.(Model)
	if len(m.q.Items) != 0 {
		t.Fatalf("success must drain queue: %+v", m.q.Items)
	}
}

func TestPullChainsSyncReload(t *testing.T) {
	m := testModel(t)
	m.screen = sSync
	next, cmd := m.Update(replayMsg{out: "pulled"})
	m = next.(Model)
	if cmd == nil || m.loading != "sync" || m.notice != "pulled" {
		t.Fatal("pull must chain a sync reload")
	}
	next, cmd = m.Update(replayMsg{out: "plan"})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("plain replay must not chain commands")
	}
}

func TestCatStatsFillsPct(t *testing.T) {
	m := testModel(t)
	next, _ := m.onCatStats(catStatsMsg{stats: []rwcore.CatStat{
		{Category: "food", Total: 4, Started: 1},
		{Category: "empty", Total: 0, Started: 0},
	}})
	m = next.(Model)
	if m.catPct["food"] != "25%" || m.catPct["empty"] != "—" {
		t.Fatalf("bad pct: %v", m.catPct)
	}
}

func TestReviewCardUsesPool(t *testing.T) {
	m := testModel(t)
	next, _ := m.onWords(wordsMsg{key: "pool", words: []rwcore.Word{
		{ID: 1, Text: "el pan", Translations: map[string]string{"RUS": "хлеб"}},
	}})
	m = next.(Model)
	c := m.reviewCard("el pan", 1)
	if c.done || c.native != "хлеб" {
		t.Fatalf("card must come from pool without backend: %+v", c)
	}
	if _, ok := m.poolWord("missing"); ok {
		t.Fatal("unknown word must miss pool index")
	}
}
