package ui

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
	m.q = queue.Load(m.appQueuePath())
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
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(t *testing.T, m Model, k string) Model {
	t.Helper()
	next, _ := m.Update(key(k))
	return next.(Model)
}

func graded(t *testing.T, m Model, verdict string) Model {
	t.Helper()
	c := m.sess.cur
	next, _ := m.Update(checkMsg{wordID: c.wordID, mode: c.mode, res: rwcore.Check{Verdict: verdict, Accepted: verdict != "wrong"}})
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

func dealt(t *testing.T, m Model, rc rwcore.Card) Model {
	t.Helper()
	m.screen = sSession
	next, _ := m.onDeal(dealMsg{mode: m.sess.mode, deal: rwcore.Deal{
		Card: &rc,
		Day:  rwcore.Day{Due: 3, Learning: 2},
		Now:  1000,
	}})
	return next.(Model)
}

func rcard(id int64, text, rus string, side, status int64) rwcore.Card {
	lvl := rwcore.ModeState{Level: status}
	return rwcore.Card{
		Word: rwcore.Word{ID: id, Text: text, Translations: map[string]string{"RUS": rus},
			Recognition: lvl, Reproduction: lvl},
		Side:   side,
		Status: status,
		Queue:  status,
	}
}

func answered(t *testing.T, m Model) queue.Intent {
	t.Helper()
	if len(m.q.Items) == 0 {
		t.Fatal("no answer queued")
	}
	return m.q.Items[len(m.q.Items)-1]
}

func TestR1Flow(t *testing.T) {
	m := dealt(t, testModel(t), rcard(1, "el pan", "хлеб", 1, 2))
	m = press(t, m, " ")
	if !m.sess.cur.reveal {
		t.Fatal("space must reveal")
	}
	next, cmd := m.Update(key("g"))
	m = next.(Model)
	it := answered(t, m)
	if it.Op != "answer" || it.Mode != "rec" || !it.Positive || it.ID != 1 || it.TS == 0 {
		t.Fatalf("got it must queue the left answer on the card's side: %+v", it)
	}
	if m.sess.ok != 1 {
		t.Fatal("got it must count as correct")
	}
	if m.sess.cur != nil || !m.sess.dealing || cmd == nil {
		t.Fatal("an answer must ask rwcore for the next card")
	}
}

func TestR1Missed(t *testing.T) {
	m := dealt(t, testModel(t), rcard(1, "el pan", "хлеб", 2, 2))
	m = press(t, m, "m")
	if it := answered(t, m); it.Positive || it.Mode != "rep" || m.sess.fail != 1 {
		t.Fatalf("missed it must queue the right answer: %+v", it)
	}
}

func TestNewWordAnswers(t *testing.T) {
	m := testModel(t)
	m.sess.mode = modeLearn
	m = dealt(t, m, rcard(4, "a", "а", 1, 0))
	m = press(t, m, "l")
	if it := answered(t, m); it.Positive {
		t.Fatalf("l on a new word starts learning, the right answer: %+v", it)
	}
	if m.sess.cur != nil || m.sess.ok != 0 {
		t.Fatal("a learning decision moves on and is not a correct answer")
	}
	m = press(t, m, "esc")
	if m.screen != sLearn {
		t.Fatal("esc must leave session")
	}
}

func TestCardReadsItsSide(t *testing.T) {
	prefs := DefaultPrefs()
	other := func(id int64, text string) rwcore.Word {
		return rwcore.Word{ID: id, Text: text, Translations: map[string]string{"RUS": "п-" + text}}
	}
	rc := rcard(1, "casa", "дом", 2, 2)
	rc.Keyboard, rc.Choose = true, true
	rc.Variants = []rwcore.Word{other(10, "mesa"), rc.Word, other(11, "sala"), other(12, "cama")}
	c := cardFrom(&rc, "RUS", prefs)
	if c.prompt != "дом" || c.native != "casa" || c.mode != "rep" {
		t.Fatalf("reproduction shows the translation and asks for the word: %+v", c)
	}
	if !slices.Equal(c.choices, []string{"mesa", "casa", "sala", "cama"}) || c.answer != 1 || !c.keyboard {
		t.Fatalf("its blocks answer with words: %v %d %v", c.choices, c.answer, c.keyboard)
	}
	rc.Side = 1
	rc.Word.Translations = map[string]string{"RUS": "дом, жилище"}
	rc.Variants[1] = rc.Word
	c = cardFrom(&rc, "RUS", prefs)
	if c.prompt != "casa" || c.native != "дом, жилище" || c.mode != "rec" {
		t.Fatalf("recognition shows the word: %+v", c)
	}
	if c.choices[1] != "дом, жилище" || !c.keyboard {
		t.Fatalf("its blocks answer with translations: %v %v", c.choices, c.keyboard)
	}
	if cardFrom(nil, "RUS", prefs) != nil {
		t.Fatal("no card, no layout")
	}
}

func i64p(v int64) *int64 { return &v }

func TestTypedFlow(t *testing.T) {
	rc := rcard(1, "el pan", "хлеб", 2, 2)
	rc.Keyboard = true
	m := dealt(t, testModel(t), rc)
	m = press(t, m, "i")
	if !m.sess.typing || m.sess.cur.pane != paneType {
		t.Fatal("i must open the keyboard block")
	}
	for _, k := range []string{"e", "l", " ", "p", "a", "n"} {
		m = press(t, m, k)
	}
	next, cmd := m.Update(key("enter"))
	if m = next.(Model); cmd == nil || !m.sess.checking {
		t.Fatal("enter must hand the answer to rwcore")
	}
	if m = press(t, m, "enter"); m.sess.cur.attempts != 3 {
		t.Fatal("a second enter must wait for the grade, not spend an attempt")
	}
	m = graded(t, m, "correct")
	if c := m.sess.cur; len(m.q.Items) != 0 || c.typed != vRight || !c.reveal || m.sess.typing || m.sess.checking {
		t.Fatalf("a right answer must open the card and leave the grade to the swipe: %+v", m.q.Items)
	}
	m = press(t, m, "left")
	if it := answered(t, m); !it.Positive {
		t.Fatalf("← after the answer means got it: %+v", it)
	}
	rc = rcard(2, "x", "y", 2, 2)
	rc.Keyboard = true
	m2 := dealt(t, testModel(t), rc)
	m2 = press(t, m2, "i")
	for range 3 {
		m2 = press(t, m2, "z")
		m2 = press(t, m2, "enter")
		m2 = graded(t, m2, "wrong")
	}
	if c := m2.sess.cur; c.typed != vWrong || !c.reveal || m2.sess.typing || len(m2.q.Items) != 0 {
		t.Fatalf("3 wrong attempts must end typing as missed, ungraded: %+v", m2.q.Items)
	}
	m2 = press(t, m2, "right")
	if it := answered(t, m2); it.Positive {
		t.Fatalf("→ after a miss means missed it: %+v", it)
	}
}

func TestPartialAnswerOpensInYellow(t *testing.T) {
	rc := rcard(1, "canción", "песня", 2, 2)
	rc.Keyboard = true
	m := dealt(t, testModel(t), rc)
	m = press(t, m, "i")
	for _, k := range []string{"c", "a", "n", "c", "i", "o", "n", "enter"} {
		m = press(t, m, k)
	}
	stale := m
	next, _ := stale.Update(checkMsg{wordID: 99, mode: "rep", res: rwcore.Check{Verdict: "correct", Accepted: true}})
	if c := next.(Model).sess.cur; c.typed != vNone || !next.(Model).sess.typing {
		t.Fatal("a grade for another card must not touch this one")
	}
	m = graded(t, m, "partial")
	if c := m.sess.cur; c.typed != vPartial || !c.reveal || m.sess.typing || c.attempts != 3 {
		t.Fatalf("a partial answer is accepted, as on the phone: %+v", c)
	}
	if out := sgrRe.ReplaceAllString(m.View(), ""); !strings.Contains(out, "✓ nearly right") {
		t.Fatalf("a partial answer must say so:\n%s", out)
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
	m.setIdx = len(settingRows) - 1
	m = press(t, m, " ")
	if !m.prefs.InvertedSwipes || !LoadPrefs().InvertedSwipes {
		t.Fatal("inverted swipes must toggle on and persist on this computer")
	}
	if len(m.q.Items) != 0 {
		t.Fatal("a setting of this computer must not go to iCloud")
	}
}

func TestSharedSettingsGoThroughTheBackup(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 40
	m.screen = sSettings
	m.synced = &rwcore.Synced{NewWords: "recognition", Learning: "reproduction", Review: "random",
		Keyboard: "foreign", Guessing: "both", ReviewFrom: "selected", MasteredDays: 60, Transcription: true}
	m = press(t, m, " ")
	m = press(t, press(t, m, "j"), " ")
	m = press(t, press(t, m, "j"), " ")
	var got []string
	for _, it := range m.q.Items {
		got = append(got, it.Op+" "+it.Name+"="+it.Value)
	}
	want := []string{
		"setting new_words_card_mode=reproduction",
		"setting word_learning_card_mode=recognition_or_reproduction",
		"setting word_review_card_mode=recognition",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("each change cycles to the phone's next value and queues for iCloud:\n%v", got)
	}
	out := sgrRe.ReplaceAllString(m.View(), "")
	for _, s := range []string{"Shared with the phone", "This computer", "New words first: translation → word", "Review mode: word → translation"} {
		if !strings.Contains(out, s) {
			t.Fatalf("settings screen lacks %q:\n%s", s, out)
		}
	}
	m.setIdx = 7
	if m = press(t, m, " "); m.prefs.ShowTranscription || m.q.Items[len(m.q.Items)-1].Value != "0" {
		t.Fatal("transcription is shared with the phone and follows it here")
	}
	m.setIdx = 6
	if m = press(t, m, "enter"); m.ov != oGoal || m.numFor != "mastered" || m.goalInput != "60" {
		t.Fatalf("the mastered interval asks for a number: ov=%v for=%q in=%q", m.ov, m.numFor, m.goalInput)
	}
	for _, k := range []string{"backspace", "backspace", "0", "enter"} {
		m = press(t, m, k)
	}
	if m.ov != oGoal || m.err == "" {
		t.Fatal("0 days is outside the phone's 1–999")
	}
	for _, k := range []string{"backspace", "9", "0", "enter"} {
		m = press(t, m, k)
	}
	if last := m.q.Items[len(m.q.Items)-1]; m.ov != oNone || m.numFor != "" || last.Name != masteredKey || last.Value != "90" || m.synced.MasteredDays != 90 {
		t.Fatalf("90 days must be written: %+v", last)
	}
}

func TestInvertedSwipesSwapTheArrows(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 30
	m.prefs.InvertedSwipes = true
	m = dealt(t, m, rcard(1, "el pan", "хлеб", 2, 2))
	out := sgrRe.ReplaceAllString(m.View(), "")
	if !strings.Contains(out, "← Missed it") || !strings.Contains(out, "Got it →") {
		t.Fatalf("inverted swipes put got it on the right:\n%s", out)
	}
	m = press(t, m, "right")
	if it := answered(t, m); !it.Positive {
		t.Fatal("→ answers got it with inverted swipes")
	}
}

func TestUndoTakesTheLastAnswerBack(t *testing.T) {
	m := dealt(t, testModel(t), rcard(7, "la foca", "тюлень", 2, 2))
	m = press(t, m, "left")
	if len(m.sess.undo) != 0 {
		t.Fatal("without a working copy there is no row to put back")
	}
	if next, _ := m.Update(key("u")); !next.(Model).sess.dealing || len(next.(Model).q.Items) != 1 {
		t.Fatal("nothing to take back while the next card is on its way")
	}
	m = dealt(t, m, rcard(8, "el oso", "медведь", 2, 2))
	c := &card{kind: cR1, wordID: 7, word: "la foca", mode: "rep", variantIDs: []int64{3, 7, 5, 9}}
	receipt := rwcore.Receipt{Detail: rwcore.ReceiptDetail{Detail: map[string]any{"pre": map[string]any{"q_rec": 2.0}, "at": 1000.0}}}
	m.sess.pushUndo(c, receipt, 1)
	if !slices.Contains(m.sessionHints(), kb{"u", "undo"}) {
		t.Fatal("the footer offers the undo")
	}
	next, cmd := m.Update(key("u"))
	m = next.(Model)
	it := m.q.Items[len(m.q.Items)-1]
	if cmd == nil || !m.sess.dealing || m.sess.cur != nil || m.sess.ok != 0 || len(m.sess.undo) != 0 {
		t.Fatalf("undo takes the answer back and deals the word again: ok=%d undo=%d", m.sess.ok, len(m.sess.undo))
	}
	if it.Op != "restore" || it.ID != 7 || it.At != 1000 || string(it.Row) != `{"q_rec":2}` {
		t.Fatalf("undo queues the row from before the answer: %+v", it)
	}
	for range undoDepth + 1 {
		m.sess.pushUndo(c, receipt, 0)
	}
	if len(m.sess.undo) != undoDepth {
		t.Fatalf("the phone keeps %d answers, got %d", undoDepth, len(m.sess.undo))
	}
}

func TestSessionDealsThroughRwcore(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 30
	next, cmd := m.startSession(modeReview)
	m = next.(Model)
	if cmd == nil || !m.sess.dealing || m.sess.cur != nil || m.screen != sSession {
		t.Fatal("a session starts by asking rwcore for a card")
	}
	if !strings.Contains(m.View(), "dealing") {
		t.Fatal("the screen must say a card is on its way")
	}
	nr := int64(1000 + 7200)
	next, _ = m.onDeal(dealMsg{mode: modeReview, deal: rwcore.Deal{Day: rwcore.Day{NextReview: &nr}, Now: 1000}})
	m = next.(Model)
	if out := sgrRe.ReplaceAllString(m.View(), ""); !strings.Contains(out, "Words for review will show up in 2 hours") {
		t.Fatalf("no card: say when the next review falls due, as the phone does:\n%s", out)
	}
	g := int64(5)
	m.sess.mode = modeLearn
	next, _ = m.onDeal(dealMsg{mode: modeLearn, deal: rwcore.Deal{Day: rwcore.Day{Goal: &g, LearnedToday: 5, GoalReached: true}}})
	m = next.(Model)
	if out := sgrRe.ReplaceAllString(m.View(), ""); !strings.Contains(out, "Today you've learned 5 new words") {
		t.Fatalf("a met goal stops learning with the phone's goal screen:\n%s", out)
	}
	next, _ = m.onDeal(dealMsg{mode: modeReview, deal: rwcore.Deal{Card: new(rcard(1, "a", "б", 1, 2))}})
	if next.(Model).sess.cur != nil {
		t.Fatal("a deal for a mode left meanwhile must not land")
	}
}

func TestViewAllCards(t *testing.T) {
	m := testModel(t)
	cards := []*card{
		{kind: cR1, prompt: "w", native: "н", tr: "t"},
		{kind: cR1, prompt: "w", native: "н", reveal: true, example: "ex"},
		{kind: cR1, prompt: "w", native: "н", done: true, wasOk: true},
		{kind: cR1, prompt: "w", native: "н", done: true},
		{kind: cR1, prompt: "н", native: "w", choices: []string{"a", "b", "c", "d"}, keyboard: true, attempts: 3},
		{kind: cR1, prompt: "н", choices: []string{"a", "b", "c", "d"}, answer: 1, pane: paneChoose},
		{kind: cR1, prompt: "н", choices: []string{"a", "b", "c", "d"}, answer: 1, pane: paneChoose, pick: 3, reveal: true},
		{kind: cR1, prompt: "н", native: "w", keyboard: true, attempts: 3, pane: paneType},
		{kind: cR1, prompt: "н", native: "w", keyboard: true, pane: paneType, typed: vRight, reveal: true},
		{kind: cL1, prompt: "w", native: "н", tr: "t", example: "ex"},
		{kind: cL1b, prompt: "w", native: "н", done: true, wasOk: true},
		{kind: cL1b, prompt: "w", native: "н", choices: []string{"a", "b", "c", "d"}},
	}
	for i, c := range cards {
		m.sess.cur = c
		m.sess.typing = c.pane == paneType && c.typed == vNone
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
	if m.sess.mode != modeLearn {
		t.Fatalf("Learn new words must start learning, got mode %d", m.sess.mode)
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
	m.setIdx = sharedRows
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
	m.sess.mode = modeLearn
	m = dealt(t, m, rcard(1, "uno", "один", 2, 1))
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
	week := make([]rwcore.WeekDay, 7)
	for i := range week {
		week[i] = rwcore.WeekDay{Date: time.Now().AddDate(0, 0, i-6).Format("2006-01-02"), Learned: int64(i * 7)}
	}
	m.today = &rwcore.Today{Learned: 10, Goal: &g, StreakCur: 2, StreakBest: 6, ActiveDates: []string{"2026-09-13"}, Week: week, WeekGoal: &g}
	m.stats = &rwcore.Stats{Settings: rwcore.Settings{DailyGoal: strp("30"), NativeLanguage: strp("RUS")}}
	m.due = []rwcore.DueItem{{Word: "x", Modes: []int64{1}, OverdueSecs: 100}}
	m.cats = []rwcore.Category{{ID: "custom", NameEn: strp("My words"), Words: 5}, {ID: "averylongcategorynamewithoutanyspacesinit", Words: 3}}
	m.catSel = map[string]bool{"custom": true}
	m.catPct = map[string]string{"custom": "12%"}
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
	g2 := int64(30)
	m.sess = session{mode: 2, started: true, day: rwcore.Day{Due: 182, Learning: 6, LearnedToday: 4, Goal: &g2}}
	m.sess.cur = cardFrom(&rwcore.Card{Word: longWord(), Side: 1, Status: 1, Queue: 1}, "RUS", m.prefs)
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
		{"R1-blocks", card{kind: cR1, word: long.Text, prompt: nat, native: long.Text, choices: choices, keyboard: true, attempts: 3, mode: "rep"}},
		{"R1-choose", card{kind: cR1, word: long.Text, prompt: nat, choices: choices, answer: 1, pane: paneChoose, mode: "rep"}},
		{"R1-choose-miss", card{kind: cR1, word: long.Text, prompt: nat, choices: choices, answer: 1, pane: paneChoose, pick: 3, reveal: true, example: ex, mode: "rep"}},
		{"R1-typing", card{kind: cR1, word: long.Text, prompt: nat, native: long.Text, keyboard: true, attempts: 3, pane: paneType, mode: "rep"}},
		{"L1", card{kind: cL1, word: long.Text, prompt: long.Text, native: nat, example: ex}},
		{"L1b", card{kind: cL1b, word: long.Text, prompt: long.Text, native: nat, example: ex, choices: choices}},
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
					if c.pane == paneType {
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
		{"settings last row", func(m *Model) { m.screen, m.ov, m.searchOn = sSettings, oNone, false; m.setIdx = len(settingRows) - 1 }},
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
			c := card{kind: cR1, word: long.Text, prompt: "перевод", choices: choices, answer: 1, pane: paneChoose, mode: "rep"}
			m.sess.cur = &c
			out := m.View()
			for i := 1; i <= 4; i++ {
				if !strings.Contains(out, fmt.Sprintf("%d  ", i)) {
					fails = append(fails, fmt.Sprintf("R2 %dx%-2d choice %d cropped", w, h, i))
				}
			}
			if !strings.Contains(out, "1-4") {
				fails = append(fails, fmt.Sprintf("R2 %dx%-2d pick keys missing from the footer", w, h))
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

func TestLearnMenuRowsMapToModes(t *testing.T) {
	for row, want := range []int{modeLearn, modeReview, modeMixed} {
		m := testModel(t)
		m.screen = sLearn
		m.menuIdx = row
		m = press(t, m, "enter")
		if m.sess.mode != want {
			t.Fatalf("menu row %d must start mode %d, got %d", row, want, m.sess.mode)
		}
	}
}

func TestNewWordHidesTranslationUntilShown(t *testing.T) {
	m := testModel(t)
	m.sess.mode = modeLearn
	m = dealt(t, m, rcard(7, "la foca", "тюлень", 1, 0))
	if m.sess.cur.kind != cL1 {
		t.Fatalf("setup: want a new-word card, got %v", m.sess.cur.kind)
	}
	out := m.View()
	if strings.Contains(out, "тюлень") {
		t.Fatal("a new word must hide its translation until space")
	}
	if !strings.Contains(out, "Start learning") || strings.Contains(out, "Got it") {
		t.Fatal("a new word must offer the learning answers, not review grades")
	}
	m = press(t, m, " ")
	if !strings.Contains(m.View(), "тюлень") {
		t.Fatal("space must show the translation")
	}
	m.prefs.RevealAtOnce = true
	m = dealt(t, m, rcard(8, "el oso", "медведь", 1, 0))
	if !strings.Contains(m.View(), "медведь") {
		t.Fatal("'show translation at once' must reveal new words too")
	}
}

func TestReviewAdvancesByItself(t *testing.T) {
	m := dealt(t, testModel(t), rcard(1, "a", "а", 1, 2))
	next, cmd := m.Update(key("g"))
	m = next.(Model)
	if m.sess.cur != nil || cmd == nil || m.sess.ok != 1 {
		t.Fatal("got it in review must deal the next word by itself")
	}
	m.sess.mode = modeLearn
	m = dealt(t, m, rcard(2, "c", "с", 1, 0))
	m = press(t, m, "l")
	if m.sess.cur != nil {
		t.Fatal("a learning decision must move on like a swipe")
	}
	if m.sess.ok != 1 {
		t.Fatalf("start learning is a decision, not a correct answer: ok=%d", m.sess.ok)
	}
}

func TestChooseBlockPicksWithHjklOrDigits(t *testing.T) {
	onCard := func(kind cardKind) Model {
		m := testModel(t)
		m.screen = sSession
		m.sess.mode = modeReview
		m.sess.cur = &card{kind: kind, word: "a", wordID: 1, mode: "rep", choices: []string{"x", "y", "z", "w"}, answer: 1}
		return press(t, m, "c")
	}
	for i, k := range []string{"h", "j", "k", "l"} {
		m := onCard(cR1)
		if m.sess.cur.pane != paneChoose {
			t.Fatal("c must open the choose block")
		}
		next, cmd := m.Update(key(k))
		m = next.(Model)
		c := m.sess.cur
		if cmd != nil || c == nil || c.word != "a" || c.pick != i+1 || !c.reveal || len(m.q.Items) != 0 {
			t.Fatalf("%s must pick answer %d and stay on the card ungraded: %+v", k, i+1, c)
		}
		if d := press(t, onCard(cR1), strconv.Itoa(i+1)); d.sess.cur.pick != i+1 {
			t.Fatalf("%d must pick the same answer as %s", i+1, k)
		}
	}
	m := press(t, onCard(cR1), "j")
	m = press(t, m, "h")
	if m.sess.cur.pick != 2 {
		t.Fatal("a pick is final")
	}
	m = press(t, m, "left")
	if it := answered(t, m); !it.Positive || m.sess.cur != nil {
		t.Fatalf("← after a pick must answer got it and move on: %+v", it)
	}
	m = press(t, onCard(cL1b), "l")
	m = press(t, m, "l")
	if len(m.q.Items) != 0 || m.sess.cur == nil || m.sess.cur.pick != 4 {
		t.Fatal("hjkl in a choose block must never fall through to the card's own letters")
	}
	m = press(t, onCard(cR1), "esc")
	if m.screen != sSession || m.sess.cur.pane != paneNone {
		t.Fatal("esc before a pick must close the choose block, not the session")
	}
}

func TestAttemptsWarningStaysWithItsCard(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.cur = &card{kind: cR1, word: "a", mode: "rep", keyboard: true, attempts: 2, pane: paneType}
	m.sess.typing = true
	m = press(t, m, "z")
	m = graded(t, press(t, m, "enter"), "wrong")
	if m.notice != "not quite · 1 attempt left" || !m.noticeWarn {
		t.Fatalf("a wrong try must warn with the attempts left, got %q", m.notice)
	}
	m = press(t, m, "z")
	m = graded(t, press(t, m, "enter"), "wrong")
	if m.notice != "" || m.sess.cur.typed != vWrong {
		t.Fatalf("the last wrong try resolves the card and drops the warning, got %q", m.notice)
	}
}

func TestTypedAnswerStaysUntilArrow(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.mode = modeReview
	m.sess.cur = &card{kind: cR1, word: "a", mode: "rep", native: "a", keyboard: true, attempts: 3, pane: paneType}
	m.sess.typing = true
	m = press(t, m, "a")
	m = graded(t, press(t, m, "enter"), "correct")
	if m.sess.cur == nil || m.sess.cur.word != "a" || m.sess.cur.typed != vRight {
		t.Fatal("a typed answer must stay on screen with its result")
	}
	if out := sgrRe.ReplaceAllString(m.View(), ""); !strings.Contains(out, "← Got it") || !strings.Contains(out, "Missed it →") {
		t.Fatal("a resolved answer must offer the two grades under the frame")
	}
	m = press(t, m, "left")
	if it := answered(t, m); !it.Positive || m.sess.cur != nil {
		t.Fatal("← must answer got it and move on to the next card")
	}
}

func TestTabSwitchesSessionAndRedeals(t *testing.T) {
	m := dealt(t, testModel(t), rcard(1, "r1", "р", 1, 2))
	next, cmd := m.Update(key("tab"))
	m = next.(Model)
	if m.sess.mode != modeLearn || m.sess.cur != nil || !m.sess.dealing || cmd == nil {
		t.Fatal("tab must switch to learning and deal from there")
	}
	if len(m.q.Items) != 0 {
		t.Fatal("switching leaves the unanswered card untouched")
	}
}

func TestSessionBarShowsTheDay(t *testing.T) {
	m := dealt(t, testModel(t), rcard(1, "a", "а", 1, 2))
	g := int64(10)
	m.sess.day = rwcore.Day{LearnedToday: 3, Goal: &g, Due: 7, Learning: 2}
	bar := sgrRe.ReplaceAllString(m.sessionBar(), "")
	if !strings.Contains(bar, "3/10 today") || !strings.Contains(bar, "due 7") {
		t.Fatalf("the bar reads the day's goal and the reviews due: %q", bar)
	}
	col := sgrRe.ReplaceAllString(strings.Join(m.modeColumn(), "\n"), "")
	if !strings.Contains(col, "Review (7)") || !strings.Contains(col, "Learning (2)") {
		t.Fatalf("the modes count what rwcore counts: %q", col)
	}
}

func TestSessionQuietQueue(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.cur = &card{kind: cR1, word: "a", mode: "rec", reveal: true}
	m = press(t, m, "g")
	if m.notice != "" {
		t.Fatalf("grading must not raise a queued notice in a session: %q", m.notice)
	}
}

func TestDuplicateTextsResolveByID(t *testing.T) {
	m := dealt(t, testModel(t), rcard(7393, "la carne", "мясо, плоть", 1, 2))
	m = press(t, m, " ")
	m = press(t, m, "g")
	body := answered(t, m).ApplyBody()
	if body["word"] != "7393" || body["positive"] != true || body["op"] != "answer" || body["ts"] == nil {
		t.Fatalf("the answer must target the dealt word's id, not a same-text twin: %v", body)
	}
}

func TestWordCardDetails(t *testing.T) {
	m := testModel(t)
	m.screen = sWord
	tr := "[feˈliθ]"
	m.word = &rwcore.Word{ID: 1, Text: "feliz", Transcription: &tr, Translations: map[string]string{"RUS": "счастливый"}}
	m.wordLog = []rwcore.LogEntry{
		{Date: "2026-08-10", Mode: 1, Queue: 2, Step: 0},
		{Date: "2026-08-11", Mode: 1, Queue: 2, Step: 1},
		{Date: "2026-08-12", Mode: 2, Queue: 2, Step: 2},
	}
	out := sgrRe.ReplaceAllString(m.View(), "")
	if strings.Contains(out, "[[") {
		t.Fatal("a transcription that carries brackets must not get a second pair")
	}
	for _, want := range []string{"1st review", "2nd review", "3rd review"} {
		if !strings.Contains(out, want) {
			t.Fatalf("history must read %q:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	rule := strings.Repeat("─", 10)
	for i := 1; i < len(lines); i++ {
		if strings.Contains(lines[i], rule) && strings.Contains(lines[i-1], rule) {
			t.Fatal("a word without examples must not stack two separators")
		}
	}
	if ordinal(11) != "11th" || ordinal(21) != "21st" || ordinal(112) != "112th" {
		t.Fatal("ordinal suffixes are wrong for the teens")
	}
}

func TestSwipeArrowsPickTheSideAnswer(t *testing.T) {
	onCard := func(kind cardKind, reveal bool) Model {
		m := testModel(t)
		m.screen = sSession
		m.sess.mode = modeLearn
		m.sess.cur = &card{kind: kind, word: "w", wordID: 9, reveal: reveal, mode: "rec"}
		return m
	}
	for _, tc := range []struct {
		kind   cardKind
		reveal bool
		key    string
		left   bool
	}{
		{cL1, false, "left", true},
		{cL1, false, "right", false},
		{cL1b, false, "right", false},
		{cL1b, true, "left", true},
		{cR1, false, "left", true},
		{cR1, false, "right", false},
	} {
		m := press(t, onCard(tc.kind, tc.reveal), tc.key)
		if it := answered(t, m); it.Positive != tc.left || it.ID != 9 || m.sess.cur != nil {
			t.Fatalf("%v %s must queue the %v answer and move on: %+v", tc.kind, tc.key, tc.left, it)
		}
	}
	m := onCard(cR1, false)
	m.sess.cur.choices, m.sess.cur.pane = []string{"a", "b", "c", "d"}, paneChoose
	m = press(t, m, "left")
	if len(m.q.Items) != 0 || m.sess.cur == nil {
		t.Fatal("an open block hides the swipes until it is resolved")
	}
}

func TestSwipeLabelsSitUnderTheCard(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.width, m.height = 112, 40
	m.sess.mode = modeLearn
	m = dealt(t, m, rcard(3, "el topo", "крот", 1, 0))
	lines := strings.Split(sgrRe.ReplaceAllString(m.View(), ""), "\n")
	bottom := -1
	for i, ln := range lines {
		if strings.Contains(ln, "└") {
			bottom = i
		}
	}
	if bottom < 0 || bottom+1 >= len(lines) {
		t.Fatal("no card frame rendered")
	}
	frame, under := lines[bottom], lines[bottom+1]
	if !strings.Contains(under, "← Already known") || !strings.Contains(under, "Start learning →") {
		t.Fatalf("the answers must sit right under the frame: %q", under)
	}
	leftEdge := lipgloss.Width(frame[:strings.Index(frame, "└")])
	rightEdge := lipgloss.Width(frame[:strings.LastIndex(frame, "┘")])
	if got := lipgloss.Width(under[:strings.Index(under, "←")]); got != leftEdge+2 {
		t.Fatalf("← must line up with the card's text, col %d vs frame %d", got, leftEdge)
	}
	if got := lipgloss.Width(under[:strings.LastIndex(under, "→")]); got != rightEdge-2 {
		t.Fatalf("→ must end at the card's right side, col %d vs frame %d", got, rightEdge)
	}
	for _, ln := range lines[:bottom] {
		if strings.Contains(ln, "[space show") || strings.Contains(ln, "already know]") {
			t.Fatal("the card must not repeat the footer keys inside the frame")
		}
	}
}

func TestArrowsKeepTyping(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.cur = &card{kind: cR1, word: "x", mode: "rep", keyboard: true, attempts: 3, pane: paneType}
	m.sess.typing, m.sess.input = true, "ab"
	m = press(t, m, "left")
	if !m.sess.typing || m.sess.input != "ab" {
		t.Fatalf("an arrow must not end typing, typing=%v input=%q", m.sess.typing, m.sess.input)
	}
}

func TestBurstInputReplaysRunes(t *testing.T) {
	burst := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	m := testModel(t)
	m.screen = sSession
	m.sess.cur = &card{kind: cR1, word: "la carne", mode: "rep", keyboard: true, attempts: 3, pane: paneType}
	m.sess.typing = true
	next, _ := m.Update(burst("la carne"))
	if m = next.(Model); m.sess.input != "la carne" || !m.sess.typing {
		t.Fatalf("a fast-typed answer must land in the input, got %q typing=%v", m.sess.input, m.sess.typing)
	}
	m = testModel(t)
	m.screen, m.searchOn = sVocab, true
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("leche"), Paste: true})
	if m = next.(Model); m.search != "leche" {
		t.Fatalf("a pasted search must land in the field, got %q", m.search)
	}
}

func TestLearningVerdictNotQueued(t *testing.T) {
	m := testModel(t)
	m.screen = sSession
	m.sess.mode = modeLearn
	m = dealt(t, m, rcard(7, "la foca", "тюлень", 1, 0))
	m = press(t, m, "l")
	if m.notice != "" || strings.Contains(m.View(), "queued:") {
		t.Fatal("start learning must not raise a queued notice")
	}
}

func TestReviewTitleNamesItsNumber(t *testing.T) {
	c := &card{kind: cR1, mode: "rep", stepRec: 4, stepRep: 2}
	for _, tc := range []struct {
		mode string
		pane pane
		rec  int64
		want string
	}{
		{"rep", paneNone, 4, "2nd review"},
		{"rec", paneNone, 4, "4th review"},
		{"rec", paneType, 4, "4th review · type"},
		{"rec", paneChoose, 11, "11th review · choose"},
		{"rec", paneNone, 21, "21st review"},
	} {
		c.mode, c.pane, c.stepRec = tc.mode, tc.pane, tc.rec
		if got := cardTitle(c); got != tc.want {
			t.Fatalf("title %q, want %q", got, tc.want)
		}
	}
	if cardTitle(&card{kind: cL1}) != "new word" || cardTitle(&card{kind: cL1b}) != "learning" {
		t.Fatal("new and learning words keep their headers")
	}
}

func TestStreakDotsFollowTheGoal(t *testing.T) {
	g := int64(10)
	m := testModel(t)
	m.today = &rwcore.Today{WeekGoal: &g, StreakCur: 1, StreakBest: 3, Week: []rwcore.WeekDay{
		{Date: "2000-01-03", Learned: 10}, {Date: "2000-01-04", Learned: 12}, {Date: "2000-01-05", Learned: 3},
		{Date: "2000-01-06", Learned: 4}, {Date: "2000-01-07"}, {Date: "2000-01-08"}, {Date: "2000-01-09"},
	}}
	out := sgrRe.ReplaceAllString(m.viewDots(), "")
	if !strings.HasPrefix(out, "● ── ● ── ◐ ── ◐ ── ○ ── ○ ── ○") || !strings.Contains(out, "Current 1 · Best 3") {
		t.Fatalf("a day fills at the goal and half-fills below it:\n%s", out)
	}
	m.today.WeekGoal = nil
	if out := sgrRe.ReplaceAllString(m.viewDots(), ""); !strings.HasPrefix(out, "○ ── ○ ── ○") {
		t.Fatalf("without a goal no day fills, as on the phone:\n%s", out)
	}
}

func TestEachAppKeepsItsOwnQueue(t *testing.T) {
	m := testModel(t)
	m.enqueue(queue.Intent{Op: "select", Category: "animals"})
	if it := m.q.Items[0]; it.App != "es" || m.q.Path != queue.PathFor(m.cfg.QueuePath, "es") {
		t.Fatalf("a change is marked with its app and kept in that app's queue: %+v in %s", it, m.q.Path)
	}
	next, cmd := m.openApp("en")
	m = next.(Model)
	if cmd == nil || m.q.Path != queue.PathFor(m.cfg.QueuePath, "en") || len(m.q.Items) != 0 || m.cli.DB != "" {
		t.Fatalf("another app opens on its own queue and working copy: %s %d", m.q.Path, len(m.q.Items))
	}
	if got := queue.Load(queue.PathFor(m.cfg.QueuePath, "es")).Items; len(got) != 1 {
		t.Fatal("the first app's queue stays as it was")
	}
}

func TestQueuingRaisesNoNotice(t *testing.T) {
	m := testModel(t)
	m.screen = sLearn
	m.enqueue(queue.Intent{Op: "select", Category: "animals", Selected: false})
	if len(m.q.Items) != 1 || m.notice != "" {
		t.Fatalf("a queued change must stay quiet, notice %q", m.notice)
	}
}

func TestKeyboardNeverFirst(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 30
	m.prefs.ShowTranscription = true
	tr := func(s string) map[string]string { return map[string]string{"RUS": s} }
	rc := rcard(1, "el pan", "хлеб", 2, 2)
	rc.Word.Transcription = new("[el ˈpan]")
	rc.Keyboard, rc.Choose = true, true
	rc.Variants = []rwcore.Word{
		{ID: 2, Text: "la mesa", Translations: tr("стол")},
		rc.Word,
		{ID: 3, Text: "el gato", Translations: tr("кот")},
		{ID: 4, Text: "la casa", Translations: tr("дом")},
	}
	m = dealt(t, m, rc)
	c := m.sess.cur
	if c == nil || m.sess.typing || c.pane != paneNone {
		t.Fatal("a card must open on its three blocks, never straight in the keyboard")
	}
	if !c.keyboard || len(c.choices) != 4 || c.choices[c.answer] != "el pan" {
		t.Fatalf("keyboard and choose must come together: keyboard=%v choices=%v", c.keyboard, c.choices)
	}
	out := sgrRe.ReplaceAllString(m.View(), "")
	for _, b := range []string{"i type", "space show", "c choose"} {
		if !strings.Contains(out, b) {
			t.Fatalf("the card must offer %q:\n%s", b, out)
		}
	}
	if strings.Contains(out, "ˈpan") {
		t.Fatal("the translation side must not voice the answer before it is shown")
	}
	m = press(t, m, "i")
	m = press(t, m, "esc")
	if m.sess.typing || m.sess.cur.pane != paneNone || m.screen != sSession {
		t.Fatal("esc must close the keyboard back to the blocks")
	}
	if m = press(t, m, " "); !strings.Contains(sgrRe.ReplaceAllString(m.View(), ""), "ˈpan") {
		t.Fatal("space must show the word with its transcription")
	}
	m = dealt(t, m, rcard(1, "el pan", "хлеб", 1, 2))
	if c := m.sess.cur; c.keyboard || len(c.choices) != 0 {
		t.Fatalf("a card without blocks offers none: %+v", c)
	}
	if out := sgrRe.ReplaceAllString(m.View(), ""); strings.Contains(out, "i type") || strings.Contains(out, "c choose") || !strings.Contains(out, "space show") {
		t.Fatalf("the word side must offer show alone:\n%s", out)
	}
	lc := rcard(5, "el vaso", "стакан", 2, 1)
	lc.Keyboard = true
	if m = dealt(t, m, lc); m.sess.cur.kind != cL1b || m.sess.cur.prompt != "стакан" || !m.sess.cur.keyboard {
		t.Fatalf("a word in learning gets the same blocks on its translation side: %+v", m.sess.cur)
	}
}

func TestSessionCardAlignsWithHeader(t *testing.T) {
	m := fitModel(t)
	m.screen, m.ov, m.searchOn = sSession, oNone, false
	m.width, m.height = 112, 42
	lines := strings.Split(sgrRe.ReplaceAllString(m.View(), ""), "\n")
	rule := strings.Index(lines[1], "─")
	for _, ln := range lines {
		if i := strings.Index(ln, "┌"); i >= 0 {
			if i != rule {
				t.Fatalf("card must line up with the header rule: card at %d, rule at %d", i, rule)
			}
			return
		}
	}
	t.Fatal("no card frame rendered")
}

func TestSessionCardCenteredWithModeColumn(t *testing.T) {
	m := fitModel(t)
	m.screen, m.ov, m.searchOn = sSession, oNone, false
	m.width, m.height = 144, 42
	var frame, modes int
	lines := strings.Split(sgrRe.ReplaceAllString(m.View(), ""), "\n")
	frame, modes = -1, -1
	for i, ln := range lines {
		if frame < 0 && strings.Contains(ln, "┌") {
			frame = i
		}
		if modes < 0 && strings.Contains(ln, "Learning (") {
			modes = i
		}
	}
	if frame < 0 || modes < 0 {
		t.Fatalf("session must show a card and the mode column (frame=%d modes=%d)", frame, modes)
	}
	fl := lines[frame]
	left := strings.Index(fl, "┌")
	right := lipgloss.Width(fl[:strings.LastIndex(fl, "┐")]) + 1
	if d := left - (144 - right); d < -1 || d > 1 {
		t.Fatalf("card must sit on the screen's center line: left margin %d, right margin %d", left, 144-right)
	}
	if col := strings.Index(lines[modes], "Learning ("); col < 0 || col >= left {
		t.Fatalf("mode column must sit left of the card: col=%d card=%d", col, left)
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

func TestPickerDigitsSelectApp(t *testing.T) {
	mk := func(t *testing.T) Model {
		m := testModel(t)
		m.appID = ""
		m.screen = sPicker
		m.apps = []rwcore.App{{N: 1, ID: "en"}, {N: 2, ID: "es"}, {N: 3, ID: "fr"}}
		return m
	}
	for _, tc := range []struct {
		key string
		id  string
	}{
		{"1", "en"},
		{"2", "es"},
		{"3", "fr"},
	} {
		m := press(t, mk(t), tc.key)
		if m.appID != tc.id || m.screen != sLearn {
			t.Fatalf("picker %q: appID=%q screen=%v, want %q sLearn", tc.key, m.appID, m.screen, tc.id)
		}
	}
	m := press(t, mk(t), "q")
	if m.ov != oQuit {
		t.Fatal("q on picker must open the quit guard")
	}
	m = press(t, mk(t), "?")
	if m.ov != oHelp {
		t.Fatal("? on picker must open help")
	}
}
