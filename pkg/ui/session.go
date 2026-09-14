package ui

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

type cardKind int

const (
	cR1 cardKind = iota
	cL1
	cL1b
)

// pane is the block a tested card opened. Like the phone, a card offers
// three equal ways to check yourself — type, show, choose from four —
// and none of them is picked for the user.
type pane int

const (
	paneNone pane = iota
	paneType
	paneChoose
)

// verdict is how a typed answer came out.
type verdict int

const (
	vNone verdict = iota
	vRight
	// vPartial is accepted in yellow, as on the phone: loose letters (an
	// accent, ё for е) or only some of the meanings.
	vPartial
	vWrong
)

// Session modes. The Learn menu lists them as Learn, Review, Mixed; they
// map to the phone's sessions: new-only, review-only and smart.
const (
	modeReview = iota
	modeLearn
	modeMixed
)

type card struct {
	kind     cardKind
	word     string
	wordID   int64
	prompt   string
	native   string // what the card hides: the translation, or the word itself
	tr       string
	example  string
	choices  []string // choose-from-4 answers; empty when the block is off
	answer   int
	keyboard bool // the keyboard block is offered; rwcore grades the answer
	pane     pane
	pick     int // chosen answer, 1-based; 0 = not yet
	typed    verdict
	mode     string // the side the card asks: "rec" or "rep"
	stepRec  int64
	stepRep  int64
	reveal   bool
	done     bool
	wasOk    bool
	attempts int
	// variantIDs are the choose-from-4's words, kept so an undo shows the
	// card again with the same answers.
	variantIDs []int64
}

type session struct {
	mode     int
	day      rwcore.Day
	now      int64 // when the last card was dealt
	cur      *card
	dealing  bool // a deal is out; its card lands in cur
	started  bool
	ok       int
	fail     int
	zen      bool
	typing   bool
	input    string
	checking bool // a typed answer is out with rwcore
	undo     []undoStep
}

// undoDepth is how many answers the phone's presenter keeps to take back.
const undoDepth = 200

// undoStep is one answer the session can take back (the phone's foa): the
// word's scheduling columns before it, when it was given, and the card as
// it was shown.
type undoStep struct {
	wordID   int64
	word     string
	side     int64 // 1 recognition, 2 reproduction
	row      json.RawMessage
	at       int64
	variants []int64
	counted  int // +1 got it, -1 missed it, 0 not a review
}

// kind names the phone's session for a mode.
func (s *session) kind() string {
	switch s.mode {
	case modeReview:
		return "review"
	case modeLearn:
		return "new"
	}
	return "smart"
}

func pickNative(w rwcore.Word, native string) string {
	for _, k := range []string{native, "RUS", "ENG"} {
		if k == "" {
			continue
		}
		if v, ok := w.Translations[k]; ok && strings.TrimSpace(v) != "" {
			return firstLine(v)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(w.Translations)) {
		if v := strings.TrimSpace(w.Translations[k]); v != "" {
			return firstLine(v)
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func pickExample(w rwcore.Word, native string) string {
	var raw string
	for _, k := range []string{native, "RUS", "ENG"} {
		if k == "" {
			continue
		}
		if v, ok := w.Examples[k]; ok && v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		for _, k := range slices.Sorted(maps.Keys(w.Examples)) {
			if w.Examples[k] != "" {
				raw = w.Examples[k]
				break
			}
		}
	}
	pairs := parseExamples(raw)
	if len(pairs) == 0 {
		return ""
	}
	return pairs[0]
}

type examplePair struct {
	O string `json:"o"`
	T string `json:"t"`
}

func decodeExamples(raw string) []examplePair {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var items []examplePair
	if err := json.Unmarshal([]byte(raw), &items); err != nil || len(items) == 0 {
		return nil
	}
	return items
}

func stripHashes(s string) string { return strings.ReplaceAll(s, "#", "") }

func parseExamples(raw string) []string {
	var out []string
	for _, it := range decodeExamples(raw) {
		o := stripHashes(it.O)
		if it.T != "" {
			out = append(out, o+" — "+stripHashes(it.T))
		} else {
			out = append(out, o)
		}
	}
	return out
}

func parseExamplesFull(raw string) [][2]string {
	var out [][2]string
	for _, it := range decodeExamples(raw) {
		out = append(out, [2]string{stripHashes(it.O), stripHashes(it.T)})
	}
	return out
}

// cardFrom lays out a card rwcore dealt. Recognition shows the word and
// hides its translation, reproduction the other way round. The blocks come
// as the phone offers them: the keyboard asks for what the card hides (rwcore
// grades it against the whole of it), and the choose block reads its four
// answers the way the card does.
func cardFrom(rc *rwcore.Card, native string, prefs Prefs) *card {
	if rc == nil {
		return nil
	}
	w := rc.Word
	nat := pickNative(w, native)
	c := &card{
		word:     w.Text,
		wordID:   w.ID,
		example:  pickExample(w, native),
		reveal:   prefs.RevealAtOnce,
		keyboard: rc.Keyboard,
		attempts: 3,
		stepRec:  w.Recognition.Step,
		stepRep:  w.Reproduction.Step,
	}
	switch rc.Status {
	case 0:
		c.kind = cL1
	case 1:
		c.kind = cL1b
	default:
		c.kind = cR1
	}
	if w.Transcription != nil && prefs.ShowTranscription {
		c.tr = *w.Transcription
	}
	show := func(v rwcore.Word) string { return pickNative(v, native) }
	if rc.Side == 1 {
		c.mode, c.prompt, c.native = "rec", w.Text, nat
	} else {
		c.mode, c.prompt, c.native = "rep", nat, w.Text
		show = func(v rwcore.Word) string { return v.Text }
	}
	if rc.Choose {
		for i, v := range rc.Variants {
			c.choices = append(c.choices, show(v))
			c.variantIDs = append(c.variantIDs, v.ID)
			if v.ID == w.ID {
				c.answer = i
			}
		}
	}
	return c
}

// deal asks rwcore for the session's next card, keeping the card just
// answered out of the draw as the phone does.
func (m Model) deal(exclude int64) tea.Cmd {
	cli, app, kind, mode := m.cli, m.appID, m.sess.kind(), m.sess.mode
	return func() tea.Msg {
		d, err := cli.Next(app, kind, exclude)
		return dealMsg{mode: mode, deal: d, err: err}
	}
}

func (m Model) onDeal(msg dealMsg) (tea.Model, tea.Cmd) {
	// A deal for a mode left meanwhile has no place on screen.
	if m.screen != sSession || msg.mode != m.sess.mode {
		return m, nil
	}
	m.sess.dealing = false
	m.loading = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.sess.day, m.sess.now = msg.deal.Day, msg.deal.Now
	m.sess.cur = cardFrom(msg.deal.Card, m.nativeLang(), m.prefs)
	m.sess.started = true
	m.sess.typing, m.sess.input = false, ""
	return m, nil
}

// answer sends a swipe as the phone reads it: positive is the left answer.
// rwcore picks the action from the queue of the card's side, the working
// copy takes it at once, and the next card is dealt from there.
func (m Model) answer(positive bool) (tea.Model, tea.Cmd) {
	c := m.sess.cur
	if c == nil {
		return m, nil
	}
	if m.noticeWarn {
		// A warning belongs to the card it was about.
		m.setNotice("", false)
	}
	r := m.enqueue(queue.Intent{Op: "answer", Word: c.word, ID: c.wordID, Mode: c.mode, Positive: positive})
	counted := 0
	if c.kind == cR1 {
		if positive {
			m.sess.ok++
			counted = 1
		} else {
			m.sess.fail++
			counted = -1
		}
	}
	m.sess.pushUndo(c, r, counted)
	m.sess.cur, m.sess.typing, m.sess.input = nil, false, ""
	m.sess.dealing = true
	return m, m.deal(c.wordID)
}

// pushUndo keeps what taking this answer back needs; the working copy's
// receipt carries the word's row from before it.
func (s *session) pushUndo(c *card, r rwcore.Receipt, counted int) {
	pre, ok := r.Detail.Detail["pre"]
	if !ok {
		return
	}
	row, err := json.Marshal(pre)
	if err != nil {
		return
	}
	at, _ := r.Detail.Detail["at"].(float64)
	side := int64(1)
	if c.mode == "rep" {
		side = 2
	}
	if len(s.undo) == undoDepth {
		s.undo = s.undo[1:]
	}
	s.undo = append(s.undo, undoStep{
		wordID: c.wordID, word: c.word, side: side, row: row, at: int64(at),
		variants: c.variantIDs, counted: counted,
	})
}

// undo takes the last answer back, as the phone's undo does: the word's
// columns return, that answer's LOG rows go, and its card comes again with
// the choose-from-4 it had.
func (m Model) undo() (tea.Model, tea.Cmd) {
	s := &m.sess
	n := len(s.undo)
	if n == 0 || s.dealing {
		return m, nil
	}
	st := s.undo[n-1]
	s.undo = s.undo[:n-1]
	m.enqueue(queue.Intent{Op: "restore", Word: st.word, ID: st.wordID, Row: st.row, At: st.at})
	switch st.counted {
	case 1:
		s.ok--
	case -1:
		s.fail--
	}
	s.cur, s.typing, s.input = nil, false, ""
	s.dealing = true
	cli, app, mode := m.cli, m.appID, s.mode
	return m, func() tea.Msg {
		d, err := cli.Card(app, st.wordID, st.side, st.variants)
		return dealMsg{mode: mode, deal: d, err: err}
	}
}

// swipeSides lays a card's answers on ← and →: the positive one on the
// left, or on the right with inverted swipes, as on the phone.
func (m Model) swipeSides(c *card) (left, right string, ok bool) {
	pos, neg, ok := c.swipe()
	if m.prefs.InvertedSwipes {
		return neg, pos, ok
	}
	return pos, neg, ok
}

// check has rwcore grade a typed answer against the card's side of the word,
// with the phone's own matcher.
func (m Model) check(c *card, typed string) tea.Cmd {
	cli, app, id, mode := m.cli, m.appID, c.wordID, c.mode
	return func() tea.Msg {
		res, err := cli.Check(app, id, mode, typed)
		return checkMsg{wordID: id, mode: mode, res: res, err: err}
	}
}

// onCheck takes the grade as the phone's keyboard block does: a correct or
// partial answer opens the card for the swipe, a wrong one costs one of the
// three attempts, and the last one opens it as missed.
func (m Model) onCheck(msg checkMsg) (tea.Model, tea.Cmd) {
	s := &m.sess
	s.checking = false
	c := s.cur
	if m.screen != sSession || c == nil || c.wordID != msg.wordID || c.mode != msg.mode || !s.typing {
		return m, nil
	}
	if msg.err != nil {
		m.setNotice("check failed: "+msg.err.Error(), true)
		return m, nil
	}
	if msg.res.Accepted {
		s.typing, s.input = false, ""
		m.setNotice("", false)
		c.typed, c.reveal = vRight, true
		if msg.res.Verdict == "partial" {
			c.typed = vPartial
		}
		return m, nil
	}
	c.attempts--
	s.input = ""
	if c.attempts <= 0 {
		s.typing = false
		m.setNotice("", false)
		c.typed, c.reveal = vWrong, true
		return m, nil
	}
	left := "1 attempt left"
	if c.attempts > 1 {
		left = fmt.Sprintf("%d attempts left", c.attempts)
	}
	m.setNotice("not quite · "+left, true)
	return m, nil
}

func (m Model) switchMode(mode int) (tea.Model, tea.Cmd) {
	if mode == m.sess.mode {
		return m, nil
	}
	m.sess.mode = mode
	m.sess.cur, m.sess.typing, m.sess.input = nil, false, ""
	m.sess.dealing = true
	return m, m.deal(0)
}

func (m Model) startSession(mode int) (tea.Model, tea.Cmd) {
	m.sess = session{mode: mode, dealing: true}
	m.screen = sSession
	m.notice, m.noticeWarn = "", false
	m.loading = "session"
	return m, m.deal(0)
}

func (m *Model) nativeLang() string {
	if m.stats != nil && m.stats.Settings.NativeLanguage != nil {
		return *m.stats.Settings.NativeLanguage
	}
	return "RUS"
}

// cardTitle is the phone's card header (WordCardDeckView): new word,
// learning, or the review's number on the card's side, then the open block.
func cardTitle(c *card) string {
	var title string
	switch c.kind {
	case cR1:
		step := c.stepRec
		if c.mode == "rep" {
			step = c.stepRep
		}
		title = ordinal(step) + " review"
	case cL1:
		title = "new word"
	case cL1b:
		title = "learning"
	}
	switch c.pane {
	case paneType:
		return title + " · type"
	case paneChoose:
		return title + " · choose"
	}
	return title
}

// swipe names the two answers a card offers on ← and →, mirroring the
// phone's swipes: left takes the word out of the loop, right keeps it in.
// As on the phone (WordCardView) they are there from the start — the
// blocks are optional checks, not a gate.
func (c *card) swipe() (left, right string, ok bool) {
	if c == nil || c.done || c.pane != paneNone && !c.reveal {
		// An open block hides the answers until it is resolved.
		return "", "", false
	}
	switch c.kind {
	case cL1:
		return "Already known", "Start learning", true
	case cL1b:
		return "I have memorized", "Keep showing", true
	case cR1:
		return "Got it", "Missed it", true
	}
	return "", "", false
}
