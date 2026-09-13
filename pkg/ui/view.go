package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"reword-tui/pkg/rwcore"
)

func (m Model) View() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	cw := min(max(w-4, 20), 76)
	var b strings.Builder
	if m.screen == sSession && m.sess.cur != nil && zenOn(m) {
		b.WriteString(center(cw, m.viewCard()))
	} else {
		b.WriteString(center(cw, m.viewHeader(cw)))
		b.WriteString("\n")
		b.WriteString(center(cw, faint.Render(strings.Repeat("─", cw))))
		b.WriteString("\n")
		b.WriteString(center(cw, m.viewBody(cw)))
		b.WriteString("\n")
		b.WriteString(center(cw, faint.Render(strings.Repeat("─", cw))))
		b.WriteString("\n")
		b.WriteString(center(cw, m.viewFooter()))
	}
	out := b.String()
	if m.ov != oNone {
		out += "\n" + center(cw, m.viewOverlay(cw))
	}
	if m.err != "" {
		out += "\n" + center(cw, red.Render("! "+m.err))
	} else if m.notice != "" {
		out += "\n" + center(cw, green.Render(m.notice))
	} else if m.loading != "" {
		out += "\n" + center(cw, dim.Render("… "+m.loading))
	}
	return out
}

func zenOn(m Model) bool { return m.sess.zen }

func center(cw int, s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if n := lipgloss.Width(line); n < cw {
			line = strings.Repeat(" ", (cw-n)/2) + line
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (m Model) viewHeader(cw int) string {
	left := "reword"
	if m.appID != "" {
		left += " · " + m.appID
	}
	dot := faint.Render("●")
	for _, r := range m.syncRows {
		if r.App == m.appID || m.appID == "" {
			switch r.State {
			case "clean":
				dot = green.Render("●")
			case "dirty":
				dot = red.Render("●")
			default:
				dot = yellow.Render("●")
			}
		}
	}
	stale := ""
	if i := slices.IndexFunc(m.apps, func(a rwcore.App) bool { return a.ID == m.appID }); i >= 0 {
		stale = " " + yellow.Render("[stale "+ageStr(m.apps[i].MtimeSecs)+"]")
	}
	learned, goal, due := "—", "", 0
	if m.today != nil {
		learned = fmt.Sprint(m.today.Learned)
	}
	if m.stats != nil && m.stats.Settings.DailyGoal != nil {
		goal = "/" + *m.stats.Settings.DailyGoal
	}
	due = len(m.due)
	right := fmt.Sprintf("%s%s · due %d", learned, goal, due)
	if len(m.q.Items) > 0 {
		right += fmt.Sprintf(" · +%d queued", len(m.q.Items))
	}
	gap := cw - lipgloss.Width(left+"  "+right) - 4
	if gap < 1 {
		gap = 1
	}
	return left + " " + dot + stale + strings.Repeat(" ", gap) + right
}

func (m Model) viewFooter() string {
	switch m.screen {
	case sPicker:
		return "h/l move · 1-9 pick · enter open · q quit"
	case sLearn:
		return "q quit · enter open · c cats · 2 vocab · 3 menu · s sync · ? help"
	case sSession:
		if m.sess.typing {
			return "type answer · enter check · esc back"
		}
		if m.sess.cur != nil && !m.sess.cur.done {
			return "space show · g got it · m missed it · 1-4 pick · e card · z zen · esc back"
		}
		return "enter next · e card · esc back · q quit"
	case sVocab:
		if m.searchOn {
			return "type filter · enter apply · esc cancel"
		}
		if m.vocabMode == 1 {
			return "j/k move · enter card · esc back · q quit"
		}
		if m.obStep == 1 {
			return "space toggle · enter done · You will be able to change your selection at any time"
		}
		return "j/k move · space toggle · enter words · / search · a add · C category · X del · R reset · q back"
	case sWord:
		return "g got it · m missed it · r again · p later · R reset · X remove · e examples · esc back"
	case sStats:
		return "g goal · 3 menu · q back · s sync"
	case sMenu:
		return "j/k move · enter open · esc back · q quit"
	case sSettings:
		return "j/k move · space toggle · g goal · esc back"
	case sImport:
		return "tab field · enter import · esc cancel"
	case sAddCat:
		return "tab field · enter add · esc cancel"
	case sSync:
		return "p pull · w write · r dry-run · R apply · o orphans · D drop · a abort · esc back"
	case sAdd:
		return "tab field · space toggle · enter next/submit · esc cancel"
	}
	return "q quit · ? help"
}

func (m Model) viewBody(cw int) string {
	switch m.screen {
	case sPicker:
		return m.viewPicker()
	case sLearn:
		return m.viewLearn()
	case sSession:
		return m.viewSession()
	case sVocab:
		return m.viewVocab(cw)
	case sWord:
		return m.viewWord()
	case sStats:
		return m.viewStats()
	case sSync:
		return m.viewSync()
	case sAdd:
		return m.viewAdd()
	case sMenu:
		return m.viewMenu()
	case sSettings:
		return m.viewSettings()
	case sImport:
		return m.viewImport()
	case sAddCat:
		return m.viewAddCat()
	}
	return ""
}

func (m Model) viewPicker() string {
	var b strings.Builder
	b.WriteString(center(76, accent.Render("reword")) + "\n")
	b.WriteString(center(76, dim.Render("Spaced repetition in terminal")) + "\n\n")
	for i, a := range m.apps {
		marker := " "
		style := box
		if i == m.appIdx {
			marker = "▸"
			style = abox
		}
		meta, ok := m.appMeta[a.ID]
		dueLine := "due …"
		wdLine := "… wds"
		if ok {
			dueLine = fmt.Sprintf("due %d", meta.due)
			wdLine = fmt.Sprintf("%d wds", meta.words)
		}
		ts := time.Unix(int64(a.MtimeSecs), 0).Format("2 Jan")
		size := fmt.Sprintf("%d MB", a.SizeBytes/1048576)
		block := fmt.Sprintf("%s %d %s\n  %s\n  %s\n%s · %s", marker, a.N, a.ID, wdLine, dueLine, size, ts)
		b.WriteString(style.Render(block) + "  ")
	}
	b.WriteString("\n\nh/l move · 1-9 pick · enter open")
	return b.String()
}

func (m Model) viewLearn() string {
	var b strings.Builder
	b.WriteString(center(76, accent.Render("reword")) + "\n")
	b.WriteString(center(76, dim.Render("Spaced repetition")) + "\n\n")
	chosen, total := 0, len(m.cats)
	names := []string{}
	for _, c := range m.cats {
		if m.catSel[c.ID] {
			chosen++
			if len(names) < 2 {
				names = append(names, c.DisplayName())
			}
		}
	}
	_ = total
	catLine := fmt.Sprintf("%d categories chosen", chosen)
	if len(names) > 0 {
		catLine += " · " + strings.Join(names, " · ")
		if chosen > 2 {
			catLine += "…"
		}
	}
	b.WriteString(dim.Render(catLine) + "  [c] change\n")
	b.WriteString(faint.Render(strings.Repeat("─", 60)) + "\n")
	learned, goal := int64(0), ""
	if m.today != nil {
		learned = m.today.Learned
		if m.today.Goal != nil {
			goal = " of " + fmt.Sprint(*m.today.Goal)
		}
	}
	if goal == "" && m.stats != nil && m.stats.Settings.DailyGoal != nil {
		goal = " of " + *m.stats.Settings.DailyGoal
	}
	oldest := ""
	if len(m.due) > 0 {
		oldest = " · oldest " + overdueStr(m.due[0].OverdueSecs) + " overdue"
	}
	rows := []string{
		fmt.Sprintf("Learn new words\n  Learned today: %d%s", learned, goal),
		fmt.Sprintf("Review words (%d)\n  Words to review: %d%s", len(m.due), len(m.due), oldest),
		"Mixed mode\n  New + review interleaved",
	}
	for i, r := range rows {
		lines := strings.Split(r, "\n")
		if i == m.menuIdx {
			lines[0] = sel.Render("▸ " + lines[0])
		} else {
			lines[0] = fg.Render("  " + lines[0])
		}
		lines[1] = "  " + dim.Render(lines[1])
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", 60)) + "\n")
	b.WriteString(m.viewDots() + "\n")
	return b.String()
}

func (m Model) viewDots() string {
	if m.today == nil {
		return dim.Render("no history yet")
	}
	today := time.Now()
	var dots []string
	for i := 6; i >= 0; i-- {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		if slices.Contains(m.today.ActiveDates, d) {
			dots = append(dots, green.Render("●"))
		} else {
			dots = append(dots, faint.Render("○"))
		}
	}
	return strings.Join(dots, " ── ") + dim.Render(fmt.Sprintf("   Current %d · Best %d", m.today.StreakCur, m.today.StreakBest))
}

func (m Model) viewSession() string {
	if m.loading != "" && m.sess.cur == nil {
		return dim.Render("loading session…")
	}
	if m.sess.cur == nil {
		if m.sess.mode == 1 {
			return dim.Render("There are no new words in the chosen categories")
		}
		return dim.Render("There are no words for review in the chosen categories")
	}
	var b strings.Builder
	b.WriteString(m.viewModeline())
	b.WriteString("\n")
	total := len(m.sess.units) + m.sess.ok + m.sess.fail
	if m.sess.mode == 1 {
		total = len(m.sess.learn)
	}
	done := m.sess.ok + m.sess.fail
	b.WriteString(dim.Render(fmt.Sprintf("session %d/%d · ✓%d ✗%d", done, total, m.sess.ok, m.sess.fail)))
	if len(m.q.Items) > 0 {
		b.WriteString(dim.Render(fmt.Sprintf(" · +%d queued", len(m.q.Items))))
	}
	b.WriteString("\n\n")
	b.WriteString(m.viewCard())
	return b.String()
}

func (m Model) viewModeline() string {
	rev := dim.Render(fmt.Sprintf("Review (%d)", len(m.sess.units)))
	lrn := dim.Render(fmt.Sprintf("Learning (%d)", len(m.sess.learn)))
	if m.sess.mode == 0 {
		rev = sel.Render(fmt.Sprintf("▸ Review (%d)", len(m.sess.units)))
	} else if m.sess.mode == 1 {
		lrn = sel.Render(fmt.Sprintf("▸ Learning (%d)", len(m.sess.learn)))
	} else {
		rev = yellow.Render("≋ Mixed")
	}
	up := ""
	seen := map[string]bool{}
	if m.sess.cur != nil {
		seen[m.sess.cur.word] = true
	}
	shown := 0
	for _, u := range m.sess.units {
		if shown >= 3 {
			break
		}
		if seen[u.word] {
			continue
		}
		seen[u.word] = true
		up += dim.Render(fmt.Sprintf("\n· %s · %s", u.word, overdueStr(u.overdue)))
		shown++
	}
	if up != "" {
		up = dim.Render("\nnext up") + up
	}
	return rev + "  " + lrn + up
}

func (m Model) viewCard() string {
	c := m.sess.cur
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(dim.Render(cardTitle(c)) + "\n")
	switch c.kind {
	case cR1:
		b.WriteString(bold.Render(c.prompt) + "\n")
		if c.tr != "" {
			b.WriteString(dim.Render(c.tr) + "\n")
		}
		if c.reveal || c.done {
			b.WriteString(green.Render(c.native) + "\n")
			if c.example != "" {
				b.WriteString(dim.Render(c.example) + "\n")
			}
		}
		if c.done {
			if c.wasOk {
				b.WriteString(green.Render("✓ Got it") + "\n")
			} else {
				b.WriteString(red.Render("✗ Missed it") + "\n")
			}
		}
	case cR2:
		b.WriteString(dim.Render("how do you say:") + "\n")
		b.WriteString(bold.Render(c.prompt) + "\n")
		for i, ch := range c.choices {
			line := fmt.Sprintf("  %d  %s", i+1, ch)
			if c.done && i == c.answer {
				line = green.Render("▸ " + line)
			} else if c.done {
				line = dim.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if c.done {
			if c.wasOk {
				b.WriteString(green.Render("✓") + "\n")
			} else {
				b.WriteString(red.Render("✗ answer: "+c.choices[c.answer]) + "\n")
			}
		}
	case cR3:
		b.WriteString(dim.Render("type in the target language:") + "\n")
		b.WriteString(bold.Render(c.prompt) + "\n")
		if m.sess.typing || c.done {
			b.WriteString("› " + m.sess.input + "▌\n")
		}
		if !c.done {
			b.WriteString(faint.Render(fmt.Sprintf("attempts left: %d", c.attempts)) + "\n")
		} else if c.wasOk {
			b.WriteString(green.Render("✓ Got it") + "\n")
		} else {
			b.WriteString(red.Render("✗ Missed it") + "\n")
		}
	case cL1, cL1b:
		b.WriteString(bold.Render(c.prompt) + "\n")
		if c.tr != "" {
			b.WriteString(dim.Render(c.tr) + "\n")
		}
		if c.native != "" {
			b.WriteString(c.native + "\n")
		}
		if c.example != "" {
			b.WriteString(dim.Render(c.example) + "\n")
		}
		if c.done {
			b.WriteString(green.Render("✓ queued") + "\n")
		}
	case cL2:
		b.WriteString(bold.Render(c.prompt) + " → choose translation\n")
		for i, ch := range c.choices {
			line := fmt.Sprintf("  %d  %s", i+1, ch)
			if c.done && i == c.answer {
				line = green.Render("▸ " + line)
			} else if c.done {
				line = dim.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if c.done {
			if c.wasOk {
				b.WriteString(green.Render("✓") + "\n")
			} else {
				b.WriteString(red.Render("✗ answer: "+c.choices[c.answer]) + "\n")
			}
		}
	}
	b.WriteString(faint.Render("[" + c.hint() + "]"))
	return abox.Render(b.String())
}

func (m Model) viewVocab(cw int) string {
	var b strings.Builder
	title := "Vocabulary · " + m.appID
	if m.searchOn {
		title += "  [/" + m.search + "＿]"
	} else {
		title += dim.Render("   [/] search")
	}
	b.WriteString(title + "\n")
	if m.vocabMode == 1 {
		b.WriteString(dim.Render(m.wordListTitle) + "\n")
		for i, w := range m.vocabWords {
			line := fmt.Sprintf("%s — %s · S%d/S%d", w.Text, pickNative(w, m.nativeLang()), w.Recognition.Step, w.Reproduction.Step)
			if i == m.menuIdx {
				b.WriteString(sel.Render("▸ "+line) + "\n")
			} else {
				b.WriteString("  " + line + "\n")
			}
			if i > 60 {
				b.WriteString(dim.Render(fmt.Sprintf("… %d more", len(m.vocabWords)-i-1)) + "\n")
				break
			}
		}
		_ = cw
		return b.String()
	}
	for i, c := range m.cats {
		mark := "○"
		if m.catSel[c.ID] {
			mark = green.Render("●")
		} else {
			mark = faint.Render("○")
		}
		pct := m.catPct[c.ID]
		if pct == "" {
			pct = "—"
		}
		line := fmt.Sprintf("%s %s · %d · %s", mark, c.DisplayName(), c.Words, pct)
		if i == m.vocabIdx {
			b.WriteString(sel.Render("▸ "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

func (m Model) viewWord() string {
	if m.word == nil {
		return dim.Render("no word")
	}
	w := m.word
	var b strings.Builder
	tr := ""
	if w.Transcription != nil {
		tr = "  [" + *w.Transcription + "]"
	}
	b.WriteString(bold.Render(w.Text) + dim.Render(tr) + "\n")
	nat := m.nativeLang()
	if v, ok := w.Translations[nat]; ok {
		b.WriteString(green.Render(firstLine(v)) + "\n")
	}
	for _, k := range slices.Sorted(maps.Keys(w.Translations)) {
		if k == nat {
			continue
		}
		b.WriteString(dim.Render(firstLine(w.Translations[k])) + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", 40)) + "\n")
	raw := ""
	if v, ok := w.Examples[nat]; ok {
		raw = v
	}
	if raw == "" {
		for _, k := range slices.Sorted(maps.Keys(w.Examples)) {
			if w.Examples[k] != "" {
				raw = w.Examples[k]
				break
			}
		}
	}
	pairs := parseExamplesFull(raw)
	shown := pairs
	if !m.wordEx && len(pairs) > 2 {
		shown = pairs[:2]
	}
	for _, p := range shown {
		b.WriteString("e.g. " + p[0])
		if p[1] != "" {
			b.WriteString(" — " + dim.Render(p[1]))
		}
		b.WriteString("\n")
	}
	if !m.wordEx && len(pairs) > 2 {
		b.WriteString(dim.Render(fmt.Sprintf("[+ %d more — e]", len(pairs)-2)) + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", 40)) + "\n")
	b.WriteString(fmt.Sprintf("recognition (%s→%s):  S%d · E%.2f · F%d",
		strings.ToUpper(m.appID), m.nativeLang(), w.Recognition.Step, w.Recognition.Easiness, w.Recognition.Fails) + "  [g]ot-it [m]issed\n")
	b.WriteString(fmt.Sprintf("reproduction (%s→%s): S%d · E%.2f · F%d",
		m.nativeLang(), strings.ToUpper(m.appID), w.Reproduction.Step, w.Reproduction.Easiness, w.Reproduction.Fails) + "  [1-4] quiz\n")
	b.WriteString(faint.Render(strings.Repeat("─", 40)) + "\n")
	b.WriteString(dim.Render("history") + "\n")
	for _, e := range m.wordLog {
		kind := "got-it"
		if e.Mode == 2 {
			kind = "tested"
		}
		mark := red.Render("✗")
		if e.Queue == 2 {
			mark = green.Render("✓")
		}
		b.WriteString(fmt.Sprintf(" %s  %s %s · %dth review\n", e.Date, kind, mark, e.Step+1))
	}
	return b.String()
}

func (m Model) viewStats() string {
	var b strings.Builder
	b.WriteString(bold.Render("Stats · "+m.appID+" · 7 days") + "\n")
	b.WriteString(m.viewDots() + "\n")
	b.WriteString(faint.Render(strings.Repeat("─", 50)) + "\n")
	var learned, reviewed, memorizing, known, mastered int64
	var goal *int64
	left := len(m.due)
	if m.today != nil {
		learned = m.today.Learned
		reviewed = m.today.Reviewed
		memorizing = m.today.Memorizing
		known = m.today.Known
		mastered = m.today.Mastered
		goal = m.today.Goal
	}
	if goal != nil {
		b.WriteString(green.Render(fmt.Sprintf("Memorized today: %d of %d", learned, *goal)) + "\n")
	} else {
		b.WriteString(dim.Render("Daily goal is not set  [g] set") + "\n")
	}
	b.WriteString(fmt.Sprintf("New words memorized: %d\n", learned))
	b.WriteString(fmt.Sprintf("Words being memorized: %d\n", memorizing))
	b.WriteString(fmt.Sprintf("Reviewed (unique): %d · due left %d\n", reviewed, left))
	b.WriteString(fmt.Sprintf("Already known: %d\n", known))
	b.WriteString(fmt.Sprintf("Mastered: %d\n", mastered))
	if goal != nil {
		b.WriteString(dim.Render("[g] adjust daily goal") + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", 50)) + "\n")
	b.WriteString(m.viewBars() + "\n")
	return b.String()
}

func (m Model) viewBars() string {
	if m.today == nil {
		return ""
	}
	active := m.today.ActiveDates
	glyphs := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	now := time.Now()
	todayStr := now.Format("2006-01-02")
	vals := make([]int, 7)
	for i := range vals {
		d := now.AddDate(0, 0, i-6).Format("2006-01-02")
		switch {
		case d == todayStr:
			vals[i] = int(m.today.Learned + m.today.Reviewed)
		case slices.Contains(active, d):
			vals[i] = 1
		}
	}
	mx := max(1, slices.Max(vals))
	bars := make([]string, 0, 7)
	for _, v := range vals {
		idx := min(v*7/mx, 7)
		if v == 0 {
			bars = append(bars, faint.Render(glyphs[0]))
		} else {
			bars = append(bars, pink.Render(glyphs[idx]))
		}
	}
	return strings.Join(bars, " ") + dim.Render("  7d · counts before today are presence-only")
}

func (m Model) viewSync() string {
	var b strings.Builder
	b.WriteString(bold.Render("Sync · "+m.appID) + "\n")
	for _, r := range m.syncRows {
		state := green.Render("● match (clean)")
		if r.State == "dirty" {
			state = red.Render("✖ drift (DIRTY_SOURCE)")
		} else if r.State == "untracked" {
			state = yellow.Render("? untracked")
		}
		b.WriteString(fmt.Sprintf("iCloud file  %s\nfingerprint  %s\n", dim.Render(shortPath(r.Backup)), state))
	}
	b.WriteString(fmt.Sprintf("op-queue     %d intents queued · oplog tail %d\n", len(m.q.Items), len(m.oplog)))
	b.WriteString(fmt.Sprintf("orphans      %d shelved\n", len(m.orphans)))
	b.WriteString(faint.Render(strings.Repeat("─", 50)) + "\n")
	b.WriteString("[p] pull   [w] write queue now\n[r] replay dry-run   [R] replay --apply\n[o] orphans   [a] abort queue\n")
	if m.replayPlan != "" {
		out := m.replayPlan
		if len(out) > 800 {
			out = out[:800] + "…"
		}
		b.WriteString(faint.Render(strings.Repeat("─", 50)) + "\n")
		b.WriteString(dim.Render(out) + "\n")
	}
	if len(m.q.Items) > 0 {
		b.WriteString(faint.Render(strings.Repeat("─", 50)) + "\n")
		for _, it := range m.q.Items[:min(len(m.q.Items), 8)] {
			b.WriteString(dim.Render("· "+it.Label()) + "\n")
		}
		if len(m.q.Items) > 8 {
			b.WriteString(dim.Render(fmt.Sprintf("… +%d", len(m.q.Items)-8)) + "\n")
		}
	}
	return b.String()
}

func shortPath(p string) string {
	if len(p) > 55 {
		return "…" + p[len(p)-54:]
	}
	return p
}

func (m Model) viewMenu() string {
	rows := []string{"Statistics", "Settings", "Import words", "Help", "About"}
	var b strings.Builder
	b.WriteString(bold.Render("Menu") + "\n")
	for i, r := range rows {
		if i == m.menuIdx {
			b.WriteString(sel.Render("▸ "+r) + "\n")
		} else {
			b.WriteString("  " + r + "\n")
		}
	}
	return b.String()
}

func onoff(v bool) string {
	if v {
		return green.Render("on")
	}
	return faint.Render("off")
}

func (m Model) viewSettings() string {
	goal := "Not set"
	if m.today != nil && m.today.Goal != nil {
		goal = fmt.Sprint(*m.today.Goal)
	} else if m.stats != nil && m.stats.Settings.DailyGoal != nil {
		goal = *m.stats.Settings.DailyGoal
	}
	rows := []struct{ label, value string }{
		{"Show transcription", onoff(m.prefs.ShowTranscription)},
		{"New words first language", m.prefs.NewFirst},
		{"Review first language", m.prefs.ReviewFirst},
		{"Guessing game", onoff(m.prefs.Guess)},
		{"Keyboard input", onoff(m.prefs.Keyboard)},
		{"Show translation at once", onoff(m.prefs.RevealAtOnce)},
		{"Review words from", m.prefs.ReviewFrom},
		{"Daily goal", goal + dim.Render("  [g] adjust")},
	}
	var b strings.Builder
	b.WriteString(bold.Render("Settings") + "\n")
	for i, r := range rows {
		line := fmt.Sprintf("%s: %s", r.label, r.value)
		if i == m.setIdx {
			b.WriteString(sel.Render("▸ "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

func (m Model) viewImport() string {
	labels := []string{"file (txt, csv)", "category"}
	var b strings.Builder
	b.WriteString(bold.Render("Import words") + "\n")
	b.WriteString(dim.Render("\"word\";\"translation\" per line, UTF-8") + "\n")
	for i, l := range labels {
		val := ""
		if i < len(m.impF) {
			val = m.impF[i]
		}
		if m.impIdx == i {
			b.WriteString(sel.Render("▸ "+l+": "+val+"▌") + "\n")
		} else {
			b.WriteString(fmt.Sprintf("  %s: %s\n", dim.Render(l), val))
		}
	}
	return b.String()
}

func (m Model) viewAddCat() string {
	labels := []string{"id", "title"}
	var b strings.Builder
	b.WriteString(bold.Render("New category") + "\n")
	for i, l := range labels {
		val := ""
		if i < len(m.catF) {
			val = m.catF[i]
		}
		if m.catIdx == i {
			b.WriteString(sel.Render("▸ "+l+": "+val+"▌") + "\n")
		} else {
			b.WriteString(fmt.Sprintf("  %s: %s\n", dim.Render(l), val))
		}
	}
	return b.String()
}

func (m Model) viewAdd() string {
	labels := []string{"word", "transcription", "RUS", "ENG"}
	var b strings.Builder
	b.WriteString(bold.Render("Add word → My words (custom)") + "\n")
	for i, l := range labels {
		val := ""
		if i < len(m.addF) {
			val = m.addF[i]
		}
		if m.addIdx == i {
			val += "▌"
			b.WriteString(sel.Render("▸ "+l+": "+val) + "\n")
		} else {
			b.WriteString(fmt.Sprintf("  %s: %s\n", dim.Render(l), val))
		}
	}
	mark := "○"
	if m.addEnroll {
		mark = green.Render("◉")
	}
	if m.addIdx == 4 {
		b.WriteString(sel.Render("▸ ["+mark+"] enroll immediately") + "\n")
	} else {
		b.WriteString(fmt.Sprintf("  [%s] enroll immediately\n", mark))
	}
	return b.String()
}

func (m Model) viewOverlay(cw int) string {
	switch m.ov {
	case oQuit:
		var b strings.Builder
		b.WriteString(bold.Render("Quit") + "\n")
		b.WriteString("Your phone will NOT pick up the progress by itself.\n")
		b.WriteString("Open ReWord → Restore, or your study stays here.\n\n")
		b.WriteString(fmt.Sprintf("Queued intents: %d (not written) · Written: %d\n\n", len(m.q.Items), m.written))
		b.WriteString("[w] write queue + quit   [q] quit anyway   [esc] stay")
		return ybox.Render(b.String())
	case oConfirm:
		return ybox.Render(bold.Render("Confirm") + "\n" + m.confirmT + "\n\n[y] yes   [n] no")
	case oHelp:
		return abox.Render(m.helpText())
	case oGoal:
		return ybox.Render(bold.Render(m.goalTitle) + "\n› " + m.goalInput + "▌\n\n[enter] save   [esc] cancel")
	case oAbout:
		return abox.Render(m.aboutText())
	case oOrphans:
		var b strings.Builder
		b.WriteString(bold.Render("Orphans") + "\n")
		if len(m.orphans) == 0 {
			b.WriteString(dim.Render("shelf empty") + "\n")
		}
		for i, o := range m.orphans {
			q := ""
			if o.WordQuery != nil {
				q = *o.WordQuery
			}
			b.WriteString(fmt.Sprintf("%d  %s  %s\n", i+1, o.App, q))
			if i > 9 {
				break
			}
		}
		return abox.Render(b.String())
	}
	_ = cw
	return ""
}

func (m Model) aboutText() string {
	return "reword-tui — terminal client for ReWord backups\n" +
		"To master a word, you need to review it 6 times.\n" +
		"Time intervals between the reviews are increased\n" +
		"from 30 minutes to 2 months.\n\n" +
		"Local only: no account, no ads, no notifications.\n" +
		"Hands-free and Browse flashcards need the phone app."
}

func (m Model) helpText() string {
	switch m.screen {
	case sSession:
		return "space show · g Got it · m Missed it · 1-4 pick\n" +
			"type answer · enter check (3 attempts)\n" +
			"k Already known · l Start learning · e card · z zen\n" +
			"review: random of 5 most overdue · new: shuffled"
	case sVocab:
		return "j/k move · space toggle pool · enter words\n" +
			"/ search · a add word · C category · X remove\n" +
			"R reset progress · D clear · r refresh · esc back"
	case sSync:
		return "p pull (adopt iCloud) · w write (snapshot→apply)\n" +
			"r dry-run · R apply (after wipe) · a abort\n" +
			"write blocked while DIRTY_SOURCE"
	case sWord:
		return "Word actions: g Got it · m Missed it\n" +
			"r Memorize this word again · p Show this word later\n" +
			"R Reset progress for this word · X remove · e examples"
	default:
		return "q quit · 1 Learn · 2 Vocabulary · 3 Menu\n" +
			"s sync · r refresh · ? help · esc back"
	}
}

var _ = tea.Quit
