package rwcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Client struct {
	Bin        string
	IcloudRoot string
	CacheDir   string
	DataDir    string
	// DB is a working copy of the app's backup: reads come from it and
	// Apply writes straight into it. Empty means the backup in iCloud.
	DB string
}

// Remote is the client for the app's backup in iCloud itself, for writes,
// sync and anything that must not read the working copy.
func (c Client) Remote() Client {
	c.DB = ""
	return c
}

func (c Client) baseArgs(app string) []string {
	var a []string
	if c.IcloudRoot != "" {
		a = append(a, "--icloud-root", c.IcloudRoot)
	}
	if c.CacheDir != "" {
		a = append(a, "--cache-dir", c.CacheDir)
	}
	if c.DataDir != "" {
		a = append(a, "--data-dir", c.DataDir)
	}
	if c.DB != "" {
		a = append(a, "--db", c.DB)
	}
	a = append(a, "--format", "json")
	if app != "" {
		a = append(a, "--app", app)
	}
	return a
}

func (c Client) run(app string, stdin string, args ...string) ([]byte, error) {
	full := append(c.baseArgs(app), args...)
	cmd := exec.Command(c.Bin, full...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return out.Bytes(), nil
}

func decode[T any](data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("decode rwcore output: %w", err)
	}
	return v, nil
}

func (c Client) Apps() ([]App, error) {
	b, err := c.run("", "", "apps")
	if err != nil {
		return nil, err
	}
	return decode[[]App](b)
}

func (c Client) Stats(app string) (Stats, error) {
	b, err := c.run(app, "", "stats")
	if err != nil {
		return Stats{}, err
	}
	return decode[Stats](b)
}

func (c Client) Today(app string) (Today, error) {
	b, err := c.run(app, "", "today")
	if err != nil {
		return Today{}, err
	}
	return decode[Today](b)
}

func (c Client) Due(app string, limit int) ([]DueItem, error) {
	b, err := c.run(app, "", "due", "--limit", fmt.Sprint(limit))
	if err != nil {
		return nil, err
	}
	return decode[[]DueItem](b)
}

func (c Client) Categories(app string) ([]Category, error) {
	b, err := c.run(app, "", "categories")
	if err != nil {
		return nil, err
	}
	return decode[[]Category](b)
}

func (c Client) CatStats(app string) ([]CatStat, error) {
	b, err := c.run(app, "", "catstats")
	if err != nil {
		return nil, err
	}
	return decode[[]CatStat](b)
}

func (c Client) Words(app, search, category string, limit int) ([]Word, error) {
	args := []string{"words", "--limit", fmt.Sprint(limit)}
	if search != "" {
		args = append(args, "--search", search)
	}
	if category != "" {
		args = append(args, "--category", category)
	}
	b, err := c.run(app, "", args...)
	if err != nil {
		return nil, err
	}
	return decode[[]Word](b)
}

func (c Client) Show(app, query string) (Word, error) {
	b, err := c.run(app, "", "show", query)
	if err != nil {
		return Word{}, err
	}
	return decode[Word](b)
}

func (c Client) Log(app, word string, limit int) ([]LogEntry, error) {
	b, err := c.run(app, "", "log", word, "--limit", fmt.Sprint(limit))
	if err != nil {
		return nil, err
	}
	return decode[[]LogEntry](b)
}

func (c Client) Status(app string) ([]StatusRow, error) {
	b, err := c.run(app, "", "status")
	if err != nil {
		return nil, err
	}
	return decode[[]StatusRow](b)
}

func (c Client) Oplog(limit int) ([]OpEntry, error) {
	b, err := c.run("", "", "oplog", "--limit", fmt.Sprint(limit))
	if err != nil {
		return nil, err
	}
	return decode[[]OpEntry](b)
}

func (c Client) Orphans() ([]OrphanEntry, error) {
	b, err := c.run("", "", "orphans")
	if err != nil {
		return nil, err
	}
	return decode[[]OrphanEntry](b)
}

func (c Client) Snapshot(app string) (OkWrap, error) {
	b, err := c.run(app, "", "snapshot")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) Pull(app string) (OkWrap, error) {
	b, err := c.run(app, "", "pull")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) Replay(app string, apply bool) ([]byte, error) {
	if apply {
		return c.run(app, "", "replay", "--apply", "--yes")
	}
	return c.run(app, "", "replay")
}

func (c Client) Apply(app string, intent map[string]any) (Receipt, error) {
	body, err := json.Marshal(intent)
	if err != nil {
		return Receipt{}, err
	}
	b, err := c.run(app, string(body), "apply")
	if err != nil {
		return Receipt{}, err
	}
	return decode[Receipt](b)
}

func (c Client) Select(app, category string, selected bool) (OkWrap, error) {
	flag := "--off"
	if selected {
		flag = "--on"
	}
	b, err := c.run(app, "", "select", "--category", category, flag, "--yes")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) SelectAll(app string, selected bool) (OkWrap, error) {
	flag := "--off"
	if selected {
		flag = "--on"
	}
	b, err := c.run(app, "", "select", "--all", flag, "--yes")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) AddCategory(app, id, name string) (OkWrap, error) {
	b, err := c.run(app, "", "add-category", "--id", id, "--name", name, "--yes")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) ImportCsv(app, category, file, lang string) (OkWrap, error) {
	args := []string{"import-csv", "--category", category, "--file", file, "--yes"}
	if lang != "" {
		args = append(args, "--lang", lang)
	}
	b, err := c.run(app, "", args...)
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) Remove(app, word string) (Receipt, error) {
	b, err := c.run(app, "", "remove", word, "--yes")
	if err != nil {
		return Receipt{}, err
	}
	return decode[Receipt](b)
}

func (c Client) Reset(app, word string) (OkWrap, error) {
	b, err := c.run(app, "", "reset", word, "--yes")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) Postpone(app, word string) (OkWrap, error) {
	b, err := c.run(app, "", "postpone", word, "--yes")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) CategoryAdmin(app, action, category string) (OkWrap, error) {
	var cmd string
	switch action {
	case "clear":
		cmd = "clear-category"
	case "remove":
		cmd = "remove-category"
	default:
		cmd = "reset-category"
	}
	b, err := c.run(app, "", cmd, "--category", category, "--yes")
	if err != nil {
		return OkWrap{}, err
	}
	return decode[OkWrap](b)
}

func (c Client) Goal(app string, set *int64) (string, error) {
	args := []string{"goal"}
	if set != nil {
		args = append(args, "--set", strconv.FormatInt(*set, 10))
	}
	b, err := c.run(app, "", args...)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Work copies the app's backup to out as a fresh working copy.
func (c Client) Work(app, out string) error {
	_, err := c.Remote().run(app, "", "work", "--out", out)
	return err
}

// Next deals the next card of a session — smart, review or new — the way
// the phone deals it, keeping exclude (the card just answered) out.
func (c Client) Next(app, session string, exclude int64) (Deal, error) {
	args := []string{"next", "--session", session}
	if exclude != 0 {
		args = append(args, "--exclude", strconv.FormatInt(exclude, 10))
	}
	b, err := c.run(app, "", args...)
	if err != nil {
		return Deal{}, err
	}
	return decode[Deal](b)
}

// Check grades a typed answer to one side ("rec" or "rep") of a word the way
// the phone's keyboard block does.
func (c Client) Check(app string, word int64, side, typed string) (Check, error) {
	// "=" keeps an answer that starts with a dash from reading as a flag.
	b, err := c.run(app, "", "check", "--word", strconv.FormatInt(word, 10), "--side", side, "--typed="+typed)
	if err != nil {
		return Check{}, err
	}
	return decode[Check](b)
}

// Settings reads the settings the phone shares through the backup.
func (c Client) Settings(app string) (Synced, error) {
	b, err := c.run(app, "", "settings")
	if err != nil {
		return Synced{}, err
	}
	return decode[Synced](b)
}

// Card deals one word's card again with the choose-from-4 it showed, as the
// phone does after an undo. side is 1 for recognition, 2 for reproduction.
func (c Client) Card(app string, word, side int64, variants []int64) (Deal, error) {
	args := []string{"card", "--word", strconv.FormatInt(word, 10), "--side", strconv.FormatInt(side, 10)}
	if len(variants) > 0 {
		ids := make([]string, len(variants))
		for i, v := range variants {
			ids[i] = strconv.FormatInt(v, 10)
		}
		args = append(args, "--variants", strings.Join(ids, ","))
	}
	b, err := c.run(app, "", args...)
	if err != nil {
		return Deal{}, err
	}
	return decode[Deal](b)
}

func IsDirty(err error) bool {
	return err != nil && strings.Contains(err.Error(), "DIRTY_SOURCE")
}
