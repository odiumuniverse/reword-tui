package ui

import (
	"cmp"
	"encoding/json"
	"maps"
	"math/rand"
	"slices"
	"strings"

	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

type cardKind int

const (
	cR1 cardKind = iota
	cR2
	cR3
	cL1
	cL1b
	cL2
)

// Session modes. The Learn menu lists them as Learn, Review, Mixed.
const (
	modeReview = iota
	modeLearn
	modeMixed
)

type card struct {
	kind      cardKind
	word      string
	wordID    int64
	prompt    string
	native    string
	tr        string
	example   string
	choices   []string
	answer    int
	expected  []string
	mode      string
	reveal    bool
	done      bool
	wasOk     bool
	attempts  int
	fromLearn bool       // drawn from the learning queue, not a review unit
	unit      reviewUnit // review unit behind the card, put back on a mode switch
	verdict   string     // what a learning decision did, shown once resolved
}

type reviewUnit struct {
	id      int64
	word    string
	mode    int64
	overdue int64
}

type session struct {
	mode     int
	units    []reviewUnit
	revWords int // distinct words in the review queue when it was built
	learn    []rwcore.Word
	lpos     int
	turn     bool
	started  bool
	cur      *card
	seq      int // bumps on every card change so stale auto-advance ticks are ignored
	ok       int
	fail     int
	zen      bool
	typing   bool
	input    string
}

// reviewLeft counts distinct words still to review, the current card included.
func (s *session) reviewLeft() int {
	seen := map[unitWord]bool{}
	for _, u := range s.units {
		seen[u.key()] = true
	}
	if c := s.cur; c != nil && !c.done && !c.fromLearn && c.unit.word != "" {
		seen[c.unit.key()] = true
	}
	return len(seen)
}

// unitWord identifies the word behind a review unit: by id, since texts
// repeat across categories, else by text.
type unitWord struct {
	id   int64
	text string
}

func (u reviewUnit) key() unitWord {
	if u.id != 0 {
		return unitWord{id: u.id}
	}
	return unitWord{text: u.word}
}

// learnLeft counts learning words not finished yet, the current card included.
func (s *session) learnLeft() int {
	n := len(s.learn) - s.lpos
	if c := s.cur; c != nil && !c.done && c.fromLearn {
		n++
	}
	return max(n, 0)
}

// poolWord resolves a word from the already-loaded session pool,
// avoiding a backend round-trip per card.
func (m *Model) poolWord(id int64) (rwcore.Word, bool) {
	if i, ok := m.poolIdx[id]; ok && i >= 0 && i < len(m.pool) {
		return m.pool[i], true
	}
	return rwcore.Word{}, false
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

func normalizeAnswer(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case '-', '‐', '−', '–', '—':
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
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

func (m *Model) buildLearnQueue() {
	var q []rwcore.Word
	for _, w := range m.pool {
		if w.Recognition.Level <= 1 || w.Reproduction.Level <= 1 {
			q = append(q, w)
		}
	}
	rand.Shuffle(len(q), func(i, j int) { q[i], q[j] = q[j], q[i] })
	m.sess.learn = q
}

func (m *Model) buildReview() {
	var units []reviewUnit
	for _, d := range m.due {
		modes := slices.Clone(d.Modes)
		switch m.prefs.ReviewFirst {
		case "native":
			slices.Sort(modes)
			slices.Reverse(modes)
		case "target":
			slices.Sort(modes)
		default:
			rand.Shuffle(len(modes), func(i, j int) { modes[i], modes[j] = modes[j], modes[i] })
		}
		for _, mo := range modes {
			units = append(units, reviewUnit{id: d.ID, word: d.Word, mode: mo, overdue: d.OverdueSecs})
		}
	}
	m.sess.units = units
	seen := map[unitWord]bool{}
	for _, u := range units {
		seen[u.key()] = true
	}
	m.sess.revWords = len(seen)
}

func (m *Model) pickReview() *card {
	u := m.sess.units
	if len(u) == 0 {
		return nil
	}
	n := len(u)
	if n > 5 {
		n = 5
	}
	i := rand.Intn(n)
	picked := u[i]
	m.sess.units = append(u[:i], u[i+1:]...)
	c := m.reviewCard(picked.id, picked.word, picked.mode)
	c.unit = picked
	return c
}

func (m *Model) learnFrom(w rwcore.Word) *card {
	c := m.learnCard(w)
	c.fromLearn = true
	return c
}

func (m *Model) nextCard() *card {
	s := &m.sess
	if s.mode == modeMixed {
		for len(s.units) > 0 || s.lpos < len(s.learn) {
			s.turn = !s.turn
			if s.turn && len(s.units) > 0 {
				if c := m.pickReview(); c != nil {
					return c
				}
				continue
			}
			if s.lpos < len(s.learn) {
				w := s.learn[s.lpos]
				s.lpos++
				return m.learnFrom(w)
			}
		}
		return nil
	}
	if s.mode == modeReview {
		return m.pickReview()
	}
	if s.lpos < len(s.learn) {
		w := s.learn[s.lpos]
		s.lpos++
		return m.learnFrom(w)
	}
	return nil
}

func (m *Model) reviewCard(id int64, word string, mode int64) *card {
	w, ok := m.poolWord(id)
	if !ok {
		var err error
		w, err = m.cli.Show(m.appID, wordRef(id, word))
		if err != nil {
			return &card{kind: cR1, word: word, wordID: id, prompt: word, native: "(gone — skipped)", done: true, wasOk: true}
		}
	}
	nat := pickNative(w, m.nativeLang())
	tr := ""
	if w.Transcription != nil && m.prefs.ShowTranscription {
		tr = *w.Transcription
	}
	if mode == 1 {
		prompt, shown := w.Text, nat
		if m.prefs.ReviewFirst == "native" {
			prompt, shown = nat, w.Text
		} else if m.prefs.ReviewFirst == "random" && rand.Intn(2) == 0 {
			prompt, shown = nat, w.Text
		}
		return &card{kind: cR1, word: w.Text, wordID: w.ID, prompt: prompt, native: shown, tr: tr, mode: "rec", reveal: m.prefs.RevealAtOnce}
	}
	if m.prefs.Keyboard {
		return &card{kind: cR3, word: w.Text, wordID: w.ID, prompt: nat, native: "", tr: tr, expected: []string{w.Text}, mode: "rep", attempts: 3}
	}
	if m.prefs.Guess {
		if choices, ans, ok := m.makeChoicesEs(w); ok {
			return &card{kind: cR2, word: w.Text, wordID: w.ID, prompt: nat, native: "", tr: tr, choices: choices, answer: ans, mode: "rep"}
		}
	}
	return &card{kind: cR1, word: w.Text, wordID: w.ID, prompt: w.Text, native: nat, tr: tr, mode: "rep", reveal: m.prefs.RevealAtOnce}
}

func (m *Model) learnCard(w rwcore.Word) *card {
	nat := pickNative(w, m.nativeLang())
	tr := ""
	if w.Transcription != nil && m.prefs.ShowTranscription {
		tr = *w.Transcription
	}
	ex := pickExample(w, m.nativeLang())
	prompt, shown := w.Text, nat
	if m.prefs.NewFirst == "native" {
		prompt, shown = nat, w.Text
	} else if m.prefs.NewFirst == "random" && rand.Intn(2) == 0 {
		prompt, shown = nat, w.Text
	}
	if w.Recognition.Level <= 0 && w.Reproduction.Level <= 0 {
		return &card{kind: cL1, word: w.Text, wordID: w.ID, prompt: prompt, native: shown, tr: tr, example: ex, reveal: m.prefs.RevealAtOnce}
	}
	if w.Recognition.Level <= 1 || w.Reproduction.Level <= 1 {
		return &card{kind: cL1b, word: w.Text, wordID: w.ID, prompt: prompt, native: shown, tr: tr, example: ex, reveal: m.prefs.RevealAtOnce}
	}
	if prompt == nat {
		var pool []string
		seen := map[string]bool{w.Text: true}
		for _, o := range m.pool {
			if seen[o.Text] || o.Text == "" {
				continue
			}
			seen[o.Text] = true
			pool = append(pool, o.Text)
		}
		if choices, ans, ok := pickN(w.Text, pool); ok {
			return &card{kind: cL2, word: w.Text, wordID: w.ID, prompt: prompt, native: "", choices: choices, answer: ans}
		}
		return &card{kind: cR1, word: w.Text, wordID: w.ID, prompt: prompt, native: shown, tr: tr, mode: "rec"}
	}
	choices, ans, ok := m.makeChoices(w)
	if !ok {
		return &card{kind: cR1, word: w.Text, wordID: w.ID, prompt: prompt, native: shown, mode: "rec"}
	}
	return &card{kind: cL2, word: w.Text, wordID: w.ID, prompt: prompt, native: "", choices: choices, answer: ans}
}

func (m *Model) makeChoices(w rwcore.Word) ([]string, int, bool) {
	nat := pickNative(w, m.nativeLang())
	type cand struct {
		text string
		pos  int64
		dist int
	}
	var pool []cand
	var wpos int64 = -1
	if w.Pos != nil {
		wpos = *w.Pos
	}
	for _, o := range m.pool {
		if o.Text == w.Text {
			continue
		}
		v := pickNative(o, m.nativeLang())
		if v == "" || v == nat {
			continue
		}
		var opos int64 = -1
		if o.Pos != nil {
			opos = *o.Pos
		}
		if wpos >= 0 && opos != wpos {
			continue
		}
		d := len([]rune(o.Text)) - len([]rune(w.Text))
		if d < 0 {
			d = -d
		}
		pool = append(pool, cand{v, opos, d})
	}
	slices.SortFunc(pool, func(a, b cand) int { return cmp.Compare(a.dist, b.dist) })
	pool = pool[:min(len(pool), 12)]
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if len(pool) < 3 {
		return nil, 0, false
	}
	choices := []string{nat, pool[0].text, pool[1].text, pool[2].text}
	rand.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })
	for i, c := range choices {
		if c == nat {
			return choices, i, true
		}
	}
	return choices, 0, true
}

func (m *Model) makeChoicesEs(w rwcore.Word) ([]string, int, bool) {
	var pool []string
	seen := map[string]bool{w.Text: true}
	for _, o := range m.pool {
		if seen[o.Text] || o.Text == "" {
			continue
		}
		seen[o.Text] = true
		pool = append(pool, o.Text)
	}
	return pickN(w.Text, pool)
}

func pickN(answer string, pool []string) ([]string, int, bool) {
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if len(pool) < 3 {
		return nil, 0, false
	}
	choices := []string{answer, pool[0], pool[1], pool[2]}
	rand.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })
	for i, c := range choices {
		if c == answer {
			return choices, i, true
		}
	}
	return choices, 0, true
}

func (m *Model) nativeLang() string {
	if m.stats != nil && m.stats.Settings.NativeLanguage != nil {
		return *m.stats.Settings.NativeLanguage
	}
	return "RUS"
}

func cardTitle(c *card) string {
	switch c.kind {
	case cR1:
		return "recall"
	case cR2:
		return "choose"
	case cR3:
		return "type"
	case cL1:
		return "new word"
	case cL1b:
		return "learning"
	case cL2:
		return "quiz"
	}
	return ""
}

// swipe names the two answers a card offers on ← and →, mirroring the
// phone's swipes: left takes the word out of the loop, right keeps it in.
func (c *card) swipe() (left, right string, ok bool) {
	if c == nil || c.done {
		return "", "", false
	}
	switch c.kind {
	case cL1:
		return "Already known", "Start learning", true
	case cL1b:
		return "I have memorized", "Keep showing", true
	case cR1:
		if c.reveal {
			return "Got it", "Missed it", true
		}
	}
	return "", "", false
}

// swipeKey maps ← or → to the session key of the answer on that side,
// or "" when the card offers no two-way choice right now.
func (c *card) swipeKey(left bool) string {
	if _, _, ok := c.swipe(); !ok {
		return ""
	}
	switch c.kind {
	case cL1:
		if left {
			return "k"
		}
		return "l"
	case cL1b:
		if left {
			return "l"
		}
		return "keep"
	case cR1:
		if left {
			return "g"
		}
		return "m"
	}
	return ""
}

func (m *Model) gradeCurrent(ok bool) {
	c := m.sess.cur
	if c == nil {
		return
	}
	m.enqueue(queue.Intent{Op: "grade", Word: c.word, ID: c.wordID, Mode: c.mode, Result: gradeResult(ok)})
	if ok {
		m.sess.ok++
	} else {
		m.sess.fail++
	}
	c.done, c.wasOk = true, ok
}
