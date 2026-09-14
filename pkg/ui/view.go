package ui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	if w < 24 || h < 10 {
		msg := truncateCell(fmt.Sprintf("need a bigger terminal (%dx%d)", w, h), w)
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
	}
	col := lipgloss.NewStyle().Width(cw)
	if m.screen == sSession && m.sess.cur != nil && zenOn(m) {
		card, _ := m.cardBlock(cw, h-2)
		body := fitHeight(col.Render(strings.TrimRight(card, "\n")), h)
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Position(0.45), body)
	}
	header := col.Render(m.viewHeader(cw) + "\n" + faint.Render(strings.Repeat("─", cw)))
	status := col.Render(m.viewStatus(cw))
	help := col.Render(lipgloss.PlaceHorizontal(cw, lipgloss.Center, m.viewHints(cw)))
	statusH := 0
	if m.viewStatus(cw) != "" {
		statusH = lipgloss.Height(status)
	}
	bodyH := h - lipgloss.Height(header) - statusH - lipgloss.Height(help)
	if bodyH < 1 {
		bodyH = 1
	}
	var mid string
	if m.ov == oNone && m.screen == sSession && m.sess.cur != nil {
		body := fitHeight(m.viewSessionWide(w, cw, bodyH), bodyH)
		mid = lipgloss.Place(w, bodyH, lipgloss.Left, lipgloss.Position(0.45), body)
	} else {
		var body string
		if m.ov != oNone {
			body = m.viewOverlay(cw, bodyH)
		} else {
			body = m.viewBody(cw, bodyH)
		}
		body = fitHeight(col.Render(strings.TrimRight(body, "\n")), bodyH)
		mid = lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Position(0.45), body)
	}
	parts := []string{
		lipgloss.PlaceHorizontal(w, lipgloss.Center, header),
		mid,
	}
	if statusH > 0 {
		parts = append(parts, lipgloss.PlaceHorizontal(w, lipgloss.Center, status))
	}
	parts = append(parts, lipgloss.PlaceHorizontal(w, lipgloss.Center, help))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) viewStatus(cw int) string {
	var s string
	switch {
	case m.err != "":
		s = badStyle.Render("! " + m.err)
	case m.notice != "":
		if m.noticeWarn {
			s = attn.Render(m.notice)
		} else {
			s = fg.Render(m.notice)
		}
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
	more := faint.Render("? more")
	join := func(ps []string) string { return strings.Join(ps, sep) }
	var parts []string
	for _, b := range bs {
		p := interactive.Render(b.k) + " " + dim.Render(b.d)
		if lipgloss.Width(join(append(parts, p))) > cw {
			for len(parts) > 0 && lipgloss.Width(join(parts)+sep+more) > cw {
				parts = parts[:len(parts)-1]
			}
			if len(parts) == 0 {
				return p
			}
			return join(parts) + sep + more
		}
		parts = append(parts, p)
	}
	return join(parts)
}

func (m Model) viewHints(cw int) string {
	switch m.screen {
	case sPicker:
		return hints(cw, kb{"h/l", "move"}, kb{"1-9", "pick"}, kb{"enter", "open"}, kb{"q", "quit"})
	case sLearn:
		return hints(cw, kb{"enter", "open"}, kb{"c", "cats"}, kb{"2", "vocab"}, kb{"3", "menu"}, kb{"s", "sync"}, kb{"?", "help"}, kb{"q", "quit"})
	case sSession:
		return hints(cw, m.sessionHints()...)
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
		return hints(cw, kb{"g", "got it"}, kb{"m", "missed it"}, kb{"r", "again"}, kb{"p", "later"}, kb{"e", "examples"}, kb{"J/K", "scroll"}, kb{"esc", "back"}, kb{"R", "reset"}, kb{"X", "remove"})
	case sStats:
		return hints(cw, kb{"j/k", "scroll"}, kb{"g", "goal"}, kb{"3", "menu"}, kb{"q", "back"}, kb{"s", "sync"})
	case sMenu:
		return hints(cw, kb{"j/k", "move"}, kb{"enter", "open"}, kb{"esc", "back"}, kb{"q", "quit"})
	case sSettings:
		return hints(cw, kb{"j/k", "move"}, kb{"space", "change"}, kb{"g", "goal"}, kb{"esc", "back"})
	case sImport:
		return hints(cw, kb{"tab", "field"}, kb{"enter", "import"}, kb{"esc", "cancel"})
	case sAddCat:
		return hints(cw, kb{"tab", "field"}, kb{"enter", "add"}, kb{"esc", "cancel"})
	case sSync:
		return hints(cw, kb{"j/k", "scroll"}, kb{"p", "pull"}, kb{"w", "write"}, kb{"r", "dry-run"}, kb{"R", "apply"}, kb{"o", "orphans"}, kb{"a", "abort"}, kb{"esc", "back"}, kb{"D", "drop"})
	case sAdd:
		return hints(cw, kb{"tab", "field"}, kb{"space", "toggle"}, kb{"enter", "submit"}, kb{"esc", "cancel"})
	}
	return hints(cw, kb{"q", "quit"}, kb{"?", "help"})
}

func (m Model) sessionHints() []kb {
	c := m.sess.cur
	switch {
	case m.sess.typing:
		return []kb{{"type", "answer"}, {"enter", "check"}, {"esc", "back"}, {"tab", "mode"}}
	case c == nil && len(m.sess.undo) > 0:
		return []kb{{"u", "undo"}, {"tab", "mode"}, {"esc", "back"}, {"q", "quit"}}
	case c == nil:
		return []kb{{"tab", "mode"}, {"esc", "back"}, {"q", "quit"}}
	case c.done:
		return []kb{{"←/→", "next"}, {"e", "card"}, {"tab", "mode"}, {"esc", "back"}}
	case c.pane == paneChoose && c.pick == 0:
		return []kb{{"1-4/hjkl", "pick"}, {"esc", "back"}, {"tab", "mode"}, {"e", "card"}}
	}
	var bs []kb
	if _, _, ok := c.swipe(); ok {
		bs = []kb{{"←/→", "answer"}}
	}
	if len(m.sess.undo) > 0 {
		bs = append(bs, kb{"u", "undo"})
	}
	return append(bs, kb{"tab", "mode"}, kb{"e", "card"}, kb{"z", "zen"}, kb{"esc", "back"})
}

func blocksRow(c *card) string {
	var bs []string
	block := func(k, label string) { bs = append(bs, interactive.Render(k)+" "+fg.Render(label)) }
	if c.keyboard {
		block("i", "type")
	}
	block("space", "show")
	if len(c.choices) > 0 {
		block("c", "choose")
	}
	return strings.Join(bs, "   ")
}

func window(n, cur, h int) (from, to int) {
	if n <= h {
		return 0, n
	}
	from = min(max(cur-h/2, 0), n-h)
	return from, from + h
}

func fitHeight(s string, h int) string {
	if h < 1 {
		h = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= h {
		return s
	}
	keep := append([]string{}, lines[:h-1]...)
	keep = append(keep, dim.Render(fmt.Sprintf("… +%d more", len(lines)-h+1)))
	return strings.Join(keep, "\n")
}

func pctBar(pct string) string {
	n, err := strconv.Atoi(strings.TrimSuffix(pct, "%"))
	if err != nil || n < 0 {
		return faint.Render("—")
	}
	if n > 100 {
		n = 100
	}
	filled := n * 5 / 100
	return prog.Render(strings.Repeat("▰", filled)) + faint.Render(strings.Repeat("▱", 5-filled)) + " " + pct
}

func truncateCell(s string, w int) string {
	if w < 1 {
		w = 1
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)+"…") > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func wrapLine(s string, w int) []string {
	if w < 4 {
		w = 4
	}
	if lipgloss.Width(s) <= w {
		return []string{s}
	}
	if strings.Contains(s, "\x1b") {
		return wrapStyled(s, w)
	}
	var out []string
	for _, ln := range wrapStyled(s, w) {
		for lipgloss.Width(ln) > w {
			cut := len(ln)
			cells := 0
			for i, r := range ln {
				rw := lipgloss.Width(string(r))
				if cells+rw > w {
					cut = i
					break
				}
				cells += rw
			}
			if cut <= 0 {
				cut = len(ln)
			}
			out = append(out, ln[:cut])
			ln = ln[cut:]
		}
		out = append(out, ln)
	}
	return out
}

func wrapStyled(line string, w int) []string {
	if w < 4 {
		w = 4
	}
	if lipgloss.Width(line) <= w {
		return []string{line}
	}
	var out []string
	cur := ""
	for _, word := range strings.Split(line, " ") {
		cand := word
		if cur != "" {
			cand = cur + " " + word
		}
		if lipgloss.Width(cand) <= w {
			cur = cand
			continue
		}
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
		if lipgloss.Width(word) <= w {
			cur = word
		} else {
			out = append(out, word)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) == 0 {
		return []string{line}
	}
	return out
}

func windowedCursor(rows []string, cur, h int) string {
	n := len(rows)
	if h < 1 {
		h = 1
	}
	if n <= h {
		return strings.Join(rows, "\n")
	}
	if cur < 0 {
		cur = 0
	}
	if cur >= n {
		cur = n - 1
	}
	from, to := window(n, cur, h)
	for {
		top := boolInt(from > 0)
		bot := boolInt(to < n)
		if to-from+top+bot <= h {
			break
		}
		if to-from <= 1 {
			break
		}
		if to-1-cur >= cur-from && to > cur+1 {
			to--
		} else if from < cur {
			from++
		} else {
			to--
		}
	}
	var seg []string
	if from > 0 {
		seg = append(seg, faint.Render(fmt.Sprintf("↑ %d more", from)))
	}
	seg = append(seg, rows[from:to]...)
	if to < n {
		seg = append(seg, faint.Render(fmt.Sprintf("↓ %d more", n-to)))
	}
	if len(seg) > h {
		return rows[cur]
	}
	return strings.Join(seg, "\n")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
				dot = okStyle.Render("●")
			case "dirty":
				dot = badStyle.Render("●")
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
		goal = prog.Render("/" + *m.stats.Settings.DailyGoal)
	}
	due = len(m.due)
	duePart := fmt.Sprintf("due %d", due)
	if due > 0 {
		duePart = attn.Render(duePart)
	}
	right := learned + goal + " · " + duePart
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
	line = ansi.Truncate(line, cw, "…")
	if lipgloss.Width(line) > cw {
		line = left + " " + dot
	}
	return line
}

func (m Model) viewBody(cw, h int) string {
	switch m.screen {
	case sPicker:
		return m.viewPicker(cw, h)
	case sLearn:
		return m.viewLearn(cw, h)
	case sSession:
		return m.viewSession(cw, h)
	case sVocab:
		return m.viewVocab(cw, h)
	case sWord:
		return m.scrollFit(m.viewWord(cw), h)
	case sStats:
		return m.scrollFit(m.viewStats(cw), h)
	case sSync:
		return m.scrollFit(m.viewSync(cw), h)
	case sAdd:
		return m.viewAdd(h)
	case sMenu:
		return m.viewMenu(h)
	case sSettings:
		return m.viewSettings(h)
	case sImport:
		return m.viewImport()
	case sAddCat:
		return m.viewAddCat()
	}
	return ""
}

func (m Model) viewPicker(cw, h int) string {
	head := interactive.Render("reword") + "\n" + dim.Render("Spaced repetition in terminal") + "\n\n"
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
		msg := "no ReWord apps found"
		if !m.appsLoaded {
			msg = "loading…"
		}
		return lipgloss.PlaceHorizontal(cw, lipgloss.Center, head+dim.Render(msg))
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
		blocks = append(blocks, style.Width(bw+4).Render(c))
	}
	rowW := 0
	for _, bl := range blocks {
		rowW += lipgloss.Width(strings.Split(bl, "\n")[0])
	}
	rowW += 2 * (len(blocks) - 1)
	var full string
	if len(blocks) > 3 || rowW > cw {
		full = strings.Join(blocks, "\n")
	} else {
		spaced := make([]string, 0, len(blocks)*2-1)
		for i, bl := range blocks {
			if i > 0 {
				spaced = append(spaced, "  ")
			}
			spaced = append(spaced, bl)
		}
		full = lipgloss.JoinHorizontal(lipgloss.Top, spaced...)
	}
	if lipgloss.Height(head+full) <= h {
		return lipgloss.PlaceHorizontal(cw, lipgloss.Center, strings.TrimRight(head+full, "\n"))
	}
	capApps := max(1, (h-1)/6)
	title := 1
	if title+6*capApps > h {
		title = 0
		capApps = max(1, h/6)
	}
	from, to := window(len(blocks), m.appIdx, capApps)
	var c strings.Builder
	if title > 0 {
		c.WriteString(interactive.Render("reword") + "\n")
	}
	for i := from; i < to; i++ {
		if i > from {
			c.WriteString("\n")
		}
		c.WriteString(blocks[i])
	}
	return lipgloss.PlaceHorizontal(cw, lipgloss.Center, strings.TrimRight(c.String(), "\n"))
}

func (m Model) viewLearn(cw, h int) string {
	var b strings.Builder
	b.WriteString(interactive.Render("reword") + "\n")
	b.WriteString(dim.Render("Spaced repetition") + "\n")
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
	catBlock := dim.Render(truncateCell(catLine, cw-13)) + "  [c] change\n" +
		faint.Render(strings.Repeat("─", cw)) + "\n"
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
	item := func(i int) []string {
		r := rows[i]
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
		line := lipgloss.NewStyle().Width(cw-8).Render(truncateCell(title, cw-10)) + badgeW.Render(badge)
		if i == m.menuIdx {
			line = sel.Render(line)
		} else {
			line = fg.Render(line)
		}
		return []string{line, "  " + dim.Render(r.desc)}
	}
	k := max(1, min(3, (h-2)/2))
	from, to := window(len(rows), m.menuIdx, k)
	used := 2
	if used+2+2*(to-from) <= h {
		b.WriteString(catBlock)
		used += 2
	}
	for i := from; i < to; i++ {
		for _, ln := range item(i) {
			b.WriteString(ln + "\n")
		}
	}
	used += 2 * (to - from)
	if used+2 <= h {
		b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
		b.WriteString(m.viewDots() + "\n")
	}
	return b.String()
}

func (m Model) viewDots() string {
	if m.today == nil || len(m.today.Week) == 0 {
		return dim.Render("no history yet")
	}
	level := func(d rwcore.WeekDay) int {
		g := m.today.WeekGoal
		switch {
		case g == nil || *g <= 0 || d.Learned <= 0:
			return 0
		case d.Learned >= *g:
			return 2
		}
		return 1
	}
	today := time.Now().Format("2006-01-02")
	var b strings.Builder
	for i, d := range m.today.Week {
		lv := level(d)
		if i > 0 {
			if lv > 0 && lv == level(m.today.Week[i-1]) {
				b.WriteString(attn.Render(" ── "))
			} else {
				b.WriteString(faint.Render(" ── "))
			}
		}
		glyph := [...]string{"○", "◐", "●"}[lv]
		switch {
		case d.Date == today:
			b.WriteString(interactive.Render(glyph))
		case lv == 0:
			b.WriteString(faint.Render(glyph))
		default:
			b.WriteString(attn.Render(glyph))
		}
	}
	return b.String() + dim.Render("   Current ") + attn.Render(fmt.Sprint(m.today.StreakCur)) + dim.Render(" · Best ") + attn.Render(fmt.Sprint(m.today.StreakBest))
}

func (m Model) viewSession(cw, h int) string {
	s := &m.sess
	if s.cur == nil && (s.dealing || !s.started) {
		return dim.Render("dealing…")
	}
	if s.cur == nil {
		return dim.Render(m.sessionDone())
	}
	return m.viewSessionWide(cw, cw, h)
}

func (m Model) sessionDone() string {
	d := m.sess.day
	switch {
	case m.sess.mode != modeReview && d.GoalReached && d.Goal != nil:
		lines := []string{
			bold.Render("Nice job!"),
			"Today you've learned " + prog.Render(plural(d.LearnedToday, "%d new word", "%d new words")),
		}
		if d.NextReview != nil {
			lines = append(lines, dim.Render(showUpIn(*d.NextReview-m.sess.now)))
		}
		return strings.Join(append(lines, "", dim.Render("[c] continue · add more new words   [r] review")), "\n")
	case m.sess.mode == modeLearn && !m.anySelected():
		return "Choose categories to learn new words from"
	case m.sess.mode == modeLearn:
		return "There are no new words in the chosen categories"
	case d.NextReview != nil:
		return showUpIn(*d.NextReview - m.sess.now)
	}
	return "There are no words for review in the chosen categories"
}

func (m Model) viewSessionWide(w, cw, h int) string {
	const minGap, maxGap = 2, 3
	modes := m.modeColumn()
	colW := 0
	for _, ln := range modes {
		colW = max(colW, lipgloss.Width(ln))
	}
	cardW, gap := cw, maxGap
	side := (w-cw)/2-colW >= minGap
	if side {
		gap = min(maxGap, (w-cw)/2-colW)
	} else if w-2*(colW+maxGap) >= 44 {
		cardW, side = w-2*(colW+maxGap), true
	}
	var head []string
	if !side {
		head = append(head, ansi.Truncate(strings.Join(modes, "  "), cardW, "…"))
	}
	head = append(head, wrapLine(m.sessionBar(), cardW)...)
	block := append(head, "")
	card, cut := m.cardBlock(cardW, h-len(block))
	if cut || len(block)+lipgloss.Height(card) > h {
		card, _ = m.cardBlock(cardW, h)
		block = nil
	}
	top := len(block) + 1
	block = append(block, strings.Split(card, "\n")...)
	pad := (w - cardW) / 2
	colStyle := lipgloss.NewStyle().Width(colW)
	for i, ln := range block {
		prefix := strings.Repeat(" ", pad)
		if j := i - top; side && j >= 0 && j < len(modes) {
			prefix = strings.Repeat(" ", pad-gap-colW) + colStyle.Render(modes[j]) + strings.Repeat(" ", gap)
		}
		block[i] = prefix + ln
	}
	return strings.Join(block, "\n")
}

func (m Model) modeColumn() []string {
	rows := []struct {
		mode  int
		label string
	}{
		{modeReview, fmt.Sprintf("Review (%d)", m.sess.day.Due)},
		{modeLearn, fmt.Sprintf("Learning (%d)", m.sess.day.Learning)},
		{modeMixed, "Mixed"},
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.mode == m.sess.mode {
			out = append(out, sel.Render("▸ "+r.label))
		} else {
			out = append(out, fg.Render("  "+r.label))
		}
	}
	return out
}

func (m Model) cardBlock(w, maxH int) (string, bool) {
	c := m.sess.cur
	left, right, ok := m.swipeSides(c)
	if !ok && c != nil && c.done {
		left, right, ok = "", "Next", true
	}
	if !ok {
		return m.viewCard(w, maxH)
	}
	card, cut := m.viewCard(w, maxH-1)
	return card + "\n" + swipeLine(left, right, w), cut
}

func swipeLine(left, right string, w int) string {
	l := ""
	if left != "" {
		l = interactive.Render("←") + " " + swipeStyle(left).Render(left)
	}
	r := swipeStyle(right).Render(right) + " " + interactive.Render("→")
	gap := w - 4 - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		return ansi.Truncate("  "+l+"  "+r, w, "…")
	}
	return "  " + l + strings.Repeat(" ", gap) + r + "  "
}

func swipeStyle(answer string) lipgloss.Style {
	switch answer {
	case "Got it", "Already known", "I have memorized":
		return okStyle
	case "Missed it":
		return badStyle
	}
	return fg
}

func (m Model) sessionBar() string {
	s := &m.sess
	d := s.day
	barW := 20
	filled := 0
	today := fmt.Sprintf("%d today", d.LearnedToday)
	if d.Goal != nil && *d.Goal > 0 {
		filled = int(min(d.LearnedToday, *d.Goal) * int64(barW) / *d.Goal)
		today = fmt.Sprintf("%d/%d today", d.LearnedToday, *d.Goal)
	}
	bar := prog.Render(strings.Repeat("━", filled)) + faint.Render(strings.Repeat("━", barW-filled))
	line := fmt.Sprintf("%s %s · due %d · ✓%d ✗%d", bar, today, d.Due, s.ok, s.fail)
	return line
}

type cardLine struct {
	s      string
	pri    int
	choice bool
}

func countCardChoices(lines []cardLine) int {
	n := 0
	for _, ln := range lines {
		if ln.choice {
			n++
		}
	}
	return n
}

func (m Model) viewCard(cw, maxH int) (string, bool) {
	c := m.sess.cur
	if c == nil {
		return "", false
	}
	inner := cw - 6
	if inner < 10 {
		inner = 10
	}
	var raw []cardLine
	add := func(pri int, s string) { raw = append(raw, cardLine{s, pri, false}) }
	addChoice := func(s string) { raw = append(raw, cardLine{s, 0, true}) }
	add(2, dim.Render(cardTitle(c))+" "+wordStage(c.stepRec, c.stepRep))
	choicesBlock := func() {
		for i, ch := range c.choices {
			line := fmt.Sprintf("%d  %s", i+1, ch)
			switch {
			case c.pick == 0:
			case i == c.answer:
				line = okStyle.Render("▸ " + line)
			case i == c.pick-1:
				line = badStyle.Render("✗ " + line)
			default:
				line = dim.Render("  " + line)
			}
			addChoice(line)
		}
	}
	typeBlock := func() {
		switch c.typed {
		case vRight:
			add(2, okStyle.Render("✓ right"))
		case vPartial:
			add(2, attn.Render("✓ nearly right"))
		case vWrong:
			add(2, badStyle.Render("✗ out of attempts"))
		default:
			in := m.sess.input
			for lipgloss.Width(in) > inner-4 && len(in) > 0 {
				_, size := utf8.DecodeRuneInString(in)
				in = in[size:]
			}
			if in != m.sess.input {
				in = "…" + in
			}
			add(0, "› "+in+"▌")
			add(1, faint.Render(fmt.Sprintf("attempts left: %d", c.attempts)))
		}
	}
	wordFirst := c.native != c.word
	add(0, bold.Render(c.prompt))
	if c.tr != "" && wordFirst {
		add(3, c.tr)
	}
	switch c.pane {
	case paneChoose:
		choicesBlock()
	case paneType:
		typeBlock()
	}
	if c.reveal || c.done {
		if c.native != "" && c.pane != paneChoose {
			add(0, content.Render(c.native))
		}
		if c.tr != "" && !wordFirst {
			add(3, c.tr)
		}
		if c.example != "" {
			add(3, c.example)
		}
	}
	if c.kind == cR1 && c.done {
		if c.wasOk {
			add(2, okStyle.Render("✓ Got it"))
		} else {
			add(2, badStyle.Render("✗ Missed it"))
		}
	}
	var lines []cardLine
	for _, cl := range raw {
		for _, wln := range wrapStyled(cl.s, inner) {
			lines = append(lines, cardLine{wln, cl.pri, cl.choice})
		}
	}
	budget := max(maxH-2, 1)
	var foot []string
	if c.pane == paneNone && !c.reveal && !c.done {
		foot = wrapStyled(blocksRow(c), inner)
		if budget-len(foot) >= 1 {
			budget -= len(foot)
		} else {
			foot = nil
		}
	}
	for len(lines) > budget {
		idx, best := -1, 2
		for i, ln := range lines {
			if ln.pri >= best {
				best, idx = ln.pri, i
			}
		}
		if idx < 0 {
			break
		}
		lines = append(lines[:idx], lines[idx+1:]...)
	}
	if n := countCardChoices(lines); len(lines) > budget && n > 1 {
		var merged []cardLine
		var acc []string
		flush := func() {
			if len(acc) == 0 {
				return
			}
			for _, wln := range wrapStyled(strings.Join(acc, " · "), inner) {
				merged = append(merged, cardLine{wln, 0, false})
			}
			acc = nil
		}
		for _, ln := range lines {
			if ln.choice {
				acc = append(acc, ln.s)
				continue
			}
			flush()
			merged = append(merged, ln)
		}
		flush()
		lines = merged
	}
	cropped := false
	for len(lines) > budget {
		lines = lines[:len(lines)-1]
		cropped = true
	}
	var out []string
	for _, ln := range lines {
		out = append(out, ln.s)
	}
	cardH := max(min(10, maxH-2), 1)
	if len(foot) > 0 {
		for len(out)+len(foot) < cardH {
			out = append(out, "")
		}
		out = append(out, foot...)
	}
	return abox.Width(max(cw-2, 10)).Height(cardH).Render(strings.Join(out, "\n")), cropped
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
		learning, now := "reproduction", time.Now().Unix()
		if m.synced != nil {
			learning = m.synced.Learning
		}
		for i, w := range m.vocabWords {
			rest := truncateCell(fmt.Sprintf("%s — %s · %s", w.Text, pickNative(w, m.nativeLang()), wordStatus(w, learning, now)), cw-4)
			line := wordStage(w.Recognition.Step, w.Reproduction.Step) + " " + rest
			if i == m.wlIdx {
				rows = append(rows, sel.Render("▸ "+line))
			} else {
				rows = append(rows, "  "+line)
			}
		}
		b.WriteString(windowedCursor(rows, m.wlIdx, h-2))
		return b.String()
	}
	if m.obStep == 1 {
		b.WriteString(dim.Render("Choose some categories to start learning") + "\n")
	}
	rows := make([]string, 0, len(m.cats))
	for i, c := range m.cats {
		mark := faint.Render("○")
		if m.catSel[c.ID] {
			mark = okStyle.Render("●")
		}
		pct := pctBar(m.catPct[c.ID])
		name := truncateCell(c.DisplayName(), max(cw-26, 1))
		line := fmt.Sprintf("%s %s · %d · %s", mark, name, c.Words, pct)
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
	b.WriteString(windowedCursor(rows, m.vocabIdx, h-used))
	return b.String()
}

func (m Model) scrollFit(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= h || h < 1 {
		m.scrMax[m.screen] = 0
		return s
	}
	maxOff := len(lines) - h
	m.scrMax[m.screen] = maxOff
	off := m.scrOff[m.screen]
	if off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	win := append([]string{}, lines[off:off+h]...)
	if off > 0 {
		win[0] = dim.Render("↑ more")
	}
	if off+h < len(lines) {
		win[len(win)-1] = dim.Render("↓ more")
	}
	return strings.Join(win, "\n")
}

func (m *Model) clampScroll(s screen) {
	mx, ok := m.scrMax[s]
	if !ok {
		return
	}
	m.scrOff[s] = min(max(m.scrOff[s], 0), mx)
}

func (m Model) viewWord(cw int) string {
	if m.word == nil {
		return dim.Render("no word")
	}
	w := m.word
	var b strings.Builder
	tr := ""
	if w.Transcription != nil && *w.Transcription != "" {
		t := *w.Transcription
		if !strings.HasPrefix(t, "[") {
			t = "[" + t + "]"
		}
		tr = "  " + t
	}
	b.WriteString(bold.Render(w.Text) + dim.Render(tr) + "\n")
	nat := m.nativeLang()
	if v, present := w.Translations[nat]; present {
		b.WriteString(content.Render(firstLine(v)) + "\n")
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
	if len(pairs) > 0 {
		b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	}
	b.WriteString(fmt.Sprintf("%s recognition (%s→%s):  S%d · E%.2f · F%d",
		stageMark(w.Recognition.Step), strings.ToUpper(m.appID), m.nativeLang(), w.Recognition.Step, w.Recognition.Easiness, w.Recognition.Fails) + "  [g]ot-it [m]issed\n")
	b.WriteString(fmt.Sprintf("%s reproduction (%s→%s): S%d · E%.2f · F%d",
		stageMark(w.Reproduction.Step), m.nativeLang(), strings.ToUpper(m.appID), w.Reproduction.Step, w.Reproduction.Easiness, w.Reproduction.Fails) + "  [1-4] quiz\n")
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	b.WriteString(dim.Render("history") + "\n")
	for _, e := range m.wordLog {
		kind := "got-it"
		if e.Mode == 2 {
			kind = "tested"
		}
		mark := badStyle.Render("✗")
		if e.Queue == 2 {
			mark = okStyle.Render("✓")
		}
		b.WriteString(fmt.Sprintf(" %s  %s %s · %s review\n", e.Date, kind, mark, ordinal(e.Step+1)))
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		lines = append(lines, wrapStyled(ln, cw)...)
	}
	return strings.Join(lines, "\n")
}

func ordinal(n int64) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

func (m Model) viewStats(cw int) string {
	var b strings.Builder
	b.WriteString(bold.Render("Stats · "+m.appID+" · this week") + "\n")
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
	b.WriteString(fmt.Sprintf("%s Mastered: %d\n", okStyle.Render("✦"), mastered))
	if goal != nil {
		b.WriteString(dim.Render("[g] adjust daily goal") + "\n")
	}
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	b.WriteString(m.viewBars() + "\n")
	return b.String()
}

func (m Model) viewBars() string {
	if m.today == nil || len(m.today.Week) == 0 {
		return ""
	}
	glyphs := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	mx := int64(1)
	for _, d := range m.today.Week {
		mx = max(mx, d.Learned)
	}
	bars := make([]string, 0, len(m.today.Week))
	for _, d := range m.today.Week {
		if d.Learned == 0 {
			bars = append(bars, faint.Render(glyphs[0]))
		} else {
			bars = append(bars, prog.Render(glyphs[min(d.Learned*7/mx, 7)]))
		}
	}
	return strings.Join(bars, " ") + dim.Render("  learned per day · Mon–Sun")
}

func (m Model) viewSync(cw int) string {
	var b strings.Builder
	b.WriteString(bold.Render("Sync · "+m.appID) + "\n")
	for _, r := range m.syncRows {
		state := okStyle.Render("● match (clean)")
		if r.State == "dirty" {
			state = badStyle.Render("✖ drift (DIRTY_SOURCE)")
		} else if r.State == "untracked" {
			state = attn.Render("? untracked")
		}
		b.WriteString(fmt.Sprintf("iCloud file  %s\nfingerprint  %s\n", dim.Render(shortPath(r.Backup, cw-15)), state))
	}
	b.WriteString(fmt.Sprintf("op-queue     %d intents queued · oplog tail %d\n", len(m.q.Items), len(m.oplog)))
	b.WriteString(fmt.Sprintf("orphans      %d shelved\n", len(m.orphans)))
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
	for _, ln := range []string{
		"[p] pull   [w] write queue now",
		"[r] replay dry-run   [R] replay --apply",
		"[o] orphans   [a] abort queue",
	} {
		for _, wln := range wrapStyled(ln, cw) {
			b.WriteString(wln + "\n")
		}
	}
	if m.replayPlan != "" {
		lines := strings.Split(m.replayPlan, "\n")
		if len(lines) > 10 {
			lines = append(lines[:10], "…")
		}
		var shown []string
		chars := 0
		for _, ln := range lines {
			ln = truncateCell(ln, cw)
			chars += len(ln)
			if chars > 800 {
				shown = append(shown, "…")
				break
			}
			shown = append(shown, ln)
		}
		b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
		b.WriteString(dim.Render(strings.Join(shown, "\n")) + "\n")
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

func shortPath(p string, w int) string {
	if w < 8 {
		w = 8
	}
	if lipgloss.Width(p) <= w {
		return p
	}
	r := []rune(p)
	for len(r) > 0 && lipgloss.Width("…"+string(r)) > w {
		r = r[1:]
	}
	return "…" + string(r)
}

func (m Model) viewMenu(h int) string {
	rows := []string{"Statistics", "Settings", "Import words", "Help", "About"}
	var marked []string
	for i, r := range rows {
		if i == m.menuIdx {
			marked = append(marked, sel.Render("▸ "+r))
		} else {
			marked = append(marked, "  "+r)
		}
	}
	return bold.Render("Menu") + "\n" + windowedCursor(marked, m.menuIdx, max(h-1, 1))
}

func onoff(v bool) string {
	if v {
		return okStyle.Render("on")
	}
	return faint.Render("off")
}

func (m Model) viewSettings(h int) string {
	var lines []string
	cursor := 0
	for i, r := range settingRows {
		switch i {
		case 0:
			lines = append(lines, dim.Render("Shared with the phone"))
		case sharedRows:
			lines = append(lines, "", dim.Render("This computer"))
		}
		line := r.label + ": " + m.settingValue(r)
		if i == m.setIdx {
			cursor = len(lines)
			line = sel.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	return bold.Render("Settings") + "\n" + windowedCursor(lines, cursor, max(h-1, 1))
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

func (m Model) viewAdd(h int) string {
	labels := []string{"word", "transcription", "RUS", "ENG"}
	var rows []string
	for i, l := range labels {
		val := ""
		if i < len(m.addF) {
			val = m.addF[i]
		}
		if m.addIdx == i {
			val += "▌"
			rows = append(rows, sel.Render("▸ "+l+": "+val))
		} else {
			rows = append(rows, fmt.Sprintf("  %s: %s", dim.Render(l), val))
		}
	}
	mark := "○"
	if m.addEnroll {
		mark = okStyle.Render("◉")
	}
	if m.addIdx == 4 {
		rows = append(rows, sel.Render("▸ ["+mark+"] enroll immediately"))
	} else {
		rows = append(rows, fmt.Sprintf("  [%s] enroll immediately", mark))
	}
	return bold.Render("Add word → My words (custom)") + "\n" + windowedCursor(rows, m.addIdx, max(h-1, 1))
}

func (m Model) viewOverlay(cw, h int) string {
	inner := cw - 6
	if inner < 10 {
		inner = 10
	}
	var head, pin []string
	var frame lipgloss.Style
	wrap := func(s string) []string { return wrapLine(s, inner) }
	switch m.ov {
	case oQuit:
		head = []string{bold.Render("Quit"),
			"Your phone will NOT pick up the progress by itself.",
			"Open ReWord → Restore, or your study stays here.",
			"",
			fmt.Sprintf("Queued intents: %d (not written) · Written: %d", len(m.q.Items), m.written),
			""}
		pin = []string{"[w] write queue + quit", "[q] quit anyway   [esc] stay"}
		frame = ybox
	case oConfirm:
		head = append([]string{bold.Render("Confirm")}, wrap(m.confirmT)...)
		pin = []string{"[y] yes   [n] no"}
		frame = ybox
	case oHelp:
		head = wrap(m.helpText())
		frame = abox
	case oGoal:
		head = []string{bold.Render(m.goalTitle)}
		in := m.goalInput
		for lipgloss.Width(in) > inner-2 && len(in) > 0 {
			_, size := utf8.DecodeRuneInString(in)
			in = in[size:]
		}
		if in != m.goalInput {
			in = "…" + in
		}
		pin = []string{"› " + in + "▌", "[enter] save   [esc] cancel"}
		frame = ybox
	case oAbout:
		head = wrap(m.aboutText())
		frame = abox
	case oOrphans:
		head = []string{bold.Render("Orphans")}
		if len(m.orphans) == 0 {
			head = append(head, dim.Render("shelf empty"))
		}
		for i, o := range m.orphans {
			q := ""
			if o.WordQuery != nil {
				q = *o.WordQuery
			}
			head = append(head, truncateCell(fmt.Sprintf("%d  %s  %s", i+1, o.App, q), inner))
			if i > 9 {
				break
			}
		}
		frame = abox
	default:
		return ""
	}
	for i, ln := range pin {
		pin[i] = strings.Join(wrap(ln), "\n")
	}
	pinned := strings.Join(pin, "\n")
	pinH := len(strings.Split(pinned, "\n"))
	wrapLines := func(ls []string) []string {
		var out []string
		for _, ln := range ls {
			out = append(out, wrapLine(ln, inner)...)
		}
		return out
	}
	head = wrapLines(head)
	avail := h - pinH
	var out string
	switch {
	case avail-2 >= len(head):
		out = frame.Width(max(cw-2, 10)).Render(strings.Join(append(head, strings.Split(pinned, "\n")...), "\n"))
	case avail >= 4:
		keep := avail - 2 - 1
		lines := append(head[:min(keep, len(head))],
			dim.Render(fmt.Sprintf("… +%d more", len(head)-min(keep, len(head)))))
		lines = append(lines, strings.Split(pinned, "\n")...)
		out = frame.Width(max(cw-2, 10)).Render(strings.Join(lines, "\n"))
	default:
		keep := h - pinH
		var lines []string
		switch {
		case keep-1 >= len(head):
			lines = head
		case keep > 1:
			lines = append(head[:keep-1],
				dim.Render(fmt.Sprintf("… +%d more", len(head)-keep+1)))
		case keep == 1 && len(head) > 1:
			lines = []string{dim.Render(fmt.Sprintf("… +%d more", len(head)))}
		}
		lines = append(lines, strings.Split(pinned, "\n")...)
		if len(lines) > h {
			lines = lines[len(lines)-h:]
		}
		out = strings.Join(lines, "\n")
	}
	return out
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
