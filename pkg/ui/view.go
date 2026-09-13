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
	col := lipgloss.NewStyle().Width(cw)
	if m.screen == sSession && m.sess.cur != nil && zenOn(m) {
		body := col.Render(strings.TrimRight(m.viewCard(cw, h-2), "\n"))
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

// pctBar renders a 5-cell pink mini-bar from a "12%" string, "—" when empty.
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

// truncateCell cuts s to w cells with an ellipsis. Width is display-based.
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

// wrapCell splits plain text into lines of at most w cells, breaking on
// spaces and hard-splitting overlong words. Wrap before styling.
func wrapCell(s string, w int) []string {
	if w < 4 {
		w = 4
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		cur := ""
		flush := func() {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
		}
		for _, word := range strings.Fields(para) {
			if cur == "" {
				cur = word
				continue
			}
			if lipgloss.Width(cur+" "+word) <= w {
				cur += " " + word
				continue
			}
			flush()
			cur = word
		}
		for lipgloss.Width(cur) > w {
			cut := splitAt(cur, w)
			lines = append(lines, cur[:cut])
			cur = cur[cut:]
		}
		flush()
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// wrapStyled splits an already-styled line into lines of at most w cells,
// breaking on spaces only. Safe for ANSI: SGR sequences contain no spaces,
// so a break never cuts an escape sequence. Spaceless overlong words are
// left as-is (same as before, no panic).
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

// fitBox wraps every line of a box body to inner width and frames it at cw.
func fitBox(body string, w, cw int, frame lipgloss.Style) string {
	var lines []string
	for _, ln := range strings.Split(body, "\n") {
		lines = append(lines, wrapStyled(ln, w)...)
	}
	return frame.Width(cw).Render(strings.Join(lines, "\n"))
}

// splitAt returns a byte index in s holding at most w cells.
func splitAt(s string, w int) int {
	cells := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if cells+rw > w {
			if i == 0 {
				// one wide rune: still consume it to make progress
				_, size := utf8.DecodeRuneInString(s)
				return size
			}
			return i
		}
		cells += rw
	}
	return len(s)
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
	// ansi.Truncate is width- and escape-aware, unlike rune slicing.
	line = ansi.Truncate(line, cw, "…")
	if lipgloss.Width(line) > cw {
		line = left + " " + dot
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
		return m.viewSession(cw, h)
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
	b.WriteString(dim.Render(truncateCell(catLine, cw-13)) + "  [c] change\n")
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
		line := lipgloss.NewStyle().Width(cw-8).Render(truncateCell(title, cw-10)) + badgeW.Render(badge)
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

func (m Model) viewSession(cw, h int) string {
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
	b.WriteString(m.viewCard(cw, h-4))
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
		rev = sel.Render("≋ Mixed")
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

func (m Model) viewCard(cw, maxH int) string {
	c := m.sess.cur
	if c == nil {
		return ""
	}
	inner := cw - 6 // abox border + padding
	if inner < 10 {
		inner = 10
	}
	stage := ""
	if w, found := m.poolWord(c.word); found {
		stage = " " + wordStage(w.Recognition.Step, w.Reproduction.Step)
	}
	var b strings.Builder
	b.WriteString(dim.Render(cardTitle(c)) + stage + "\n")
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
				b.WriteString(okStyle.Render("✓ Got it") + "\n")
			} else {
				b.WriteString(badStyle.Render("✗ Missed it") + "\n")
			}
		}
	case cR2:
		b.WriteString(dim.Render("how do you say:") + "\n")
		b.WriteString(bold.Render(c.prompt) + "\n")
		for i, ch := range c.choices {
			line := fmt.Sprintf("  %d  %s", i+1, ch)
			if c.done && i == c.answer {
				line = okStyle.Render("▸ " + line)
			} else if c.done {
				line = dim.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if c.done {
			if c.wasOk {
				b.WriteString(okStyle.Render("✓") + "\n")
			} else {
				b.WriteString(badStyle.Render("✗ answer: "+c.choices[c.answer]) + "\n")
			}
		}
	case cR3:
		b.WriteString(dim.Render("type in the target language:") + "\n")
		b.WriteString(bold.Render(c.prompt) + "\n")
		if m.sess.typing || c.done {
			in := m.sess.input
			for lipgloss.Width(in) > inner-4 && len(in) > 0 {
				_, size := utf8.DecodeRuneInString(in)
				in = in[size:]
			}
			if in != m.sess.input {
				in = "…" + in
			}
			b.WriteString("› " + in + "▌\n")
		}
		if !c.done {
			b.WriteString(faint.Render(fmt.Sprintf("attempts left: %d", c.attempts)) + "\n")
		} else if c.wasOk {
			b.WriteString(okStyle.Render("✓ Got it") + "\n")
		} else {
			b.WriteString(badStyle.Render("✗ Missed it") + "\n")
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
			b.WriteString(okStyle.Render("✓ queued") + "\n")
		}
	case cL2:
		b.WriteString(bold.Render(c.prompt) + " → choose translation\n")
		for i, ch := range c.choices {
			line := fmt.Sprintf("  %d  %s", i+1, ch)
			if c.done && i == c.answer {
				line = okStyle.Render("▸ " + line)
			} else if c.done {
				line = dim.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if c.done {
			if c.wasOk {
				b.WriteString(okStyle.Render("✓") + "\n")
			} else {
				b.WriteString(badStyle.Render("✗ answer: "+c.choices[c.answer]) + "\n")
			}
		}
	}
	b.WriteString(faint.Render("[" + c.hint() + "]"))
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		lines = append(lines, wrapStyled(ln, inner)...)
	}
	cardH := min(10, max(4, maxH))
	return abox.Width(cw).Height(cardH).Render(strings.Join(lines, "\n"))
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
			rest := truncateCell(fmt.Sprintf("%s — %s · S%d/S%d", w.Text, pickNative(w, m.nativeLang()), w.Recognition.Step, w.Reproduction.Step), cw-4)
			line := wordStage(w.Recognition.Step, w.Reproduction.Step) + " " + rest
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
	b.WriteString(faint.Render(strings.Repeat("─", cw)) + "\n")
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
		b.WriteString(fmt.Sprintf(" %s  %s %s · %dth review\n", e.Date, kind, mark, e.Step+1))
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		lines = append(lines, wrapStyled(ln, cw)...)
	}
	return strings.Join(lines, "\n")
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
	b.WriteString(fmt.Sprintf("%s Mastered: %d\n", okStyle.Render("✦"), mastered))
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
	b.WriteString("[p] pull   [w] write queue now\n[r] replay dry-run   [R] replay --apply\n[o] orphans   [a] abort queue\n")
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
		return okStyle.Render("on")
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
		mark = okStyle.Render("◉")
	}
	if m.addIdx == 4 {
		b.WriteString(sel.Render("▸ ["+mark+"] enroll immediately") + "\n")
	} else {
		b.WriteString(fmt.Sprintf("  [%s] enroll immediately\n", mark))
	}
	return b.String()
}

func (m Model) viewOverlay(cw int) string {
	inner := cw - 6 // box border + padding
	if inner < 10 {
		inner = 10
	}
	switch m.ov {
	case oQuit:
		var b strings.Builder
		b.WriteString(bold.Render("Quit") + "\n")
		b.WriteString("Your phone will NOT pick up the progress by itself.\n")
		b.WriteString("Open ReWord → Restore, or your study stays here.\n\n")
		b.WriteString(fmt.Sprintf("Queued intents: %d (not written) · Written: %d\n\n", len(m.q.Items), m.written))
		b.WriteString("[w] write queue + quit   [q] quit anyway   [esc] stay")
		return fitBox(strings.TrimRight(b.String(), "\n"), inner, cw, ybox)
	case oConfirm:
		return fitBox(bold.Render("Confirm")+"\n"+m.confirmT+"\n\n[y] yes   [n] no", inner, cw, ybox)
	case oHelp:
		return fitBox(m.helpText(), inner, cw, abox)
	case oGoal:
		in := m.goalInput
		for lipgloss.Width(in) > inner-4 && len(in) > 0 {
			_, size := utf8.DecodeRuneInString(in)
			in = in[size:]
		}
		if in != m.goalInput {
			in = "…" + in
		}
		return fitBox(bold.Render(m.goalTitle)+"\n› "+in+"▌\n\n[enter] save   [esc] cancel", inner, cw, ybox)
	case oAbout:
		return fitBox(m.aboutText(), inner, cw, abox)
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
			b.WriteString(truncateCell(fmt.Sprintf("%d  %s  %s", i+1, o.App, q), inner) + "\n")
			if i > 9 {
				break
			}
		}
		return fitBox(strings.TrimRight(b.String(), "\n"), inner, cw, abox)
	}
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
