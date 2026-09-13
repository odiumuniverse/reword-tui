package ui

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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
		if out, _ := m.viewCard(76, 10); out == "" {
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
	if m.wlIdx != 0 {
		t.Fatalf("wlIdx must stay 0 on empty word list, got %d", m.wlIdx)
	}
}

func TestParseExamplesStripsHashes(t *testing.T) {
	got := parseExamples(`[{"o":"Como #pescado#","t":"ем #рыбу#"}]`)
	if len(got) != 1 || strings.Contains(got[0], "#") {
		t.Fatalf("hashes must go: %v", got)
	}
}

func TestStageRamp(t *testing.T) {
	if len(stageRamp) != 8 {
		t.Fatalf("ramp must cover S0-S7, got %d", len(stageRamp))
	}
	for i, s := range stageRamp {
		if s.glyph == "" {
			t.Fatalf("step %d has no glyph", i)
		}
	}
	if stageRamp[0].glyph != "○" || stageRamp[7].glyph != "✦" {
		t.Fatal("ramp must run ○ to ✦")
	}
	if got := stageMark(-3); got != stageMark(0) {
		t.Fatal("negative step must clamp to S0")
	}
	if got := stageMark(99); got != stageMark(7) {
		t.Fatal("large step must clamp to S7")
	}
	if wordStage(2, 5) != stageMark(5) || wordStage(6, 1) != stageMark(6) {
		t.Fatal("word stage must follow the stronger side")
	}
}

func TestWordListCursorIsolated(t *testing.T) {
	m := testModel(t)
	m.screen = sVocab
	m.vocabMode = 1
	m.vocabWords = []rwcore.Word{{Text: "a"}, {Text: "b"}}
	m = press(t, m, "j")
	if m.wlIdx != 1 || m.menuIdx != 0 {
		t.Fatalf("word-list cursor must not touch learn cursor: wl=%d menu=%d", m.wlIdx, m.menuIdx)
	}
	m = press(t, m, "esc")
	m = press(t, m, "esc")
	if m.screen != sLearn || m.menuIdx != 0 {
		t.Fatal("esc must return to learn with cursor 0")
	}
	m = press(t, m, "enter")
	if m.sess.mode != 0 {
		t.Fatalf("session must start in mode 0, got %d", m.sess.mode)
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

func TestOnboardingPersists(t *testing.T) {
	m := testModel(t)
	g := int64(10)
	m.today = &rwcore.Today{Goal: &g}
	m.cats = []rwcore.Category{{ID: "food", Selected: true, Words: 5}}
	m.catSel["food"] = true
	m.screen = sVocab
	m.vocabMode = 0
	m.obStep = 1

	m = press(t, m, "enter")
	if m.obStep != 0 {
		t.Errorf("obStep=%d after finishing onboarding, want 0", m.obStep)
	}
	if !m.prefs.Onboarded {
		t.Error("in-memory prefs.Onboarded=false after finishing onboarding")
	}

	m.screen = sVocab
	m = press(t, m, "enter")
	if m.vocabMode != 1 {
		t.Error("enter on category did not open word list: onboarding branch fired again")
	}

	m.screen = sSettings
	m.setIdx = 0
	m = press(t, m, "enter")
	if !LoadPrefs().Onboarded {
		t.Error("settings toggle overwrote onboarded=true on disk with false")
	}
}

func proofWord(id int64, text, rus string, lvl int64) rwcore.Word {
	return rwcore.Word{
		ID:           id,
		Text:         text,
		Translations: map[string]string{"RUS": rus},
		Recognition:  rwcore.ModeState{Level: lvl},
		Reproduction: rwcore.ModeState{Level: lvl},
	}
}

func TestLearnProgressBarNoPanic(t *testing.T) {
	m := testModel(t)
	pool := []rwcore.Word{
		proofWord(1, "uno", "один", 1),
		proofWord(2, "dos", "два", 0),
		proofWord(3, "tres", "три", 0),
		proofWord(4, "cuatro", "четыре", 0),
	}
	m.pool = pool
	m.poolIdx = map[string]int{}
	for i, w := range pool {
		m.poolIdx[w.Text] = i
	}
	m.screen = sSession
	m.sess = session{mode: 1, learn: []rwcore.Word{pool[0]}, lpos: 1, started: true}
	m.sess.cur = m.learnCard(pool[0])
	if m.sess.cur.kind != cL1b {
		t.Fatalf("setup: want cL1b card, got %v", m.sess.cur.kind)
	}

	m = press(t, m, "l")
	m = press(t, m, "1")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("View panicked with done > total: %v", r)
		}
	}()
	_ = m.View()
}

func TestPullClearsDirtyError(t *testing.T) {
	m := testModel(t)
	m.err = "DIRTY_SOURCE: pull first"
	next, _ := m.Update(replayMsg{out: "pulled"})
	m = next.(Model)
	next, _ = m.Update(syncMsg{rows: []rwcore.StatusRow{{App: "es", State: "clean"}}})
	m = next.(Model)
	if m.err != "" {
		t.Errorf("stale error still shown after successful pull + clean sync: %q", m.err)
	}
}

var sgrRe = regexp.MustCompile("\x1b\\[([0-9;]+)m")

func sgrIndexes(s string) []string {
	var out []string
	for _, m := range sgrRe.FindAllStringSubmatch(s, -1) {
		for _, p := range strings.Split(m[1], ";") {
			n, err := strconv.Atoi(p)
			if err != nil {
				continue
			}
			switch {
			case n >= 30 && n <= 37:
				out = append(out, strconv.Itoa(n-30))
			case n >= 90 && n <= 97:
				out = append(out, strconv.Itoa(n-90+8))
			}
		}
	}
	return out
}

func withProfile(p termenv.Profile, fn func()) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	lipgloss.SetColorProfile(p)
	fn()
}

func TestRoleANSIDegradation(t *testing.T) {
	withProfile(termenv.ANSI, func() {
		for _, r := range roleStyles {
			got := sgrIndexes(r.style.Render("x"))
			if len(got) != 1 || got[0] != r.want {
				t.Errorf("role %s degrades to %v, want ANSI %s", r.name, got, r.want)
			}
		}
	})
}

func TestElementRoles(t *testing.T) {
	withProfile(termenv.TrueColor, func() {
		m := testModel(t)
		m.screen = sWord
		m.word = &rwcore.Word{Text: "w", Translations: map[string]string{"RUS": "родной"}}
		out := m.View()
		if !strings.Contains(out, content.Render("родной")) {
			t.Error("native translation must use the content role")
		}
		if strings.Contains(out, okStyle.Render("родной")) {
			t.Error("native translation must not use the ok role")
		}

		m = testModel(t)
		m.screen = sLearn
		m.setNotice("queue empty", false)
		out = m.View()
		if !strings.Contains(out, fg.Render("queue empty")) {
			t.Error("info notice must be neutral")
		}
		if strings.Contains(out, attn.Render("queue empty")) {
			t.Error("info notice must not warn")
		}
		m.setNotice("You have 2 attempts.", true)
		if out = m.View(); !strings.Contains(out, attn.Render("You have 2 attempts.")) {
			t.Error("attempts notice must use the warning role")
		}
	})
}

func TestNoColorContract(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	o := termenv.NewOutput(io.Discard)
	if got := o.EnvColorProfile(); got != termenv.Ascii {
		t.Fatalf("NO_COLOR=1 must degrade to Ascii, got %d", got)
	}
}

func longWord() rwcore.Word {
	return rwcore.Word{
		ID:   7,
		Text: "supercalifragilisticexpialidociousness",
		Translations: map[string]string{
			"RUS": "очень длинный перевод который точно не влезет в узкую колонку терминала",
			"ENG": "a very long translation that will not fit a narrow terminal column",
		},
		Examples: map[string]string{
			"RUS": `[{"o":"A very long example sentence that keeps going and going","t":"Очень длинный #пример# который всё продолжается"}]`,
		},
		Recognition:  rwcore.ModeState{Level: 4, Step: 4},
		Reproduction: rwcore.ModeState{Level: 2, Step: 2},
	}
}

func fitModel(t *testing.T) Model {
	t.Helper()
	m := testModel(t)
	m.appID = "es"
	g := int64(30)
	m.today = &rwcore.Today{Learned: 10, Goal: &g, StreakCur: 2, StreakBest: 6, ActiveDates: []string{"2026-09-13"}}
	m.stats = &rwcore.Stats{Settings: rwcore.Settings{DailyGoal: strp("30"), NativeLanguage: strp("RUS")}}
	m.due = []rwcore.DueItem{{Word: "x", Modes: []int64{1}, OverdueSecs: 100}}
	m.cats = []rwcore.Category{{ID: "custom", NameEn: strp("My words"), Words: 5}, {ID: "averylongcategorynamewithoutanyspacesinit", Words: 3}}
	m.catSel = map[string]bool{"custom": true}
	m.catPct = map[string]string{"custom": "12%"}
	m.pool = []rwcore.Word{longWord()}
	m.poolIdx = map[string]int{longWord().Text: 0}
	m.vocabWords = []rwcore.Word{longWord()}
	m.word = &rwcore.Word{Text: longWord().Text, Translations: longWord().Translations, Examples: longWord().Examples,
		Recognition: longWord().Recognition, Reproduction: longWord().Reproduction}
	m.wordLog = []rwcore.LogEntry{{Date: "2026-09-13", Mode: 1, Queue: 2}}
	m.apps = []rwcore.App{{N: 1, ID: "es"}}
	m.appMeta = map[string]appMeta{"es": {words: 100, due: 5}}
	m.syncRows = []rwcore.StatusRow{{App: "es", State: "dirty"}}
	m.replayPlan = "a very long replay plan line that should be truncated to fit the column width on narrow screens"
	m.q.Items = []queue.Intent{{Op: "grade", Word: "w", Mode: "rec", Result: "ok"}}
	m.orphans = []rwcore.OrphanEntry{{App: "es"}}
	m.sess = session{mode: 2, learn: []rwcore.Word{longWord()}, lpos: 1, started: true}
	m.sess.cur = m.learnCard(longWord())
	m.confirmT = "Remove 'averylongcategorynamewithoutanyspacesinit' with all of its 99999 words?"
	m.goalInput = "123456789012345678901234567890"
	m.impF = []string{"averylongfilenamewithoutanyspaces.csv", "cat"}
	m.addF = []string{"supercalifragilisticexpialidociousness", "[tr]", "RUS-translation", "ENG-translation"}
	m.search = "supercalifragilistic"
	m.searchOn = true
	return m
}

func TestLayoutFitsEverywhere(t *testing.T) {
	screens := []struct {
		name   string
		screen screen
		mode   int
		ov     overlay
		typing bool
	}{
		{"picker", sPicker, 0, oNone, false},
		{"learn", sLearn, 0, oNone, false},
		{"session", sSession, 0, oNone, false},
		{"session-typing", sSession, 0, oNone, true},
		{"vocab-cats", sVocab, 0, oNone, false},
		{"vocab-words", sVocab, 1, oNone, false},
		{"word", sWord, 0, oNone, false},
		{"stats", sStats, 0, oNone, false},
		{"sync", sSync, 0, oNone, false},
		{"menu", sMenu, 0, oNone, false},
		{"settings", sSettings, 0, oNone, false},
		{"import", sImport, 0, oNone, false},
		{"add", sAdd, 0, oNone, false},
		{"addcat", sAddCat, 0, oNone, false},
		{"ov-quit", sLearn, 0, oQuit, false},
		{"ov-confirm", sVocab, 0, oConfirm, false},
		{"ov-help", sSession, 0, oHelp, false},
		{"ov-goal", sLearn, 0, oGoal, false},
		{"ov-about", sLearn, 0, oAbout, false},
		{"ov-orphans", sSync, 0, oOrphans, false},
	}
	for _, w := range []int{40, 60, 80, 120, 200} {
		for _, h := range []int{12, 24, 50} {
			for _, sc := range screens {
				m := fitModel(t)
				m.width, m.height = w, h
				m.screen, m.vocabMode, m.ov = sc.screen, sc.mode, sc.ov
				if sc.typing && m.sess.cur != nil {
					m.sess.typing = true
					m.sess.input = "supercalifragilisticexpialidocious input typing"
				}
				out := m.View()
				if out == "" {
					t.Fatalf("%s %dx%d renders empty", sc.name, w, h)
				}
				for _, line := range strings.Split(out, "\n") {
					if got := lipgloss.Width(line); got > w {
						t.Fatalf("%s %dx%d overflows (%d): %q", sc.name, w, h, got, line)
					}
				}
			}
		}
	}
}

func measureView(out string) (lines, width int) {
	ls := strings.Split(out, "\n")
	for _, l := range ls {
		width = max(width, lipgloss.Width(l))
	}
	return len(ls), width
}

var viewportScreens = []struct {
	name   string
	screen screen
	mode   int
	ov     overlay
	typing bool
}{
	{"picker", sPicker, 0, oNone, false},
	{"learn", sLearn, 0, oNone, false},
	{"session", sSession, 0, oNone, false},
	{"session-typing", sSession, 0, oNone, true},
	{"vocab-cats", sVocab, 0, oNone, false},
	{"vocab-words", sVocab, 1, oNone, false},
	{"word", sWord, 0, oNone, false},
	{"stats", sStats, 0, oNone, false},
	{"sync", sSync, 0, oNone, false},
	{"menu", sMenu, 0, oNone, false},
	{"settings", sSettings, 0, oNone, false},
	{"import", sImport, 0, oNone, false},
	{"add", sAdd, 0, oNone, false},
	{"addcat", sAddCat, 0, oNone, false},
	{"ov-quit", sLearn, 0, oQuit, false},
	{"ov-confirm", sVocab, 0, oConfirm, false},
	{"ov-help", sSession, 0, oHelp, false},
	{"ov-goal", sLearn, 0, oGoal, false},
	{"ov-about", sLearn, 0, oAbout, false},
	{"ov-orphans", sSync, 0, oOrphans, false},
}

func TestLayoutFitsViewport(t *testing.T) {
	var fails []string
	for _, w := range []int{30, 40, 60, 80} {
		for _, h := range []int{8, 12, 16, 24} {
			for _, sc := range viewportScreens {
				m := fitModel(t)
				m.width, m.height = w, h
				m.screen, m.vocabMode, m.ov = sc.screen, sc.mode, sc.ov
				if sc.typing && m.sess.cur != nil {
					m.sess.typing = true
					m.sess.input = "supercalifragilisticexpialidocious input typing"
				}
				n, mw := measureView(m.View())
				if n > h || mw > w {
					fails = append(fails, fmt.Sprintf("%-14s %3dx%-2d lines=%d width=%d", sc.name, w, h, n, mw))
				}
			}
		}
	}
	for _, f := range fails {
		t.Log(f)
	}
	if len(fails) > 0 {
		t.Errorf("%d screen/size combos overflow the viewport", len(fails))
	}
}

func TestCardKindsFitViewport(t *testing.T) {
	long := longWord()
	choices := []string{
		"очень длинный вариант ответа номер один для узкого экрана",
		"второй очень длинный вариант ответа",
		"третий",
		"четвёртый вариант ответа тоже довольно длинный",
	}
	nat := "очень длинный перевод который точно не влезет в узкую колонку терминала"
	ex := "A very long example sentence that keeps going — Очень длинный пример который всё продолжается"
	cards := []struct {
		name string
		c    card
	}{
		{"R1-reveal", card{kind: cR1, word: long.Text, prompt: long.Text, native: nat, tr: "[tr]", example: ex, reveal: true, mode: "rec"}},
		{"R1-done", card{kind: cR1, word: long.Text, prompt: long.Text, native: nat, example: ex, reveal: true, done: true, mode: "rec"}},
		{"R2", card{kind: cR2, word: long.Text, prompt: nat, choices: choices, answer: 1, mode: "rep"}},
		{"R2-done-wrong", card{kind: cR2, word: long.Text, prompt: nat, choices: choices, answer: 1, done: true, mode: "rep"}},
		{"R3-typing", card{kind: cR3, word: long.Text, prompt: nat, expected: []string{long.Text}, attempts: 3, mode: "rep"}},
		{"L1", card{kind: cL1, word: long.Text, prompt: long.Text, native: nat, example: ex}},
		{"L1b", card{kind: cL1b, word: long.Text, prompt: long.Text, native: nat, example: ex}},
		{"L2-done-wrong", card{kind: cL2, word: long.Text, prompt: long.Text, choices: choices, answer: 0, done: true}},
	}
	var fails []string
	for _, zen := range []bool{false, true} {
		for _, w := range []int{30, 40, 60, 80} {
			for _, h := range []int{12, 16, 24} {
				for _, cc := range cards {
					m := fitModel(t)
					m.width, m.height = w, h
					m.screen, m.ov = sSession, oNone
					c := cc.c
					m.sess.cur = &c
					m.sess.zen = zen
					if c.kind == cR3 {
						m.sess.typing = true
						m.sess.input = "supercalifragilisticexpialidocious typed"
					}
					n, mw := measureView(m.View())
					if n > h || mw > w {
						fails = append(fails, fmt.Sprintf("zen=%-5v %-14s %3dx%-2d lines=%d width=%d", zen, cc.name, w, h, n, mw))
					}
				}
			}
		}
	}
	for _, f := range fails {
		t.Log(f)
	}
	if len(fails) > 0 {
		t.Errorf("%d card/size combos overflow the viewport", len(fails))
	}
}

func TestAsciiNoEscapes(t *testing.T) {
	withProfile(termenv.Ascii, func() {
		for _, sc := range viewportScreens {
			m := fitModel(t)
			m.width, m.height = 80, 24
			m.screen, m.vocabMode, m.ov = sc.screen, sc.mode, sc.ov
			if out := m.View(); strings.Contains(out, "\x1b[") {
				i := strings.Index(out, "\x1b[")
				t.Errorf("%s: ANSI escape under Ascii profile near %q", sc.name, out[max(0, i-20):min(len(out), i+20)])
			}
		}
	})
}

func TestFrameBoxModel(t *testing.T) {
	if _, w := measureView(abox.Width(20).Render("x")); w != 22 {
		t.Fatalf("abox.Width(20) outer=%d, want 22 (padding inside, border outside)", w)
	}
	m := fitModel(t)
	m.width, m.height = 80, 24
	vc, _ := m.viewCard(76, 10)
	if _, w := measureView(vc); w != 76 {
		t.Fatalf("viewCard(cw=76) outer=%d, want 76", w)
	}
}

func TestScrollFit(t *testing.T) {
	m := testModel(t)
	m.screen = sStats
	s := "l0\nl1\nl2\nl3\nl4"
	if got := m.scrollFit(s, 10); got != s {
		t.Fatal("short content must pass through")
	}
	got := m.scrollFit(s, 3)
	if !strings.Contains(got, "l0") || !strings.Contains(got, "↓ more") || strings.Contains(got, "l4") {
		t.Fatalf("top window must show head with marker: %q", got)
	}
	m.scrOff[sStats] = 2
	got = m.scrollFit(s, 3)
	if !strings.Contains(got, "↑ more") || !strings.Contains(got, "l4") {
		t.Fatalf("scrolled window must show tail with marker: %q", got)
	}
	m.scrOff[sStats] = 0
	m = press(t, m, "j")
	if m.scrOff[sStats] != 1 {
		t.Fatal("j must scroll down")
	}
	m = press(t, m, "k")
	if m.scrOff[sStats] != 0 {
		t.Fatal("k must scroll up")
	}
	// Clamp: offset must not coast past the content end.
	m.scrOff[sStats] = 29
	m = press(t, m, "j")
	if m.scrOff[sStats] != 2 {
		t.Fatalf("j must stop at content end, got %d", m.scrOff[sStats])
	}
}

func TestEdgeTinySizes(t *testing.T) {
	var fails []string
	for _, w := range []int{1, 5, 10, 23, 24, 25, 30} {
		for _, h := range []int{1, 3, 9, 10, 11, 12} {
			for _, sc := range viewportScreens {
				m := fitModel(t)
				m.width, m.height = w, h
				m.screen, m.vocabMode, m.ov = sc.screen, sc.mode, sc.ov
				n, mw := measureView(m.View())
				if n > h || mw > w {
					fails = append(fails, fmt.Sprintf("%-14s %2dx%-2d lines=%d width=%d", sc.name, w, h, n, mw))
				}
			}
		}
	}
	for _, f := range fails {
		t.Log(f)
	}
	if len(fails) > 0 {
		t.Errorf("%d tiny-size combos overflow", len(fails))
	}
}

func TestEdgeFooterSurvives(t *testing.T) {
	var fails []string
	for _, sc := range viewportScreens {
		m := fitModel(t)
		m.screen, m.vocabMode, m.ov = sc.screen, sc.mode, sc.ov
		m.width, m.height = 80, 50
		ls := strings.Split(m.View(), "\n")
		want := ls[len(ls)-1]
		for _, h := range []int{10, 11, 12, 14, 16, 24} {
			m.height = h
			ls := strings.Split(m.View(), "\n")
			if got := ls[len(ls)-1]; got != want {
				fails = append(fails, fmt.Sprintf("%-14s 80x%-2d last line %q, tall view has %q", sc.name, h, got, want))
			}
		}
	}
	for _, f := range fails {
		t.Log(f)
	}
	if len(fails) > 0 {
		t.Errorf("%d footer losses", len(fails))
	}
}

func TestEdgeCursorVisibleAfterCrop(t *testing.T) {
	type tc struct {
		name  string
		setup func(m *Model)
	}
	cases := []tc{
		{"settings last row", func(m *Model) { m.screen, m.ov, m.searchOn = sSettings, oNone, false; m.setIdx = 7 }},
		{"add enroll row", func(m *Model) { m.screen, m.ov, m.searchOn = sAdd, oNone, false; m.addIdx = 4 }},
		{"learn mixed row", func(m *Model) { m.screen, m.ov, m.searchOn = sLearn, oNone, false; m.menuIdx = 2 }},
		{"menu about row", func(m *Model) { m.screen, m.ov, m.searchOn = sMenu, oNone, false; m.menuIdx = 4 }},
		{"picker 5th app", func(m *Model) {
			m.screen, m.ov, m.searchOn = sPicker, oNone, false
			m.apps = nil
			for i := 1; i <= 5; i++ {
				id := fmt.Sprintf("a%d", i)
				m.apps = append(m.apps, rwcore.App{N: i, ID: id})
				m.appMeta[id] = appMeta{words: 10, due: 1}
			}
			m.appIdx = 4
		}},
	}
	var fails []string
	for _, c := range cases {
		for _, h := range []int{10, 11, 12, 14, 16} {
			m := fitModel(t)
			c.setup(&m)
			m.width, m.height = 80, h
			if !strings.Contains(m.View(), "▸") {
				fails = append(fails, fmt.Sprintf("%-18s 80x%-2d cursor row cropped away", c.name, h))
			}
		}
	}
	for _, f := range fails {
		t.Log(f)
	}
	if len(fails) > 0 {
		t.Errorf("%d cases hide the selected row", len(fails))
	}
}

func TestEdgeCardAndOverlayContentAfterCrop(t *testing.T) {
	long := longWord()
	choices := []string{"uno largo", "dos largo", "tres largo", "cuatro largo"}
	var fails []string
	for _, w := range []int{40, 80} {
		for _, h := range []int{10, 11, 12, 14, 16} {
			m := fitModel(t)
			m.width, m.height = w, h
			m.screen, m.ov, m.searchOn = sSession, oNone, false
			c := card{kind: cR2, word: long.Text, prompt: "перевод", choices: choices, answer: 1, mode: "rep"}
			m.sess.cur = &c
			out := m.View()
			for i := 1; i <= 4; i++ {
				if !strings.Contains(out, fmt.Sprintf("%d  ", i)) {
					fails = append(fails, fmt.Sprintf("R2 %dx%-2d choice %d cropped", w, h, i))
				}
			}
			if !strings.Contains(out, "["+c.hint()+"]") {
				fails = append(fails, fmt.Sprintf("R2 %dx%-2d in-card hint cropped", w, h))
			}
			if strings.Count(out, "┌") != strings.Count(out, "└") {
				fails = append(fails, fmt.Sprintf("R2 %dx%-2d card frame cut (┌=%d └=%d)", w, h, strings.Count(out, "┌"), strings.Count(out, "└")))
			}
			for _, ov := range []struct {
				name string
				o    overlay
				need string
			}{
				{"confirm", oConfirm, "[y] yes"},
				{"quit", oQuit, "[q] quit anyway"},
				{"goal", oGoal, "[enter] save"},
			} {
				m := fitModel(t)
				m.width, m.height = w, h
				m.screen, m.ov, m.searchOn = sLearn, ov.o, false
				out := m.View()
				if !strings.Contains(out, ov.need) {
					fails = append(fails, fmt.Sprintf("%-7s %dx%-2d action line %q cropped", ov.name, w, h, ov.need))
				}
				if strings.Count(out, "┌") != strings.Count(out, "└") {
					fails = append(fails, fmt.Sprintf("%-7s %dx%-2d frame cut", ov.name, w, h))
				}
			}
		}
	}
	for _, f := range fails {
		t.Log(f)
	}
	if len(fails) > 0 {
		t.Errorf("%d crops remove content the user must see", len(fails))
	}
}

func TestPickerCardsEqualHeight(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 80, 24
	m.screen = sPicker
	m.apps = []rwcore.App{
		{N: 1, ID: "en", SizeBytes: 60 << 20, MtimeSecs: 1789238645},
		{N: 2, ID: "es", SizeBytes: 19 << 20, MtimeSecs: 1789246418},
	}
	m.appMeta = map[string]appMeta{"en": {words: 11302, due: 68}, "es": {words: 7438, due: 181}}
	out := m.View()
	if !strings.Contains(out, "11302 wds") {
		t.Error("long card text must not wrap mid-phrase")
	}
	if tops, bottoms := strings.Count(out, "┌"), strings.Count(out, "└"); tops != 2 || bottoms != 2 {
		t.Fatalf("want 2 intact cards, got tops=%d bottoms=%d", tops, bottoms)
	}
	// Both cards must start at the same column (equal width) and the row
	// must be centered in the 76-cell column.
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "┌") {
			lead := len(ln) - len(strings.TrimLeft(ln, " "))
			if lead < 1 {
				t.Fatalf("picker row must be centered, no lead: %q", ln)
			}
			break
		}
	}
}

func TestScrollResetsOnContentLoad(t *testing.T) {
	m := testModel(t)
	m.scrOff[sWord] = 5
	m.scrOff[sStats] = 5
	m.scrOff[sSync] = 5
	next, _ := m.onWord(wordMsg{word: rwcore.Word{Text: "w"}})
	m = next.(Model)
	if m.scrOff[sWord] != 0 {
		t.Fatal("opening a word must reset its scroll")
	}
	next, _ = m.onStats(statsMsg{})
	m = next.(Model)
	if m.scrOff[sStats] != 0 {
		t.Fatal("fresh stats must reset its scroll")
	}
	next, _ = m.onSync(syncMsg{})
	m = next.(Model)
	if m.scrOff[sSync] != 0 {
		t.Fatal("fresh sync must reset its scroll")
	}
}
