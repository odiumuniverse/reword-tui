package rwcore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const fixtureSQL = `
CREATE TABLE SETTINGS (NAME TEXT PRIMARY KEY NOT NULL, VALUE TEXT);
CREATE TABLE CATEGORY (ID TEXT PRIMARY KEY NOT NULL, IS_CUSTOM INTEGER NOT NULL,
    IS_SELECTED INTEGER NOT NULL, CUSTOM_ICON TEXT DEFAULT NULL, REG INTEGER DEFAULT NULL,
    NAME_ENG TEXT);
CREATE TABLE WORD (ID INTEGER PRIMARY KEY, WORD TEXT NOT NULL DEFAULT '',
    TRANSCRIPTION TEXT DEFAULT NULL, ENG TEXT DEFAULT NULL, EXAMPLES_ENG TEXT DEFAULT NULL,
    RUS TEXT DEFAULT NULL, Q_REC INTEGER NOT NULL DEFAULT 0, Q_REP INTEGER NOT NULL DEFAULT 0,
    T_REC INTEGER DEFAULT NULL, T_REP INTEGER DEFAULT NULL,
    I_REC INTEGER DEFAULT NULL, I_REP INTEGER DEFAULT NULL,
    S_REC INTEGER NOT NULL DEFAULT 0, S_REP INTEGER NOT NULL DEFAULT 0,
    E_REC REAL NOT NULL DEFAULT 2.5, E_REP REAL NOT NULL DEFAULT 2.5,
    F_REC INTEGER NOT NULL DEFAULT 0, F_REP INTEGER NOT NULL DEFAULT 0);
CREATE TABLE WORD_CATEGORY (ID INTEGER PRIMARY KEY, WORD_ID INTEGER NOT NULL, CATEGORY_ID TEXT NOT NULL);
CREATE TABLE LOG (ID INTEGER PRIMARY KEY, TIMESTAMP INTEGER NOT NULL, LOCAL_DATE TEXT NOT NULL,
    WORD_ID INTEGER NOT NULL, MODE INTEGER NOT NULL, QUEUE INTEGER NOT NULL, STEP INTEGER NOT NULL,
    NQUEUE INTEGER NOT NULL, FLAGS INTEGER NOT NULL DEFAULT 0);
CREATE TABLE PICTURE (ID INTEGER PRIMARY KEY, SOURCE TEXT NOT NULL, SOURCE_ID TEXT NOT NULL,
    CONTENT BLOB DEFAULT NULL, IS_CUSTOM INTEGER NOT NULL DEFAULT 0);
CREATE TABLE AUDIO (ID TEXT PRIMARY KEY, IS_CUSTOM INTEGER NOT NULL DEFAULT 0,
    CONTENT BLOB DEFAULT NULL, VAR INTEGER DEFAULT NULL);
CREATE TABLE DAILY_GOAL (DATE TEXT PRIMARY KEY, GOAL INTEGER DEFAULT NULL,
    ADJUSTED_GOAL INTEGER DEFAULT NULL);
INSERT INTO SETTINGS VALUES ('native_language', 'RUS'), ('daily_goal', '30'), ('ui_language', 'en');
INSERT INTO CATEGORY VALUES ('custom', 1, 1, NULL, NULL, 'My words');
INSERT INTO CATEGORY VALUES ('food', 0, 0, NULL, NULL, 'Food');
INSERT INTO WORD (ID, WORD, ENG, RUS) VALUES (1, 'el pan', 'bread', 'хлеб');
INSERT INTO WORD (ID, WORD, ENG, RUS) VALUES (2, 'la leche', 'milk', 'молоко');
INSERT INTO WORD_CATEGORY VALUES (1, 1, 'custom');
INSERT INTO WORD_CATEGORY VALUES (2, 2, 'food');
`

func sqlite(t *testing.T, db, sql string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 missing")
	}
	script := fmt.Sprintf("import sqlite3;c=sqlite3.connect(%q);c.executescript(%q);c.commit()", db, sql)
	if out, err := exec.Command(py, "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("sqlite: %v %s", err, out)
	}
}

func e2eClient(t *testing.T) (Client, string) {
	t.Helper()
	bin := os.Getenv("RWCORE_BIN")
	if bin == "" {
		t.Skip("RWCORE_BIN unset")
	}
	root := t.TempDir()
	docs := filepath.Join(root, "icloud", "iCloud~ru~poas~englishwords~esen", "Documents")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(docs, "reword_es.backup")
	sqlite(t, db, fixtureSQL)
	c := Client{
		Bin:        bin,
		IcloudRoot: filepath.Join(root, "icloud"),
		CacheDir:   filepath.Join(root, "cache"),
		DataDir:    filepath.Join(root, "data"),
	}
	return c, db
}

func mustReceipt(t *testing.T, r Receipt, err error) Receipt {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if !r.Detail.Applied || r.Detail.Orphaned {
		t.Fatalf("bad receipt: %+v", r)
	}
	return r
}

func TestE2EWritePath(t *testing.T) {
	c, _ := e2eClient(t)
	apps, err := c.Apps()
	if err != nil || len(apps) != 1 || apps[0].ID != "es" {
		t.Fatalf("apps: %+v %v", apps, err)
	}
	st, err := c.Stats("es")
	if err != nil || st.Words != 2 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	cats, err := c.Categories("es")
	if err != nil || len(cats) != 2 || !cats[0].Selected {
		t.Fatalf("cats: %+v %v", cats, err)
	}
	due, err := c.Due("es", 5000)
	if err != nil || len(due) != 0 {
		t.Fatalf("fresh db must have empty due: %+v %v", due, err)
	}
	r1, err1 := c.Apply("es", map[string]any{"op": "triage", "word": "el pan", "decision": "learn"})
	mustReceipt(t, r1, err1)
	r1b, err1b := c.Apply("es", map[string]any{"op": "triage", "word": "el pan", "decision": "learn"})
	mustReceipt(t, r1b, err1b)
	r2, err2 := c.Apply("es", map[string]any{"op": "grade", "word": "el pan", "mode": "rep", "result": "ok"})
	mustReceipt(t, r2, err2)
	w, err := c.Show("es", "el pan")
	if err != nil || w.Reproduction.Level != 2 || w.Reproduction.Step != 1 || w.Recognition.Level != 2 {
		t.Fatalf("show: %+v %v", w, err)
	}
	td, err := c.Today("es")
	if err != nil || td.Learned != 1 || td.Reviewed != 0 || td.StreakCur != 1 {
		t.Fatalf("today: %+v %v", td, err)
	}
	if _, err := c.Select("es", "food", true); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Status("es")
	if err != nil || rows[0].State != "clean" {
		t.Fatalf("status: %+v %v", rows, err)
	}
}

func TestE2EDirtyGate(t *testing.T) {
	c, db := e2eClient(t)
	r3, err3 := c.Apply("es", map[string]any{"op": "triage", "word": "el pan", "decision": "learn"})
	mustReceipt(t, r3, err3)
	sqlite(t, db, "INSERT INTO WORD (ID, WORD) VALUES (99, 'intruder');")
	_, err := c.Apply("es", map[string]any{"op": "grade", "word": "el pan", "mode": "rec", "result": "ok"})
	if !IsDirty(err) {
		t.Fatalf("want DIRTY_SOURCE, got %v", err)
	}
	if _, err := c.Pull("es"); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Status("es")
	if err != nil || rows[0].State != "clean" {
		t.Fatalf("status after pull: %+v %v", rows, err)
	}
	out, err := c.Replay("es", false)
	if err != nil || len(out) == 0 {
		t.Fatalf("replay dry-run: %v", err)
	}
}
