package ui

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

type screen int

const (
	sPicker screen = iota
	sLearn
	sSession
	sVocab
	sWord
	sStats
	sSync
	sAdd
	sMenu
	sSettings
	sImport
	sAddCat
)

type overlay int

const (
	oNone overlay = iota
	oQuit
	oConfirm
	oHelp
	oOrphans
	oGoal
	oAbout
)

type Config struct {
	Rwcore     string
	IcloudRoot string
	CacheDir   string
	DataDir    string
	AppID      string
	QueuePath  string
}

type Model struct {
	cfg            Config
	cli            rwcore.Client
	prefs          Prefs
	screen         screen
	ov             overlay
	confirmT       string
	pending        string
	pendingWord    string
	quitAfterWrite bool
	apps           []rwcore.App
	appIdx         int
	appID          string
	appMeta        map[string]appMeta
	stats          *rwcore.Stats
	today          *rwcore.Today
	due            []rwcore.DueItem
	cats           []rwcore.Category
	catSel         map[string]bool
	catPct         map[string]string
	vocabIdx       int
	vocabMode      int
	wlIdx          int
	vocabWords     []rwcore.Word
	wordListTitle  string
	word           *rwcore.Word
	wordLog        []rwcore.LogEntry
	wordEx         bool
	wordReturn     screen
	acted          map[string]bool
	search         string
	searchOn       bool
	scrOff         map[screen]int
	scrMax         map[screen]int
	menuIdx        int
	sess           session
	pool           []rwcore.Word
	poolIdx        map[string]int
	addF           []string
	addIdx         int
	addEnroll      bool
	catF           []string
	catIdx         int
	impF           []string
	impIdx         int
	setIdx         int
	goalInput      string
	goalTitle      string
	obStep         int
	syncRows       []rwcore.StatusRow
	oplog          []rwcore.OpEntry
	orphans        []rwcore.OrphanEntry
	replayPlan     string
	q              queue.Store
	written        int
	err            string
	notice         string
	noticeWarn     bool
	loading        string
	width          int
	height         int
	detail         map[string][]rwcore.Word
}

type appMeta struct {
	words int64
	due   int
}

func New(cfg Config) Model {
	cli := rwcore.Client{Bin: cfg.Rwcore, IcloudRoot: cfg.IcloudRoot, CacheDir: cfg.CacheDir, DataDir: cfg.DataDir}
	m := Model{
		cfg:     cfg,
		cli:     cli,
		prefs:   LoadPrefs(),
		catSel:  map[string]bool{},
		catPct:  map[string]string{},
		detail:  map[string][]rwcore.Word{},
		appMeta: map[string]appMeta{},
		acted:   map[string]bool{},
		q:       queue.Load(cfg.QueuePath),
		poolIdx: map[string]int{},
		scrOff:  map[screen]int{},
		scrMax:  map[screen]int{},
		width:   80,
		height:  24,
	}
	if cfg.AppID != "" {
		m.appID = cfg.AppID
		m.screen = sLearn
	}
	return m
}

type appsMsg struct {
	apps []rwcore.App
	err  error
}
type statsMsg struct {
	stats rwcore.Stats
	today rwcore.Today
	due   []rwcore.DueItem
	err   error
}
type catsMsg struct {
	cats []rwcore.Category
	err  error
}
type catStatsMsg struct {
	stats []rwcore.CatStat
	err   error
}
type wordsMsg struct {
	key   string
	words []rwcore.Word
	err   error
}
type wordMsg struct {
	word rwcore.Word
	log  []rwcore.LogEntry
	err  error
}
type syncMsg struct {
	rows []rwcore.StatusRow
	ops  []rwcore.OpEntry
	orph []rwcore.OrphanEntry
	err  error
}
type replayMsg struct {
	out string
	err error
}
type writeMsg struct {
	written  int
	orphaned int
	consumed int
	err      error
	dirty    bool
}
type selectAllMsg struct {
	err error
}
type importMsg struct {
	err error
}
type pickerMetaMsg struct {
	id    string
	words int64
	due   int
}

func (m Model) loadPickerMeta(id string) tea.Cmd {
	cli := m.cli
	return func() tea.Msg {
		st, err := cli.Stats(id)
		if err != nil {
			return pickerMetaMsg{id: id}
		}
		due, err := cli.Due(id, 5000)
		if err != nil {
			return pickerMetaMsg{id: id}
		}
		return pickerMetaMsg{id: id, words: st.Words, due: len(due)}
	}
}

func (m Model) loadApps() tea.Cmd {
	cli := m.cli
	return func() tea.Msg {
		apps, err := cli.Apps()
		return appsMsg{apps, err}
	}
}

func (m Model) loadMain() tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		st, err := cli.Stats(app)
		if err != nil {
			return statsMsg{err: err}
		}
		td, err := cli.Today(app)
		if err != nil {
			return statsMsg{err: err}
		}
		due, err := cli.Due(app, 5000)
		if err != nil {
			return statsMsg{err: err}
		}
		slices.SortFunc(due, func(a, b rwcore.DueItem) int {
			return cmp.Compare(b.OverdueSecs, a.OverdueSecs)
		})
		return statsMsg{st, td, due, nil}
	}
}

func (m Model) loadCats() tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		cats, err := cli.Categories(app)
		return catsMsg{cats, err}
	}
}

func (m Model) loadCatStats() tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		stats, err := cli.CatStats(app)
		return catStatsMsg{stats, err}
	}
}

func (m Model) loadWords(key, search, category string, limit int) tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		w, err := cli.Words(app, search, category, limit)
		return wordsMsg{key, w, err}
	}
}

func (m Model) loadWord(query string) tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		w, err := cli.Show(app, query)
		if err != nil {
			return wordMsg{err: err}
		}
		log, err := cli.Log(app, query, 5)
		if err != nil {
			return wordMsg{err: err}
		}
		return wordMsg{w, log, nil}
	}
}

func (m Model) loadSync() tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		rows, err := cli.Status(app)
		if err != nil {
			return syncMsg{err: err}
		}
		ops, err := cli.Oplog(40)
		if err != nil {
			return syncMsg{err: err}
		}
		orph, err := cli.Orphans()
		if err != nil {
			return syncMsg{err: err}
		}
		return syncMsg{rows, ops, orph, nil}
	}
}

func (m Model) doWrite() tea.Cmd {
	cli, app := m.cli, m.appID
	items := append([]queue.Intent{}, m.q.Items...)
	return func() tea.Msg {
		if _, err := cli.Snapshot(app); err != nil {
			if rwcore.IsDirty(err) {
				return writeMsg{dirty: true, err: err}
			}
			return writeMsg{err: err}
		}
		written, orphaned := 0, 0
		for _, it := range items {
			var r rwcore.Receipt
			var err error
			if it.Op == "select" {
				_, err = cli.Select(app, it.Category, it.Selected)
			} else {
				r, err = cli.Apply(app, it.ApplyBody())
			}
			if err != nil {
				n := written + orphaned
				if rwcore.IsDirty(err) {
					return writeMsg{written: written, orphaned: orphaned, consumed: n, dirty: true, err: err}
				}
				return writeMsg{written: written, orphaned: orphaned, consumed: n, err: err}
			}
			if r.Detail.Orphaned {
				orphaned++
			} else {
				written++
			}
		}
		return writeMsg{written: written, orphaned: orphaned, consumed: len(items)}
	}
}

func (m *Model) enqueue(it queue.Intent) {
	if err := m.q.Append(it); err != nil {
		m.err = err.Error()
		return
	}
	m.setNotice("queued: "+it.Label(), false)
}

func (m *Model) setNotice(s string, warn bool) {
	m.notice, m.noticeWarn = s, warn
}

func overdueStr(s int64) string {
	if s < 90 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 5400 {
		return fmt.Sprintf("%dm", s/60)
	}
	if s < 172800 {
		return fmt.Sprintf("%dh", s/3600)
	}
	return fmt.Sprintf("%dd", s/86400)
}

func ageStr(mtime uint64) string {
	now := uint64(time.Now().Unix())
	if mtime > now {
		return "0m"
	}
	return overdueStr(int64(now - mtime))
}

func gradeResult(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}
