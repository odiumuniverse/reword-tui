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

type card struct {
	kind     cardKind
	word     string
	wordID   int64
	prompt   string
	native   string
	tr       string
	example  string
	choices  []string
	answer   int
	expected []string
	mode     string
	reveal   bool
	done     bool
	wasOk    bool
	attempts int
}

type reviewUnit struct {
	word    string
	mode    int64
	overdue int64
}

type session struct {
	mode    int
	units   []reviewUnit
	learn   []rwcore.Word
	lpos    int
	turn    bool
	started bool
	cur     *card
	ok      int
	fail    int
	zen     bool
	typing  bool
	input   string
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
			out = append(out, o+" — "+it.T)
		} else {
			out = append(out, o)
		}
	}
	return out
}

func parseExamplesFull(raw string) [][2]string {
	var out [][2]string
	for _, it := range decodeExamples(raw) {
		out = append(out, [2]string{stripHashes(it.O), it.T})
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
			units = append(units, reviewUnit{word: d.Word, mode: mo, overdue: d.OverdueSecs})
		}
	}
	m.sess.units = units
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
	return m.reviewCard(picked.word, picked.mode)
}

func (m *Model) nextCard() *card {
	s := &m.sess
	if s.mode == 2 {
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
				return m.learnCard(w)
			}
		}
		return nil
	}
	if s.mode == 0 {
		return m.pickReview()
	}
	for s.lpos < len(s.learn) {
		w := s.learn[s.lpos]
		s.lpos++
		return m.learnCard(w)
	}
	return nil
}

func (m *Model) reviewCard(word string, mode int64) *card {
	w, err := m.cli.Show(m.appID, word)
	if err != nil {
		return &card{kind: cR1, word: word, prompt: word, native: "(gone — skipped)", done: true, wasOk: true}
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
		return &card{kind: cL1, word: w.Text, wordID: w.ID, prompt: prompt, native: shown, tr: tr, example: ex}
	}
	if w.Recognition.Level <= 1 || w.Reproduction.Level <= 1 {
		return &card{kind: cL1b, word: w.Text, wordID: w.ID, prompt: prompt, native: shown, tr: tr, example: ex}
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

func (c *card) hint() string {
	if c.done {
		return "enter next"
	}
	switch c.kind {
	case cR1:
		if !c.reveal {
			return "space reveal"
		}
		return "g got it · m missed it"
	case cR2, cL2:
		return "1-4 pick"
	case cR3:
		return "type answer · enter check"
	case cL1:
		return "l start learning · k already know"
	case cL1b:
		return "l memorized · k already know · space keep showing"
	}
	return ""
}

func (m *Model) gradeCurrent(ok bool) {
	c := m.sess.cur
	if c == nil {
		return
	}
	m.enqueue(queue.Intent{Op: "grade", Word: c.word, Mode: c.mode, Result: gradeResult(ok)})
	if ok {
		m.sess.ok++
	} else {
		m.sess.fail++
	}
	c.done, c.wasOk = true, ok
}
