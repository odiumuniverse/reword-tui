package main

import (
	"cmp"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
	"reword-tui/pkg/ui"
)

func main() {
	rwcoreFlag := flag.String("rwcore", "", "path to rwcore binary")
	icloudRoot := flag.String("icloud-root", "", "iCloud root override")
	cacheDir := flag.String("cache-dir", "", "cache dir override")
	dataDir := flag.String("data-dir", "", "data dir override")
	app := flag.String("app", "", "app id to open directly")
	queuePath := flag.String("queue", "", "queue file override")
	flag.Parse()

	bin, err := findRwcore(*rwcoreFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	home, _ := os.UserHomeDir()
	qp := cmp.Or(*queuePath, defaultQueuePath(*dataDir, home))
	cfg := ui.Config{
		Rwcore:     bin,
		IcloudRoot: *icloudRoot,
		CacheDir:   *cacheDir,
		DataDir:    *dataDir,
		AppID:      *app,
		QueuePath:  qp,
	}
	p := tea.NewProgram(ui.New(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultQueuePath(dataDir, home string) string {
	if dataDir != "" {
		return filepath.Join(dataDir, "queue.jsonl")
	}
	legacy := filepath.Join(home, ".local", "share", "reword", "queue.jsonl")
	dir := filepath.Join(home, ".local", "share", "reword")
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(home, "Library", "Application Support", "reword")
	}
	next := filepath.Join(dir, "queue.jsonl")
	if next == legacy {
		return next
	}
	// Don't strand pending intents queued at the old location.
	if _, err := os.Stat(next); os.IsNotExist(err) {
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return next
}

func findRwcore(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if self, err := os.Executable(); err == nil {
		next := filepath.Join(filepath.Dir(self), "rwcore")
		if _, err := os.Stat(next); err == nil {
			return next, nil
		}
	}
	if p, err := exec.LookPath("rwcore"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("rwcore not found: pass --rwcore PATH")
}
