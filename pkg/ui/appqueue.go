package ui

import (
	"slices"
	"strconv"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

// Each app keeps its own write queue, next to the base queue file: a change
// must never reach another app's backup, where the same word id can be a
// different word.

// appQueuePath is the open app's queue.
func (m Model) appQueuePath() string {
	return queue.PathFor(m.cfg.QueuePath, m.appID)
}

// openApp switches to an app: its own queue, and a working copy made for it.
func (m Model) openApp(id string) (tea.Model, tea.Cmd) {
	m.appID = id
	m.screen = sLearn
	// Another app's working copy and queue must not serve this one.
	m.cli.DB = ""
	m.q = queue.Load(m.appQueuePath())
	return m, m.loadWork()
}

var legacyMu sync.Mutex

// migrateLegacy empties the shared queue of old versions, which held every
// app's changes in one file: each change goes, ahead of that app's own, to
// the one app whose backup has its word (by id and text) or category. What
// fits no single app stays in the old file, never written anywhere; the
// count of those comes back.
func migrateLegacy(cli rwcore.Client, base string, apps []string) (int, error) {
	legacyMu.Lock()
	defer legacyMu.Unlock()
	legacy := queue.Load(base)
	if len(legacy.Items) == 0 || len(apps) == 0 {
		// Not split yet: the apps are not known before the list loads.
		return 0, nil
	}
	cats := map[string][]rwcore.Category{}
	byApp, rest := queue.Split(legacy.Items, func(it queue.Intent) string {
		return ownerOf(cli, apps, cats, it)
	})
	for app, items := range byApp {
		s := queue.Load(queue.PathFor(base, app))
		if err := s.Rewrite(append(items, s.Items...)); err != nil {
			return len(legacy.Items), err
		}
	}
	if err := legacy.Rewrite(rest); err != nil {
		return len(rest), err
	}
	return len(rest), nil
}

// ownerOf is the one app a change fits, or "" when none or several do.
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
		// A new word or a setting names nothing a backup already has.
		return false
	}
	ref := it.Word
	if it.ID != 0 {
		ref = strconv.FormatInt(it.ID, 10)
	}
	w, err := cli.Show(app, ref)
	return err == nil && w.Text == it.Word
}
