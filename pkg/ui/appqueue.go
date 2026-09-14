package ui

import (
	"cmp"
	"slices"
	"strconv"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

func (m Model) appQueuePath() string {
	return queue.PathFor(m.cfg.QueuePath, m.appID)
}

func (m Model) openApp(id string) (tea.Model, tea.Cmd) {
	m.appID = id
	m.screen = sLearn
	m.cli.DB = ""
	m.q = queue.Load(m.appQueuePath())
	return m, m.loadWork()
}

var legacyMu sync.Mutex

func migrateLegacy(cli rwcore.Client, base string, apps []string) (int, error) {
	legacyMu.Lock()
	defer legacyMu.Unlock()
	legacy := queue.Load(base)
	if len(legacy.Items) == 0 || len(apps) == 0 {
		return 0, nil
	}
	cats := map[string][]rwcore.Category{}
	byApp, rest := queue.Split(legacy.Items, func(it queue.Intent) string {
		return ownerOf(cli, apps, cats, it)
	})
	if len(rest) > 0 {
		placed, left := queue.PlaceByTime(rest, workMarks(cli, base, apps, byApp), func(app string, it queue.Intent) bool {
			return fits(cli, app, cats, it)
		})
		for app, items := range placed {
			byApp[app] = append(byApp[app], items...)
		}
		rest = left
	}
	for app, items := range byApp {
		s := queue.Load(queue.PathFor(base, app))
		all := append(items, s.Items...)
		slices.SortStableFunc(all, func(a, b queue.Intent) int { return cmp.Compare(a.TS, b.TS) })
		if err := s.Rewrite(all); err != nil {
			return len(legacy.Items), err
		}
	}
	if err := legacy.Rewrite(rest); err != nil {
		return len(rest), err
	}
	return len(rest), nil
}

func workMarks(cli rwcore.Client, base string, apps []string, byApp map[string][]queue.Intent) []queue.Mark {
	var marks []queue.Mark
	for _, app := range apps {
		for _, it := range append(queue.Load(queue.PathFor(base, app)).Items, byApp[app]...) {
			marks = append(marks, queue.Mark{TS: it.TS, App: app})
		}
	}
	if ops, err := cli.Oplog(1000); err == nil {
		for _, o := range ops {
			if slices.Contains(apps, o.App) {
				marks = append(marks, queue.Mark{TS: o.Ts, App: o.App})
			}
		}
	}
	return marks
}

func ownerOf(cli rwcore.Client, apps []string, cats map[string][]rwcore.Category, it queue.Intent) string {
	owner := ""
	for _, app := range apps {
		if !fits(cli, app, cats, it) {
			continue
		}
		if owner != "" {
			return ""
		}
		owner = app
	}
	return owner
}

func fits(cli rwcore.Client, app string, cats map[string][]rwcore.Category, it queue.Intent) bool {
	switch {
	case it.App != "":
		return it.App == app
	case it.Op == "select":
		cs, ok := cats[app]
		if !ok {
			cs, _ = cli.Categories(app)
			cats[app] = cs
		}
		return slices.ContainsFunc(cs, func(c rwcore.Category) bool { return c.ID == it.Category })
	case it.Op == "add" || it.Word == "":
		return false
	}
	ref := it.Word
	if it.ID != 0 {
		ref = strconv.FormatInt(it.ID, 10)
	}
	w, err := cli.Show(app, ref)
	return err == nil && w.Text == it.Word
}
