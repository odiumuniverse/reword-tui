package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

func orphanPath(dataDir string) string {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		if runtime.GOOS == "darwin" {
			dataDir = filepath.Join(home, "Library", "Application Support", "reword")
		} else {
			dataDir = filepath.Join(home, ".local", "share", "reword")
		}
	}
	return filepath.Join(dataDir, "orphans.jsonl")
}

func (m Model) Init() tea.Cmd {
	if m.appID != "" {
		return tea.Batch(m.loadMain(), m.loadCats())
	}
	return m.loadApps()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case appsMsg:
		return m.onApps(msg)
	case statsMsg:
		return m.onStats(msg)
	case catsMsg:
		return m.onCats(msg)
	case catStatsMsg:
		return m.onCatStats(msg)
	case wordsMsg:
		return m.onWords(msg)
	case wordMsg:
		return m.onWord(msg)
	case syncMsg:
		return m.onSync(msg)
	case replayMsg:
		m.replayPlan = msg.out
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		m.loading = ""
		if msg.err == nil && msg.out == "pulled" {
			m.notice = "pulled"
			m.err = ""
			m.loading = "sync"
			return m, m.loadSync()
		}
		return m, nil
	case writeMsg:
		return m.onWrite(msg)
	case selectAllMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.screen = sLearn
			return m, nil
		}
		m.pool = nil
		return m, tea.Batch(m.loadMain(), m.loadCats())
	case importMsg:
		m.loading = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.notice = "The words have been successfully imported"
		m.screen = sVocab
		return m, m.loadCats()
	case pickerMetaMsg:
		m.appMeta[msg.id] = appMeta{words: msg.words, due: msg.due}
		return m, nil
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onApps(msg appsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.apps = msg.apps
	if m.appIdx >= len(m.apps) {
		m.appIdx = max(len(m.apps)-1, 0)
	}
	if len(m.apps) == 1 {
		m.appID = m.apps[0].ID
		m.screen = sLearn
		return m, tea.Batch(m.loadMain(), m.loadCats())
	}
	m.screen = sPicker
	cmds := make([]tea.Cmd, 0, len(m.apps))
	for _, a := range m.apps {
		cmds = append(cmds, m.loadPickerMeta(a.ID))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) onStats(msg statsMsg) (tea.Model, tea.Cmd) {
	m.loading = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.stats = &msg.stats
	m.today = &msg.today
	m.due = msg.due
	m.maybeOnboard()
	if m.screen == sSession && !m.sess.started && len(m.pool) == 0 {
		return m, m.loadPool()
	}
	return m, nil
}

func (m Model) onCats(msg catsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.cats = msg.cats
	for _, c := range msg.cats {
		if _, ok := m.catSel[c.ID]; !ok {
			m.catSel[c.ID] = c.Selected
		}
	}
	if m.vocabIdx >= len(m.cats) {
		m.vocabIdx = max(len(m.cats)-1, 0)
	}
	m.maybeOnboard()
	return m, nil
}

func (m Model) onCatStats(msg catStatsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	for _, s := range msg.stats {
		if s.Total > 0 {
			m.catPct[s.Category] = fmt.Sprintf("%d%%", s.Started*100/s.Total)
		} else {
			m.catPct[s.Category] = "—"
		}
	}
	return m, nil
}

func (m Model) anySelected() bool {
	for _, c := range m.cats {
		if m.catSel[c.ID] {
			return true
		}
	}
	return false
}

func (m Model) knownGoal() (int64, bool) {
	if m.today != nil && m.today.Goal != nil {
		return *m.today.Goal, true
	}
	if m.stats != nil && m.stats.Settings.DailyGoal != nil {
		if g, err := strconv.Atoi(*m.stats.Settings.DailyGoal); err == nil {
			return int64(g), true
		}
	}
	return 0, false
}

func (m *Model) maybeOnboard() {
	if m.prefs.Onboarded || m.appID == "" || m.obStep != 0 || len(m.cats) == 0 {
		return
	}
	if m.stats == nil && m.today == nil {
		return
	}
	if !m.anySelected() {
		m.obStep = 1
		m.screen = sVocab
		m.vocabMode = 0
		m.notice = "Choose some categories to start learning"
		return
	}
	if _, ok := m.knownGoal(); !ok {
		m.obStep = 2
		m.goalTitle = "How many new words do you want to learn per day?"
		m.goalInput = ""
		m.ov = oGoal
		return
	}
	m.prefs.Onboarded = true
	_ = m.prefs.Save()
}

func (m *Model) finishOnboarding() {
	m.obStep = 0
	m.prefs.Onboarded = true
	_ = m.prefs.Save()
}

func (m Model) onWords(msg wordsMsg) (tea.Model, tea.Cmd) {
	m.loading = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	if msg.key == "pool" {
		m.pool = msg.words
		m.poolIdx = make(map[string]int, len(msg.words))
		for i, w := range msg.words {
			if _, ok := m.poolIdx[w.Text]; !ok {
				m.poolIdx[w.Text] = i
			}
		}
		m.menuIdx = 0
		m.buildLearnQueue()
		m.buildReview()
		m.advance()
		return m, nil
	}
	m.vocabWords = msg.words
	if m.wlIdx >= len(m.vocabWords) {
		m.wlIdx = max(len(m.vocabWords)-1, 0)
	}
	m.detail[msg.key] = msg.words
	return m, nil
}

func (m Model) onWord(msg wordMsg) (tea.Model, tea.Cmd) {
	m.loading = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	w := msg.word
	m.word = &w
	m.wordLog = msg.log
	m.wordEx = false
	m.acted = map[string]bool{}
	m.screen = sWord
	return m, nil
}

func (m Model) onSync(msg syncMsg) (tea.Model, tea.Cmd) {
	m.loading = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.syncRows = msg.rows
	m.oplog = msg.ops
	m.orphans = msg.orph
	return m, nil
}

func (m Model) onWrite(msg writeMsg) (tea.Model, tea.Cmd) {
	m.loading = ""
	// Drop exactly the applied prefix: intents appended while the
	// async write was in flight stay queued.
	if err := m.q.Consume(msg.consumed); err != nil {
		m.err = "queue not trimmed: " + err.Error() + " (will retry)"
		m.quitAfterWrite = false
	}
	if msg.dirty {
		m.err = "DIRTY_SOURCE: pull first"
		m.screen = sSync
		return m, m.loadSync()
	}
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.written += msg.written
	quit := m.quitAfterWrite
	m.quitAfterWrite = false
	if quit && msg.orphaned == 0 {
		return m, tea.Quit
	}
	if msg.orphaned > 0 {
		m.notice = fmt.Sprintf("written %d · orphaned %d", msg.written, msg.orphaned)
		return m, m.loadSync()
	}
	m.notice = fmt.Sprintf("written %d", msg.written)
	return m, tea.Batch(m.loadMain(), m.loadSync())
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ov != oNone {
		return m.overlayKey(msg)
	}
	if m.searchOn {
		return m.searchKey(msg)
	}
	switch m.screen {
	case sAdd:
		return m.addKey(msg)
	case sImport:
		return m.importKey(msg)
	case sAddCat:
		return m.addCatKey(msg)
	case sSession:
		return m.sessionKey(msg.String())
	}
	k := msg.String()
	switch k {
	case "q", "ctrl+c":
		m.ov = oQuit
		return m, nil
	case "?":
		m.ov = oHelp
		return m, nil
	case "1":
		m.screen = sLearn
		m.menuIdx = 0
		return m, m.loadMain()
	case "2":
		if m.appID == "" {
			return m, nil
		}
		m.screen = sVocab
		m.vocabMode = 0
		m.menuIdx = 0
		return m, tea.Batch(m.loadCats(), m.loadCatStats())
	case "3":
		m.screen = sMenu
		m.menuIdx = 0
		return m, nil
	case "s":
		m.screen = sSync
		m.loading = "sync"
		return m, m.loadSync()
	case "r":
		if m.screen == sSync {
			return m.syncKey(k)
		}
		return m.refresh()
	}
	switch m.screen {
	case sPicker:
		return m.pickerKey(k)
	case sLearn:
		return m.learnKey(k)
	case sSession:
		return m.sessionKey(k)
	case sVocab:
		return m.vocabKey(k, msg)
	case sWord:
		return m.wordKey(k)
	case sStats:
		return m.statsKey(k)
	case sSync:
		return m.syncKey(k)
	case sMenu:
		return m.menuKey(k)
	case sSettings:
		return m.settingsKey(k, msg)
	case sImport:
		return m.importKey(msg)
	case sAddCat:
		return m.addCatKey(msg)
	}
	return m, nil
}

func (m Model) refresh() (tea.Model, tea.Cmd) {
	m.err, m.notice = "", ""
	switch m.screen {
	case sLearn, sStats:
		m.loading = "load"
		return m, m.loadMain()
	case sVocab:
		m.loading = "cats"
		return m, m.loadCats()
	case sSync:
		m.loading = "sync"
		return m, m.loadSync()
	}
	return m, nil
}

func (m Model) pickerKey(k string) (tea.Model, tea.Cmd) {
	if len(m.apps) == 0 {
		if k == "esc" {
			return m, tea.Quit
		}
		return m, nil
	}
	if n, err := strconv.Atoi(k); err == nil {
		if i := slices.IndexFunc(m.apps, func(a rwcore.App) bool { return a.N == n }); i >= 0 {
			m.appIdx = i
			m.appID = m.apps[i].ID
			m.screen = sLearn
			return m, tea.Batch(m.loadMain(), m.loadCats())
		}
		return m, nil
	}
	switch k {
	case "h", "left":
		m.appIdx = max(m.appIdx-1, 0)
	case "l", "right":
		m.appIdx = min(m.appIdx+1, len(m.apps)-1)
	case "enter":
		if m.appIdx < len(m.apps) {
			m.appID = m.apps[m.appIdx].ID
			m.screen = sLearn
			return m, tea.Batch(m.loadMain(), m.loadCats())
		}
	case "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) learnKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "j", "down":
		m.menuIdx = min(m.menuIdx+1, 2)
	case "k", "up":
		m.menuIdx = max(m.menuIdx-1, 0)
	case "c":
		m.screen = sVocab
		m.vocabMode = 0
		return m, m.loadCats()
	case "enter":
		return m.startSession(min(max(m.menuIdx, 0), 2))
	}
	return m, nil
}

func (m Model) startSession(mode int) (tea.Model, tea.Cmd) {
	m.sess = session{mode: mode}
	m.screen = sSession
	if m.prefs.ReviewFrom == "all" {
		m.ov = oConfirm
		m.confirmT = "review from all categories? (selects all)"
		m.pending = "selectall"
		m.loading = ""
		return m, nil
	}
	m.loading = "session"
	return m, m.loadPool()
}

func (m Model) loadPool() tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		pool, err := cli.Words(app, "", "", 20000)
		if err != nil {
			return wordsMsg{key: "pool", err: err}
		}
		return wordsMsg{key: "pool", words: pool}
	}
}

func (m Model) switchMode(mode int) (tea.Model, tea.Cmd) {
	m.sess.mode = mode
	m.sess.units = nil
	m.sess.lpos = 0
	m.sess.cur = nil
	m.sess.typing = false
	m.sess.input = ""
	if mode != 1 {
		m.buildReview()
	}
	if mode != 0 {
		m.buildLearnQueue()
	}
	m.advance()
	return m, nil
}

func (m *Model) advance() {
	m.sess.cur = m.nextCard()
	m.sess.started = true
	m.sess.typing = false
	m.sess.input = ""
	if m.sess.cur != nil && m.sess.cur.kind == cR3 && !m.sess.cur.done {
		m.sess.typing = true
	}
}

func (m Model) sessionKey(k string) (tea.Model, tea.Cmd) {
	s := &m.sess
	if s.cur == nil {
		m.advance()
		if m.sess.cur == nil {
			switch k {
			case "esc":
				m.screen = sLearn
				return m, m.loadMain()
			case "q", "ctrl+c":
				m.ov = oQuit
			}
			return m, nil
		}
		s = &m.sess
	}
	if s.typing {
		return m.typeKey(k)
	}
	switch k {
	case "q", "ctrl+c":
		m.ov = oQuit
		return m, nil
	case "?":
		m.ov = oHelp
		return m, nil
	case "s":
		m.screen = sSync
		m.loading = "sync"
		return m, m.loadSync()
	case "r":
		return m, m.loadMain()
	}
	c := s.cur
	if c.done {
		switch k {
		case "enter", " ":
			m.advance()
		case "e":
			m.wordReturn = sSession
			return m, m.loadWord(c.word)
		case "esc":
			m.screen = sLearn
			return m, m.loadMain()
		}
		return m, nil
	}
	switch c.kind {
	case cR1:
		switch k {
		case " ":
			c.reveal = true
		case "g":
			m.gradeCurrent(true)
		case "m":
			m.gradeCurrent(false)
		case "e":
			m.wordReturn = sSession
			return m, m.loadWord(c.word)
		case "z":
			s.zen = !s.zen
		case "esc":
			m.screen = sLearn
			return m, m.loadMain()
		case "1":
			return m.switchMode(0)
		case "2":
			return m.switchMode(1)
		}
	case cR2, cL2:
		if n, err := strconv.Atoi(k); err == nil && n >= 1 && n <= len(c.choices) {
			if c.kind == cR2 {
				m.gradeCurrent(n-1 == c.answer)
			} else {
				c.done, c.wasOk = true, n-1 == c.answer
				if c.wasOk {
					s.ok++
				} else {
					s.fail++
				}
			}
			return m, nil
		}
		switch k {
		case "e":
			m.wordReturn = sSession
			return m, m.loadWord(c.word)
		case "z":
			s.zen = !s.zen
		case "esc":
			m.screen = sLearn
			return m, m.loadMain()
		case "1":
			return m.switchMode(0)
		case "2":
			return m.switchMode(1)
		}
	case cR3:
		switch k {
		case "e":
			m.wordReturn = sSession
			return m, m.loadWord(c.word)
		case "z":
			s.zen = !s.zen
		case "esc":
			m.screen = sLearn
			return m, m.loadMain()
		default:
			if isRuneKey(k) || k == "backspace" || k == "enter" || k == " " {
				s.typing = true
				return m.typeKey(k)
			}
		}
	case cL1:
		switch k {
		case "k":
			m.ov = oConfirm
			m.confirmT = "Mark '" + c.word + "' as Already known?"
			m.pending = "triage-known"
			m.pendingWord = c.word
		case "l":
			m.enqueue(queue.Intent{Op: "triage", Word: c.word, Decision: "learn"})
			s.ok++
			c.done, c.wasOk = true, true
		case "e":
			m.wordReturn = sSession
			return m, m.loadWord(c.word)
		case "esc":
			m.screen = sLearn
			return m, m.loadMain()
		}
	case cL1b:
		switch k {
		case "k":
			m.ov = oConfirm
			m.confirmT = "Mark '" + c.word + "' as Already known?"
			m.pending = "triage-known"
			m.pendingWord = c.word
		case "l":
			m.enqueue(queue.Intent{Op: "triage", Word: c.word, Decision: "learn"})
			s.ok++
			nc := *c
			nc.kind = cL2
			if w, ok := m.poolWord(c.word); ok {
				if choices, ans, ok := m.makeChoices(w); ok {
					nc.choices, nc.answer = choices, ans
					m.sess.cur = &nc
					return m, nil
				}
			} else if w, err := m.cli.Show(m.appID, c.word); err == nil {
				if choices, ans, ok := m.makeChoices(w); ok {
					nc.choices, nc.answer = choices, ans
					m.sess.cur = &nc
					return m, nil
				}
			}
			c.done, c.wasOk = true, true
		case " ", "enter":
			c.done, c.wasOk = true, true
		case "e":
			m.wordReturn = sSession
			return m, m.loadWord(c.word)
		case "esc":
			m.screen = sLearn
			return m, m.loadMain()
		}
	}
	if k == "enter" && c.done {
		m.advance()
	}
	return m, nil
}

func isRuneKey(k string) bool {
	return len([]rune(k)) == 1
}

func chop(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

func (m Model) typeKey(k string) (tea.Model, tea.Cmd) {
	s := &m.sess
	c := s.cur
	switch k {
	case "esc":
		s.typing = false
		s.input = ""
		return m, nil
	case "backspace":
		s.input = chop(s.input)
	case "enter", " ":
		if k == " " {
			s.input += " "
			return m, nil
		}
		got := normalizeAnswer(s.input)
		ok := slices.ContainsFunc(c.expected, func(e string) bool {
			return got == normalizeAnswer(e)
		})
		if ok {
			s.typing = false
			s.input = ""
			m.gradeCurrent(true)
		} else {
			c.attempts--
			s.input = ""
			if c.attempts <= 0 {
				s.typing = false
				m.gradeCurrent(false)
			} else {
				m.notice = fmt.Sprintf("You have %d attempts.", c.attempts)
			}
		}
	default:
		if isRuneKey(k) {
			s.input += k
		} else {
			s.typing = false
		}
	}
	return m, nil
}

func pickNativeOf(m Model, word string) string {
	if i := slices.IndexFunc(m.pool, func(w rwcore.Word) bool { return w.Text == word }); i >= 0 {
		return pickNative(m.pool[i], m.nativeLang())
	}
	return word
}

func (m Model) vocabKey(k string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.vocabMode == 1 {
		switch k {
		case "esc":
			m.vocabMode = 0
			m.menuIdx = 0
			return m, nil
		case "j", "down":
			if len(m.vocabWords) > 0 {
				m.wlIdx = min(m.wlIdx+1, len(m.vocabWords)-1)
			}
		case "k", "up":
			if len(m.vocabWords) > 0 {
				m.wlIdx = max(m.wlIdx-1, 0)
			}
		case "enter":
			if m.wlIdx >= 0 && m.wlIdx < len(m.vocabWords) {
				return m, m.loadWord(m.vocabWords[m.wlIdx].Text)
			}
		}
		return m, nil
	}
	switch k {
	case "j", "down":
		if len(m.cats) > 0 {
			m.vocabIdx = min(m.vocabIdx+1, len(m.cats)-1)
		}
	case "k", "up":
		if len(m.cats) > 0 {
			m.vocabIdx = max(m.vocabIdx-1, 0)
		}
	case " ":
		if m.vocabIdx < len(m.cats) {
			c := m.cats[m.vocabIdx]
			m.catSel[c.ID] = !m.catSel[c.ID]
			m.enqueue(queue.Intent{Op: "select", Category: c.ID, Selected: m.catSel[c.ID]})
		}
	case "enter":
		if m.obStep == 1 {
			if _, ok := m.knownGoal(); ok {
				m.finishOnboarding()
				m.screen = sLearn
				return m, m.loadMain()
			}
			m.obStep = 2
			m.goalTitle = "How many new words do you want to learn per day?"
			m.goalInput = ""
			m.ov = oGoal
			return m, nil
		}
		if m.vocabIdx < len(m.cats) {
			c := m.cats[m.vocabIdx]
			m.vocabMode = 1
			m.wlIdx = 0
			m.wordListTitle = c.DisplayName()
			m.loading = "words"
			return m, m.loadWords("cat:"+c.ID, "", c.ID, 200)
		}
	case "/":
		m.searchOn = true
		m.search = ""
	case "a":
		m.screen = sAdd
		m.addF = []string{"", "", "", ""}
		m.addIdx = 0
		m.addEnroll = true
	case "C":
		m.screen = sAddCat
		m.catF = []string{"", ""}
		m.catIdx = 0
	case "X":
		if m.vocabIdx < len(m.cats) {
			c := m.cats[m.vocabIdx]
			if !c.Custom {
				m.err = "only custom categories can be removed"
				return m, nil
			}
			m.ov = oConfirm
			m.confirmT = fmt.Sprintf("Remove '%s' with all of its %d words?", c.DisplayName(), c.Words)
			m.pending = "rmcat"
		}
	case "R":
		if m.vocabIdx < len(m.cats) {
			c := m.cats[m.vocabIdx]
			m.ov = oConfirm
			m.confirmT = fmt.Sprintf("Reset progress for %d words in '%s'?", c.Words, c.DisplayName())
			m.pending = "rscat"
		}
	case "D":
		if m.vocabIdx < len(m.cats) {
			c := m.cats[m.vocabIdx]
			if !c.Custom {
				m.err = "only custom categories can be cleared"
				return m, nil
			}
			m.ov = oConfirm
			m.confirmT = fmt.Sprintf("Remove all %d words from '%s'?", c.Words, c.DisplayName())
			m.pending = "clearcat"
		}
	case "esc":
		m.screen = sLearn
		m.menuIdx = 0
		return m, m.loadMain()
	}
	_ = msg
	return m, nil
}

func (m Model) searchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch k {
	case "esc":
		m.searchOn = false
		return m, nil
	case "enter":
		m.searchOn = false
		m.vocabMode = 1
		m.wlIdx = 0
		m.wordListTitle = "search: " + m.search
		m.loading = "words"
		return m, m.loadWords("search:"+m.search, m.search, "", 200)
	case "backspace":
		m.search = chop(m.search)
	default:
		if isRuneKey(k) {
			m.search += k
		}
	}
	return m, nil
}

func (m Model) wordKey(k string) (tea.Model, tea.Cmd) {
	if m.word == nil {
		m.screen = sVocab
		return m, nil
	}
	w := m.word.Text
	act := "act:" + w + ":" + k
	switch k {
	case "esc":
		if m.wordReturn != 0 {
			m.screen = m.wordReturn
			m.wordReturn = 0
			return m, nil
		}
		m.screen = sVocab
		return m, nil
	case "g", "m", "t", "r", "p":
		if m.acted[act] {
			m.notice = "already queued for this word"
			return m, nil
		}
		m.acted[act] = true
		switch k {
		case "g":
			m.enqueue(queue.Intent{Op: "grade", Word: w, Mode: "rec", Result: "ok"})
		case "m":
			m.enqueue(queue.Intent{Op: "grade", Word: w, Mode: "rec", Result: "fail"})
		case "t":
			m.enqueue(queue.Intent{Op: "triage", Word: w, Decision: "learn"})
		case "r":
			m.enqueue(queue.Intent{Op: "enroll", Word: w})
			m.notice = "Memorize this word again: queued"
		case "p":
			m.enqueue(queue.Intent{Op: "postpone", Word: w})
			m.notice = "Show this word later: queued"
		}
	case "k":
		m.ov = oConfirm
		m.confirmT = "Mark '" + w + "' as Already known?"
		m.pending = "triage-known"
		m.pendingWord = w
	case "X":
		m.ov = oConfirm
		m.confirmT = "Are you sure you want to remove this word?"
		m.pending = "word-remove"
		m.pendingWord = w
	case "R":
		m.ov = oConfirm
		m.confirmT = "Reset progress for this word?"
		m.pending = "word-reset"
		m.pendingWord = w
	case "e":
		m.wordEx = !m.wordEx
	}
	return m, nil
}

func (m Model) syncKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "p":
		cli, app := m.cli, m.appID
		m.loading = "pull"
		return m, func() tea.Msg {
			r, err := cli.Pull(app)
			if err != nil {
				return replayMsg{err: err}
			}
			_ = r
			return replayMsg{out: "pulled"}
		}
	case "w":
		if len(m.q.Items) == 0 {
			m.notice = "queue empty"
			return m, nil
		}
		dirty := slices.ContainsFunc(m.syncRows, func(r rwcore.StatusRow) bool {
			return r.State == "dirty"
		})
		if dirty {
			m.err = "DIRTY_SOURCE: pull first"
			return m, nil
		}
		m.ov = oConfirm
		m.confirmT = fmt.Sprintf("write %d intents? (snapshot first)", len(m.q.Items))
		m.pending = "write"
	case "r":
		cli, app := m.cli, m.appID
		m.loading = "replay"
		return m, func() tea.Msg {
			b, err := cli.Replay(app, false)
			if err != nil {
				return replayMsg{err: err}
			}
			return replayMsg{out: string(b)}
		}
	case "R":
		m.ov = oConfirm
		m.confirmT = "replay --apply? (after wipe only)"
		m.pending = "replay"
	case "a":
		_ = m.q.Clear()
		m.q = queue.Load(m.cfg.QueuePath)
		m.notice = "queue aborted"
	case "o":
		m.ov = oOrphans
	case "D":
		if len(m.orphans) == 0 {
			m.notice = "shelf empty"
			return m, nil
		}
		m.ov = oConfirm
		m.confirmT = fmt.Sprintf("discard %d shelved orphans?", len(m.orphans))
		m.pending = "drop-orphans"
	case "esc", "enter":
		m.screen = sLearn
		return m, m.loadMain()
	}
	return m, nil
}

func (m Model) overlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch m.ov {
	case oQuit:
		switch k {
		case "w":
			if len(m.q.Items) == 0 {
				return m, tea.Quit
			}
			m.ov = oConfirm
			m.confirmT = fmt.Sprintf("write %d intents? (snapshot first)", len(m.q.Items))
			m.pending = "write-quit"
			m.pendingWord = ""
		case "q":
			return m, tea.Quit
		case "esc":
			m.ov = oNone
		}
	case oConfirm:
		switch k {
		case "enter", "y":
			pending := m.pending
			word := m.pendingWord
			m.ov = oNone
			m.pending = ""
			m.pendingWord = ""
			switch pending {
			case "write":
				m.loading = "write"
				return m, m.doWrite()
			case "write-quit":
				m.loading = "write"
				m.quitAfterWrite = true
				return m, m.doWrite()
			case "triage-known":
				m.enqueue(queue.Intent{Op: "triage", Word: word, Decision: "known"})
				if m.screen == sSession {
					m.sess.ok++
					m.advance()
				}
			case "word-remove":
				m.enqueue(queue.Intent{Op: "remove", Word: word})
			case "word-reset":
				m.enqueue(queue.Intent{Op: "reset", Word: word})
			case "selectall":
				cli, app := m.cli, m.appID
				m.loading = "session"
				return m, func() tea.Msg {
					if _, err := cli.SelectAll(app, true); err != nil {
						return selectAllMsg{err}
					}
					if _, err := cli.Categories(app); err != nil {
						return selectAllMsg{err}
					}
					return selectAllMsg{}
				}
			case "drop-orphans":
				m.orphans = nil
				_ = os.Remove(orphanPath(m.cfg.DataDir))
				m.notice = "orphan shelf discarded"
			case "replay":
				cli, app := m.cli, m.appID
				m.loading = "replay"
				return m, func() tea.Msg {
					b, err := cli.Replay(app, true)
					if err != nil {
						return replayMsg{err: err}
					}
					return replayMsg{out: string(b)}
				}
			case "import":
				cli, app := m.cli, m.appID
				file := strings.TrimSpace(m.impF[0])
				cat := strings.TrimSpace(m.impF[1])
				m.loading = "import"
				return m, func() tea.Msg {
					if _, err := cli.ImportCsv(app, cat, file, ""); err != nil {
						return importMsg{err}
					}
					return importMsg{nil}
				}
			case "rmcat", "rscat", "clearcat":
				cli, app := m.cli, m.appID
				cat := ""
				if m.vocabIdx < len(m.cats) {
					cat = m.cats[m.vocabIdx].ID
				}
				action := map[string]string{"rmcat": "remove", "rscat": "reset", "clearcat": "clear"}[pending]
				m.loading = "category"
				return m, func() tea.Msg {
					if _, err := cli.CategoryAdmin(app, action, cat); err != nil {
						return catsMsg{err: err}
					}
					cats, err := cli.Categories(app)
					return catsMsg{cats, err}
				}
			}
		case "esc", "n":
			m.ov = oNone
			m.pending = ""
			m.pendingWord = ""
			m.loading = ""
		}
	case oHelp, oOrphans, oAbout:
		if k == "esc" || k == "?" || k == "enter" {
			m.ov = oNone
			return m, nil
		}
		if k == "q" && m.ov != oOrphans {
			m.ov = oNone
			return m, nil
		}
	}
	if m.ov == oGoal {
		return m.goalKey(msg)
	}
	return m, nil
}

func (m Model) addKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	for len(m.addF) < 4 {
		m.addF = append(m.addF, "")
	}
	switch k {
	case "esc":
		m.screen = sVocab
		return m, nil
	case "enter":
		if m.addIdx < 4 {
			m.addIdx++
			return m, nil
		}
		return m.submitAdd()
	case "tab", "down":
		m.addIdx = (m.addIdx + 1) % 5
	case "shift+tab", "up":
		m.addIdx = (m.addIdx + 4) % 5
	case " ":
		if m.addIdx == 4 {
			m.addEnroll = !m.addEnroll
			return m, nil
		}
		m.addF[m.addIdx] += " "
	case "backspace":
		if m.addIdx < 4 {
			m.addF[m.addIdx] = chop(m.addF[m.addIdx])
		}
	default:
		if isRuneKey(k) && m.addIdx < 4 {
			m.addF[m.addIdx] += k
		}
	}
	return m, nil
}

func (m Model) statsKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "g":
		m.goalTitle = "How many new words do you want to learn per day?"
		m.goalInput = ""
		m.ov = oGoal
	case "esc":
		m.screen = sLearn
	}
	return m, nil
}

func (m Model) menuKey(k string) (tea.Model, tea.Cmd) {
	rows := 5
	switch k {
	case "j", "down":
		m.menuIdx = min(m.menuIdx+1, rows-1)
	case "k", "up":
		m.menuIdx = max(m.menuIdx-1, 0)
	case "enter":
		switch m.menuIdx {
		case 0:
			m.screen = sStats
			return m, m.loadMain()
		case 1:
			m.screen = sSettings
			m.menuIdx = 0
			return m, nil
		case 2:
			m.screen = sImport
			m.impF = []string{"", ""}
			m.impIdx = 0
			return m, nil
		case 3:
			m.ov = oHelp
			return m, nil
		case 4:
			m.ov = oAbout
			return m, nil
		}
	case "esc":
		m.screen = sLearn
		m.menuIdx = 0
		return m, nil
	}
	return m, nil
}

func (m Model) settingsKey(k string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	_ = msg
	rows := 8
	switch k {
	case "j", "down":
		m.setIdx = min(m.setIdx+1, rows-1)
		return m, nil
	case "k", "up":
		m.setIdx = max(m.setIdx-1, 0)
		return m, nil
	case " ", "enter":
		m.toggleSetting()
		return m, nil
	case "g":
		m.goalTitle = "How many new words do you want to learn per day?"
		m.goalInput = ""
		m.ov = oGoal
		return m, nil
	case "esc":
		m.screen = sMenu
		m.menuIdx = 1
		return m, nil
	}
	return m, nil
}

func (m *Model) toggleSetting() {
	switch m.setIdx {
	case 0:
		m.prefs.ShowTranscription = !m.prefs.ShowTranscription
	case 1:
		m.prefs.NewFirst = cycleLang(m.prefs.NewFirst)
	case 2:
		m.prefs.ReviewFirst = cycleLang(m.prefs.ReviewFirst)
	case 3:
		m.prefs.Guess = !m.prefs.Guess
	case 4:
		m.prefs.Keyboard = !m.prefs.Keyboard
	case 5:
		m.prefs.RevealAtOnce = !m.prefs.RevealAtOnce
	case 6:
		if m.prefs.ReviewFrom == "chosen" {
			m.prefs.ReviewFrom = "all"
		} else {
			m.prefs.ReviewFrom = "chosen"
		}
	case 7:
		return
	}
	if err := m.prefs.Save(); err != nil {
		m.err = err.Error()
	}
}

func cycleLang(v string) string {
	switch v {
	case "target":
		return "native"
	case "native":
		return "random"
	default:
		return "target"
	}
}

func (m Model) importKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	for len(m.impF) < 2 {
		m.impF = append(m.impF, "")
	}
	switch k {
	case "esc":
		m.screen = sMenu
		m.menuIdx = 2
		return m, nil
	case "enter":
		if m.impIdx < 1 {
			m.impIdx++
			return m, nil
		}
		return m.submitImport()
	case "tab", "down":
		m.impIdx = (m.impIdx + 1) % 2
	case "shift+tab", "up":
		m.impIdx = (m.impIdx + 1) % 2
	case "backspace":
		m.impF[m.impIdx] = chop(m.impF[m.impIdx])
	default:
		if isRuneKey(k) {
			m.impF[m.impIdx] += k
		}
	}
	return m, nil
}

func (m Model) submitImport() (tea.Model, tea.Cmd) {
	file := strings.TrimSpace(m.impF[0])
	cat := strings.TrimSpace(m.impF[1])
	if file == "" || cat == "" {
		m.err = "file and category required"
		return m, nil
	}
	m.ov = oConfirm
	m.confirmT = fmt.Sprintf("import words into '%s'?", cat)
	m.pending = "import"
	return m, nil
}

func (m Model) addCatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	for len(m.catF) < 2 {
		m.catF = append(m.catF, "")
	}
	switch k {
	case "esc":
		m.screen = sVocab
		return m, nil
	case "enter":
		if m.catIdx < 1 {
			m.catIdx++
			return m, nil
		}
		return m.submitAddCat()
	case "tab", "down", "up", "shift+tab":
		m.catIdx = (m.catIdx + 1) % 2
	case "backspace":
		m.catF[m.catIdx] = chop(m.catF[m.catIdx])
	default:
		if isRuneKey(k) {
			m.catF[m.catIdx] += k
		}
	}
	return m, nil
}

func (m Model) submitAddCat() (tea.Model, tea.Cmd) {
	id := strings.TrimSpace(m.catF[0])
	name := strings.TrimSpace(m.catF[1])
	if id == "" || name == "" {
		m.err = "Both title and id are required"
		return m, nil
	}
	cli, app := m.cli, m.appID
	m.screen = sVocab
	m.loading = "category"
	return m, func() tea.Msg {
		if _, err := cli.AddCategory(app, id, name); err != nil {
			return catsMsg{err: err}
		}
		cats, err := cli.Categories(app)
		return catsMsg{cats, err}
	}
}

func (m Model) goalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch k {
	case "esc":
		m.ov = oNone
		m.goalInput = ""
		if m.obStep == 2 {
			m.finishOnboarding()
			m.screen = sLearn
		}
		return m, nil
	case "enter":
		return m.submitGoal()
	case "backspace":
		m.goalInput = chop(m.goalInput)
	default:
		if len(k) == 1 && k[0] >= '0' && k[0] <= '9' && len(m.goalInput) < 4 {
			m.goalInput += k
		}
	}
	return m, nil
}

func (m Model) submitGoal() (tea.Model, tea.Cmd) {
	g, _ := strconv.ParseInt(m.goalInput, 10, 64)
	if g <= 0 {
		m.err = "goal must be a positive number"
		return m, nil
	}
	cli, app := m.cli, m.appID
	m.ov = oNone
	m.goalInput = ""
	m.loading = "goal"
	if m.obStep == 2 {
		m.finishOnboarding()
	}
	return m, func() tea.Msg {
		if _, err := cli.Goal(app, &g); err != nil {
			return statsMsg{err: err}
		}
		return m.loadMain()()
	}
}

func (m Model) submitAdd() (tea.Model, tea.Cmd) {
	word := strings.TrimSpace(m.addF[0])
	if word == "" {
		m.err = "word required"
		return m, nil
	}
	var tr []string
	if strings.TrimSpace(m.addF[2]) != "" {
		tr = append(tr, "RUS="+strings.TrimSpace(m.addF[2]))
	}
	if strings.TrimSpace(m.addF[3]) != "" {
		tr = append(tr, "ENG="+strings.TrimSpace(m.addF[3]))
	}
	if len(tr) == 0 {
		m.err = "RUS or ENG required"
		return m, nil
	}
	it := queue.Intent{Op: "add", Word: word, Tr: tr, Enroll: m.addEnroll}
	it.Transcription = strings.TrimSpace(m.addF[1])
	m.enqueue(it)
	m.screen = sVocab
	return m, nil
}
