package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"reword-tui/pkg/rwcore"
)

func (m Model) View() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height
	if h <= 0 {
		h = 24
	}
	cw := min(max(w-4, 20), 76)
	col := lipgloss.NewStyle().Width(cw)
	if m.screen == sSession && m.sess.cur != nil && zenOn(m) {
		body := col.Render(strings.TrimRight(m.viewCard(), "\n"))
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Position(0.45), body)
	}
	header := col.Render(m.viewHeader(cw) + "\n" + faint.Render(strings.Repeat("─", cw)))
	status := col.Render(m.viewStatus(cw))
	help := col.Render(m.viewHints(cw))
	bodyH := h - lipgloss.Height(header) - lipgloss.Height(status) - lipgloss.Height(help)
	if bodyH < 1 {
		bodyH = 1
	}
	var body string
	if m.ov != oNone {
		body = m.viewOverlay(cw)
	} else {
		body = m.viewBody(cw, bodyH)
	}
	body = col.Render(strings.TrimRight(body, "\n"))
	mid := lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Position(0.45), body)
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.PlaceHorizontal(w, lipgloss.Center, header),
		mid,
		lipgloss.PlaceHorizontal(w, lipgloss.Center, status),
		lipgloss.PlaceHorizontal(w, lipgloss.Center, help),
	)
}

func (m Model) viewStatus(cw int) string {
	var s string
	switch {
	case m.err != "":
		s = bad.Render("! " + m.err)
	case m.notice != "":
		s = ok.Render(m.notice)
	case m.loading != "":
		s = dim.Render("… " + m.loading)
	default:
		return ""
	}
	return ansi.Truncate(s, cw, "…")
}

func zenOn(m Model) bool { return m.sess.zen }

type kb struct{ k, d string }

func hints(cw int, bs ...kb) string {
	sep := faint.Render(" · ")
	var parts []string
	for _, b := range bs {
		p := interactive.Render(b.k) + " " + dim.Render(b.d)
		if lipgloss.Width(strings.Join(append(parts, p), sep)) > cw {
			if len(parts) == 0 {
				return p
			}
			return strings.Join(parts, sep) + sep + faint.Render("? more")
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, sep)
}

func (m Model) viewHints(cw int) string {
	switch m.screen {
	case sPicker:
		return hints(cw, kb{"h/l", "move"}, kb{"1-9", "pick"}, kb{"enter", "open"}, kb{"q", "quit"})
	case sLearn:
		return hints(cw, kb{"enter", "open"}, kb{"c", "cats"}, kb{"2", "vocab"}, kb{"3", "menu"}, kb{"s", "sync"}, kb{"?", "help"}, kb{"q", "quit"})
	case sSession:
		if m.sess.typing {
			return hints(cw, kb{"type", "answer"}, kb{"enter", "check"}, kb{"esc", "stop"})
		}
		if m.sess.cur != nil && !m.sess.cur.done {
			return hints(cw, kb{"space", "show"}, kb{"g", "got it"}, kb{"m", "missed it"}, kb{"1-4", "pick"}, kb{"e", "card"}, kb{"z", "zen"}, kb{"esc", "back"})
		}
		return hints(cw, kb{"enter", "next"}, kb{"e", "card"}, kb{"esc", "back"}, kb{"q", "quit"})
	case sVocab:
		if m.searchOn {
			return hints(cw, kb{"/", "filter"}, kb{"enter", "apply"}, kb{"esc", "cancel"})
		}
		if m.vocabMode == 1 {
			return hints(cw, kb{"j/k", "move"}, kb{"enter", "card"}, kb{"esc", "back"}, kb{"q", "quit"})
		}
		if m.obStep == 1 {
			return hints(cw, kb{"space", "toggle"}, kb{"enter", "done"})
		}
		return hints(cw, kb{"j/k", "move"}, kb{"space", "toggle"}, kb{"enter", "words"}, kb{"/", "search"}, kb{"a", "add"}, kb{"C", "category"}, kb{"esc", "back"}, kb{"X", "remove"}, kb{"R", "reset"}, kb{"D", "clear"})
	case sWord:
		return hints(cw, kb{"g", "got it"}, kb{"m", "missed it"}, kb{"r", "again"}, kb{"p", "later"}, kb{"e", "examples"}, kb{"esc", "back"}, kb{"R", "reset"}, kb{"X", "remove"})
	case sStats:
		return hints(cw, kb{"g", "goal"}, kb{"3", "menu"}, kb{"q", "back"}, kb{"s", "sync"})
	case sMenu:
		return hints(cw, kb{"j/k", "move"}, kb{"enter", "open"}, kb{"esc", "back"}, kb{"q", "quit"})
	case sSettings:
		return hints(cw, kb{"j/k", "move"}, kb{"space", "toggle"}, kb{"g", "goal"}, kb{"esc", "back"})
	case sImport:
		return hints(cw, kb{"tab", "field"}, kb{"enter", "import"}, kb{"esc", "cancel"})
	case sAddCat:
		return hints(cw, kb{"tab", "field"}, kb{"enter", "add"}, kb{"esc", "cancel"})
	case sSync:
		return hints(cw, kb{"p", "pull"}, kb{"w", "write"}, kb{"r", "dry-run"}, kb{"R", "apply"}, kb{"o", "orphans"}, kb{"a", "abort"}, kb{"esc", "back"}, kb{"D", "drop"})
	case sAdd:
		return hints(cw, kb{"tab", "field"}, kb{"space", "toggle"}, kb{"enter", "submit"}, kb{"esc", "cancel"})
	}
	return hints(cw, kb{"q", "quit"}, kb{"?", "help"})
}

func window(n, cur, h int) (from, to int) {
	if n <= h {
		return 0, n
	}
	from = min(max(cur-h/2, 0), n-h)
	return from, from + h
}

func windowed(items []string, cur, h int) string {
	if h < 1 {
		h = 1
	}
	n := len(items)
	if n == 0 {
		return dim.Render("nothing here")
	}
	if n <= h {
		return strings.Join(items, "\n")
	}
	from, to := window(n, cur, h)
	seg := append([]string{}, items[from:to]...)
	if from > 0 {
		seg[0] = faint.Render(fmt.Sprintf("↑ %d more", from))
	}
	if to < n {
		seg[len(seg)-1] = faint.Render(fmt.Sprintf("↓ %d more", n-to))
	}
	return strings.Join(seg, "\n")
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
				dot = ok.Render("●")
			case "dirty":
				dot = bad.Render("●")
			default:
				dot = attn.Render("●")
			}
		}
	}
	stale := ""
	if i := slices.IndexFunc(m.apps, func(a rwcore.App) bool { return a.ID == m.appID }); i >= 0 {
		stale = " " + attn.Render("[stale "+ageStr(m.apps[i].MtimeSecs)+"]")
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
	leftFull := left + " " + dot
	gap := cw - lipgloss.Width(leftFull) - lipgloss.Width(stale) - lipgloss.Width(right)
	if gap < 1 && stale != "" {
		stale = ""
		gap = cw - lipgloss.Width(leftFull) - lipgloss.Width(right)
	}
	if gap < 1 {
		gap = 1
	}
	line := leftFull + stale + strings.Repeat(" ", gap) + right
	for lipgloss.Width(line) > cw && len(right) > 0 {
		right = string([]rune(right)[:len([]rune(right))-1])
		line = leftFull + stale + strings.Repeat(" ", gap) + right
	}
	if lipgloss.Width(line) > cw {
		line = left + " " + dot
	}
	// Colorize after truncation: styles are width-neutral, and if the
	// line was cut the tokens below simply won't match (stays plain).
	if due > 0 {
		line = strings.Replace(line, fmt.Sprintf("due %d", due), attn.Render(fmt.Sprintf("due %d", due)), 1)
	}
	if goal != "" {
		line = strings.Replace(line, learned+goal, learned+prog.Render(goal), 1)
	}
	return line
}

func (m Model) viewBody(cw, h int) string {
	switch m.screen {
	case sPicker:
		return m.viewPicker(cw)
	case sLearn:
		return m.viewLearn(cw)
	case sSession:
		return m.viewSession()
	case sVocab:
		return m.viewVocab(cw, h)
	case sWord:
		return m.viewWord(cw)
	case sStats:
		return m.viewStats(cw)
	case sSync:
		return m.viewSync(cw)
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

func (m Model) viewPicker(cw int) string {
	var b strings.Builder
	b.WriteString(interactive.Render("reword") + "\n")
	b.WriteString(dim.Render("Spaced repetition in terminal") + "\n\n")
	var contents []string
	for i, a := range m.apps {
		marker := " "
		if i == m.appIdx {
			marker = interactive.Render("▸")
		}
		meta, ok := m.appMeta[a.ID]
		dueLine := "due …"
		wdLine := "… wds"
		if ok {
			dueLine = fmt.Sprintf("due %d", meta.due)
			if meta.due > 0 {
				dueLine = attn.Render(dueLine)
			}
			wdLine = fmt.Sprintf("%d wds", meta.words)
		}
		ts := time.Unix(int64(a.MtimeSecs), 0).Format("2 Jan")
		size := fmt.Sprintf("%d MB", a.SizeBytes/1048576)
		contents = append(contents, fmt.Sprintf("%s %d %s\n  %s\n  %s\n%s · %s", marker, a.N, a.ID, wdLine, dueLine, size, ts))
	}
	if len(contents) == 0 {
		b.WriteString(dim.Render("no ReWord apps found"))
		return b.String()
	}
	bw := 0
	for _, c := range contents {
		for _, ln := range strings.Split(c, "\n") {
			bw = max(bw, lipgloss.Width(ln))
		}
	}
	var blocks []string
	for i, c := range contents {
		style := box
		if i == m.appIdx {
			style = abox
		}
		blocks = append(blocks, style.Width(bw).Render(c))
	}
	rowW := 0
	for _, bl := range blocks {
		rowW += lipgloss.Width(strings.Split(bl, "\n")[0])
	}
	rowW += 2 * (len(blocks) - 1)
	if len(blocks) > 3 || rowW > cw {
		b.WriteString(strings.Join(blocks, "\n"))
	} else {
		spaced := make([]string, 0, len(blocks)*2-1)
		for i, bl := range blocks {
			if i > 0 {
				spaced = append(spaced, "  ")
			}
			spaced = append(spaced, bl)
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, spaced...))
	}
	return b.String()
}

func (m Model) viewLearn(cw int) string {
	var b strings.Builder
	b.WriteString(interactive.Render("reword") + "\n")
	b.WriteString(dim.Render("Spaced repetition") + "\n\n")
	chosen := 0
	names := []string{}
	for _, c := range m.cats {
		if m.catSel[c.ID] {
			chosen++
			if len(names) < 2 {
				names = append(names, c.DisplayName())
			}
		}
	}
	catLine := fmt.Sprintf("%d categories chosen", chosen)
	if len(names) > 0 {
		catLine += " · " + strings.Join(names, " · ")
		if chosen > 2 {
			catLine += "…"
		}
	}
	b.WriteString(dim.Render(catLine) + "  [c] change\n")
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	learned := int64(0)
	goal := ""
	if m.today != nil {
		learned = m.today.Learned
		if m.today.Goal != nil {
			goal = fmt.Sprintf("%d of %d", learned, *m.today.Goal)
		}
	}
	if goal == "" && m.stats != nil && m.stats.Settings.DailyGoal != nil {
		goal = fmt.Sprintf("%d of %s", learned, *m.stats.Settings.DailyGoal)
	}
	if goal == "" {
		goal = fmt.Sprint(learned)
	}
	dueBadge := "—"
	dueDesc := "nothing due"
	if len(m.due) > 0 {
		dueBadge = fmt.Sprint(len(m.due))
		dueDesc = "oldest " + overdueStr(m.due[0].OverdueSecs) + " overdue"
	}
	type row struct{ title, desc, badge string }
	rows := []row{
		{"Learn new words", "learned today", goal},
		{"Review words", dueDesc, dueBadge},
		{"Mixed mode", "new + review interleaved", "—"},
	}
	badgeW := lipgloss.NewStyle().Width(8)
	for i, r := range rows {
		var badge string
		switch i {
		case 0:
			badge = prog.Render(r.badge)
		case 1:
			badge = faint.Render(r.badge)
			if len(m.due) > 0 {
				badge = attn.Render(r.badge)
			}
		default:
			badge = faint.Render(r.badge)
		}
		title := "  " + r.title
		if i == m.menuIdx {
			title = "▸ " + r.title
		}
		line := lipgloss.NewStyle().Width(cw-8).Render(title) + badgeW.Render(badge)
		if i == m.menuIdx {
			line = sel.Render(line)
		} else {
			line = fg.Render(line)
		}
		b.WriteString(line + "\n")
		b.WriteString("  " + dim.Render(r.desc) + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
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
			dots = append(dots, prog.Render("●"))
		} else {
			dots = append(dots, faint.Render("○"))
		}
	}
	return strings.Join(dots, " ── ") + dim.Render("   Current ") + prog.Render(fmt.Sprint(m.today.StreakCur)) + dim.Render(" · Best ") + prog.Render(fmt.Sprint(m.today.StreakBest))
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
	// One word can yield several grades (triage + quiz), so done may
	// overshoot total: clamp for display, never panic on Repeat.
	done = min(done, total)
	barW := 20
	filled := 0
	if total > 0 {
		filled = done * barW / total
	}
	bar := prog.Render(strings.Repeat("━", filled)) + faint.Render(strings.Repeat("━", barW-filled))
	b.WriteString(fmt.Sprintf("%s %d/%d · ✓%d ✗%d", bar, done, total, m.sess.ok, m.sess.fail))
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
		rev = attn.Render("≋ Mixed")
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
	out := rev + "  " + lrn + up
	if c := m.sess.cur; c != nil {
		if w, found := m.poolWord(c.word); found {
			out += "  " + wordStage(w.Recognition.Step, w.Reproduction.Step)
		}
	}
	return out
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
			b.WriteString(content.Render(c.native) + "\n")
			if c.example != "" {
				b.WriteString(dim.Render(c.example) + "\n")
			}
		}
		if c.done {
			if c.wasOk {
				b.WriteString(ok.Render("✓ Got it") + "\n")
			} else {
				b.WriteString(bad.Render("✗ Missed it") + "\n")
			}
		}
	case cR2:
		b.WriteString(dim.Render("how do you say:") + "\n")
		b.WriteString(bold.Render(c.prompt) + "\n")
		for i, ch := range c.choices {
			line := fmt.Sprintf("  %d  %s", i+1, ch)
			if c.done && i == c.answer {
				line = ok.Render("▸ " + line)
			} else if c.done {
				line = dim.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if c.done {
			if c.wasOk {
				b.WriteString(ok.Render("✓") + "\n")
			} else {
				b.WriteString(bad.Render("✗ answer: "+c.choices[c.answer]) + "\n")
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
			b.WriteString(ok.Render("✓ Got it") + "\n")
		} else {
			b.WriteString(bad.Render("✗ Missed it") + "\n")
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
			b.WriteString(ok.Render("✓ queued") + "\n")
		}
	case cL2:
		b.WriteString(bold.Render(c.prompt) + " → choose translation\n")
		for i, ch := range c.choices {
			line := fmt.Sprintf("  %d  %s", i+1, ch)
			if c.done && i == c.answer {
				line = ok.Render("▸ " + line)
			} else if c.done {
				line = dim.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if c.done {
			if c.wasOk {
				b.WriteString(ok.Render("✓") + "\n")
			} else {
				b.WriteString(bad.Render("✗ answer: "+c.choices[c.answer]) + "\n")
			}
		}
	}
	b.WriteString(faint.Render("[" + c.hint() + "]"))
	return abox.Height(10).Render(b.String())
}

func (m Model) viewVocab(cw, h int) string {
	var b strings.Builder
	title := "Vocabulary · " + m.appID
	if m.searchOn {
		title += "  [/" + m.search + "＿]"
	} else {
		title += dim.Render("   [/] search")
	}
	b.WriteString(title + "\n")
	_ = cw
	if m.vocabMode == 1 {
		b.WriteString(dim.Render(m.wordListTitle) + "\n")
		rows := make([]string, 0, len(m.vocabWords))
		for i, w := range m.vocabWords {
			line := fmt.Sprintf("%s %s — %s · S%d/S%d", wordStage(w.Recognition.Step, w.Reproduction.Step), w.Text, pickNative(w, m.nativeLang()), w.Recognition.Step, w.Reproduction.Step)
			if i == m.wlIdx {
				rows = append(rows, sel.Render("▸ "+line))
			} else {
				rows = append(rows, "  "+line)
			}
		}
		b.WriteString(windowed(rows, m.wlIdx, h-2))
		return b.String()
	}
	if m.obStep == 1 {
		b.WriteString(dim.Render("Choose some categories to start learning") + "\n")
	}
	rows := make([]string, 0, len(m.cats))
	for i, c := range m.cats {
		mark := faint.Render("○")
		if m.catSel[c.ID] {
			mark = ok.Render("●")
		}
		pct := m.catPct[c.ID]
		if pct == "" {
			pct = "—"
		}
		line := fmt.Sprintf("%s %s · %d · %s", mark, c.DisplayName(), c.Words, pct)
		if i == m.vocabIdx {
			rows = append(rows, sel.Render("▸ "+line))
		} else {
			rows = append(rows, "  "+line)
		}
	}
	used := 1
	if m.obStep == 1 {
		used = 2
	}
	b.WriteString(windowed(rows, m.vocabIdx, h-used))
	return b.String()
}

func (m Model) viewWord(cw int) string {
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
	if v, present := w.Translations[nat]; present {
		b.WriteString(ok.Render(firstLine(v)) + "\n")
	}
	for _, k := range slices.Sorted(maps.Keys(w.Translations)) {
		if k == nat {
			continue
		}
		b.WriteString(dim.Render(firstLine(w.Translations[k])) + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
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
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	b.WriteString(fmt.Sprintf("%s recognition (%s→%s):  S%d · E%.2f · F%d",
		wordStage(w.Recognition.Step, w.Reproduction.Step), strings.ToUpper(m.appID), m.nativeLang(), w.Recognition.Step, w.Recognition.Easiness, w.Recognition.Fails) + "  [g]ot-it [m]issed\n")
	b.WriteString(fmt.Sprintf("%s reproduction (%s→%s): S%d · E%.2f · F%d",
		wordStage(w.Recognition.Step, w.Reproduction.Step), m.nativeLang(), strings.ToUpper(m.appID), w.Reproduction.Step, w.Reproduction.Easiness, w.Reproduction.Fails) + "  [1-4] quiz\n")
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	b.WriteString(dim.Render("history") + "\n")
	for _, e := range m.wordLog {
		kind := "got-it"
		if e.Mode == 2 {
			kind = "tested"
		}
		mark := bad.Render("✗")
		if e.Queue == 2 {
			mark = ok.Render("✓")
		}
		b.WriteString(fmt.Sprintf(" %s  %s %s · %dth review\n", e.Date, kind, mark, e.Step+1))
	}
	return b.String()
}

func (m Model) viewStats(cw int) string {
	var b strings.Builder
	b.WriteString(bold.Render("Stats · "+m.appID+" · 7 days") + "\n")
	b.WriteString(m.viewDots() + "\n")
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
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
		b.WriteString(prog.Render(fmt.Sprintf("Memorized today: %d of %d", learned, *goal)) + "\n")
	} else {
		b.WriteString(attn.Render("Daily goal is not set  [g] set") + "\n")
	}
	b.WriteString(fmt.Sprintf("New words memorized: %d\n", learned))
	b.WriteString(fmt.Sprintf("Words being memorized: %d\n", memorizing))
	dueLeft := fmt.Sprintf("Reviewed (unique): %d · due left %d\n", reviewed, left)
	if left > 0 {
		dueLeft = fmt.Sprintf("Reviewed (unique): %d · %s\n", reviewed, attn.Render(fmt.Sprintf("due left %d", left)))
	}
	b.WriteString(dueLeft)
	b.WriteString(fmt.Sprintf("Already known: %d\n", known))
	b.WriteString(fmt.Sprintf("%s Mastered: %d\n", ok.Render("✦"), mastered))
	if goal != nil {
		b.WriteString(dim.Render("[g] adjust daily goal") + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
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
			bars = append(bars, prog.Render(glyphs[idx]))
		}
	}
	return strings.Join(bars, " ") + dim.Render("  7d · counts before today are presence-only")
}

func (m Model) viewSync(cw int) string {
	var b strings.Builder
	b.WriteString(bold.Render("Sync · "+m.appID) + "\n")
	for _, r := range m.syncRows {
		state := ok.Render("● match (clean)")
		if r.State == "dirty" {
			state = bad.Render("✖ drift (DIRTY_SOURCE)")
		} else if r.State == "untracked" {
			state = attn.Render("? untracked")
		}
		b.WriteString(fmt.Sprintf("iCloud file  %s\nfingerprint  %s\n", dim.Render(shortPath(r.Backup)), state))
	}
	b.WriteString(fmt.Sprintf("op-queue     %d intents queued · oplog tail %d\n", len(m.q.Items), len(m.oplog)))
	b.WriteString(fmt.Sprintf("orphans      %d shelved\n", len(m.orphans)))
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	b.WriteString("[p] pull   [w] write queue now\n[r] replay dry-run   [R] replay --apply\n[o] orphans   [a] abort queue\n")
	if m.replayPlan != "" {
		lines := strings.Split(m.replayPlan, "\n")
		if len(lines) > 10 {
			lines = append(lines[:10], "…")
		}
		out := strings.Join(lines, "\n")
		if len(out) > 800 {
			out = out[:800] + "…"
		}
		b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
		b.WriteString(dim.Render(out) + "\n")
	}
	if len(m.q.Items) > 0 {
		b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
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
		return ok.Render("on")
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
		mark = ok.Render("◉")
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
