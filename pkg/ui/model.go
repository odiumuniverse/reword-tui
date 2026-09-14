package ui

import (
	"cmp"
	"fmt"
	"path/filepath"
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
	pendingID      int64
	quitAfterWrite bool
	apps           []rwcore.App
	appsLoaded     bool
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
	numFor         string // what the number overlay sets: "" the daily goal, "mastered" days
	synced         *rwcore.Synced
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
		scrOff:  map[screen]int{},
		scrMax:  map[screen]int{},
		width:   80,
		height:  24,
	}
	if cfg.AppID != "" {
		m.appID = cfg.AppID
		m.screen = sLearn
		m.q = queue.Load(m.appQueuePath())
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
type dealMsg struct {
	mode int
	deal rwcore.Deal
	err  error
}
type checkMsg struct {
	wordID int64
	mode   string
	res    rwcore.Check
	err    error
}
type syncedMsg struct {
	app string
	s   rwcore.Synced
	err error
}
type workMsg struct {
	app  string
	path string
	err  error
	// legacy counts old shared-queue changes that fit no single app.
	legacy int
	// skipped are queued changes the fresh copy could not take.
	skipped []string
}

func (m Model) loadPickerMeta(id string) tea.Cmd {
	cli := m.cli.Remote()
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
	cli, app := m.cli.Remote(), m.appID
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
	cli, app := m.cli.Remote(), m.appID
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
			if it.App != "" && it.App != app {
				// Queues are per app; one found here is a bug, not a write.
				n := written + orphaned
				return writeMsg{written: written, orphaned: orphaned, consumed: n,
					err: fmt.Errorf("queue holds %s's change %q; not written to %s", it.App, it.Label(), app)}
			}
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

// enqueue queues a change for iCloud and applies it to the working copy at
// once, returning the working copy's receipt (an answer's carries the row
// an undo puts back).
func (m *Model) enqueue(it queue.Intent) rwcore.Receipt {
	if it.TS == 0 {
		it.TS = time.Now().Unix()
	}
	if it.App == "" {
		it.App = m.appID
	}
	if err := m.q.Append(it); err != nil {
		m.err = err.Error()
		return rwcore.Receipt{}
	}
	// The working copy takes the intent at once, so the next card and every
	// count already see it; iCloud gets it on the next write.
	var r rwcore.Receipt
	if m.cli.DB != "" {
		var err error
		if r, err = m.cli.Apply(m.appID, it.ApplyBody()); err != nil {
			m.err = "working copy: " + err.Error()
		}
	}
	// No notice: the change shows at once, and the header counts the queue.
	return r
}

// workPath is the app's working copy: a local copy of the backup that
// every answer lands in at once, so the next card is dealt from the state
// the phone would see. It sits next to the write queue it mirrors.
func (m Model) workPath() string {
	return filepath.Join(filepath.Dir(m.cfg.QueuePath), "work-"+m.appID+".db")
}

// loadWork makes the working copy ready: always a fresh copy of the backup
// with this app's queued changes replayed into it, so it is exactly what the
// next write will make of iCloud. Changes of the old shared queue go to
// their apps first.
func (m Model) loadWork() tea.Cmd {
	cli, app, path, base := m.cli.Remote(), m.appID, m.workPath(), m.cfg.QueuePath
	apps := make([]string, 0, len(m.apps))
	for _, a := range m.apps {
		apps = append(apps, a.ID)
	}
	return func() tea.Msg {
		left, err := migrateLegacy(cli, base, apps)
		msg := workMsg{app: app, legacy: left}
		if err != nil {
			msg.err = fmt.Errorf("split the old queue: %w", err)
			return msg
		}
		if err := cli.Work(app, path); err != nil {
			msg.err = err
			return msg
		}
		local := cli
		local.DB = path
		// One change the copy cannot take (its word gone meanwhile) must not
		// cost the rest; the write will shelve it the same way.
		for _, it := range queue.Load(queue.PathFor(base, app)).Items {
			if _, err := local.Apply(app, it.ApplyBody()); err != nil {
				msg.skipped = append(msg.skipped, it.Label()+": "+err.Error())
			}
		}
		msg.path = path
		return msg
	}
}

func (m Model) onWork(msg workMsg) (tea.Model, tea.Cmd) {
	if msg.app != m.appID {
		return m, nil
	}
	// The old shared queue may have handed this app changes meanwhile.
	m.q = queue.Load(m.appQueuePath())
	m.cli.DB = msg.path
	switch {
	case msg.err != nil:
		// Without a working copy the screens read the backup itself; answers
		// still queue, but the next card cannot see them.
		m.err = "working copy: " + msg.err.Error()
		m.cli.DB = ""
	case len(msg.skipped) > 0:
		m.err = fmt.Sprintf("working copy: %d queued change(s) did not apply, first: %s", len(msg.skipped), msg.skipped[0])
	case msg.legacy > 0:
		m.setNotice(fmt.Sprintf("%d old queued change(s) fit no single app; kept aside in %s, not written",
			msg.legacy, filepath.Base(m.cfg.QueuePath)), true)
	}
	return m, tea.Batch(m.loadMain(), m.loadCats(), m.loadSynced())
}

// loadSynced reads the settings shared with the phone, from the working
// copy once there is one.
func (m Model) loadSynced() tea.Cmd {
	cli, app := m.cli, m.appID
	return func() tea.Msg {
		s, err := cli.Settings(app)
		return syncedMsg{app: app, s: s, err: err}
	}
}

func (m Model) onSynced(msg syncedMsg) (tea.Model, tea.Cmd) {
	if msg.app != m.appID {
		return m, nil
	}
	if msg.err != nil {
		m.err = "settings: " + msg.err.Error()
		return m, nil
	}
	m.synced = &msg.s
	m.prefs.ShowTranscription = msg.s.Transcription
	return m, nil
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
