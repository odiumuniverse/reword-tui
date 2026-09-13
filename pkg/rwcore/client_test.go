package rwcore

import (
	"os"
	"path/filepath"
	"testing"
)

func stub(t *testing.T, script string) Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rwcore")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Client{Bin: path}
}

func TestAppsDecode(t *testing.T) {
	c := stub(t, `cat <<'EOF'
[{"n":1,"id":"es","container":"c","backup":"/tmp/x","size_bytes":5,"mtime_secs":7}]
EOF`)
	apps, err := c.Apps()
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].ID != "es" || apps[0].N != 1 {
		t.Fatalf("bad apps: %+v", apps)
	}
}

func TestDueDecode(t *testing.T) {
	c := stub(t, `cat <<'EOF'
[{"id":3,"word":"el pan","modes":[1,2],"overdue_secs":99}]
EOF`)
	due, err := c.Due("es", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Modes[0] != 1 || due[0].OverdueSecs != 99 {
		t.Fatalf("bad due: %+v", due)
	}
}

func TestOplogFlattenedKind(t *testing.T) {
	c := stub(t, `cat <<'EOF'
[{"v":1,"seq":1,"ts":100,"app":"es","kind":"triaged","id":1,"decision":"learn",
"pre":{"size_bytes":1,"mtime_ns":1,"words":2,"log_rows":0,"max_log_id":0},
"post":{"size_bytes":1,"mtime_ns":1,"words":2,"log_rows":2,"max_log_id":2}}]
EOF`)
	ops, err := c.Oplog(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Kind.Name != "triaged" || ops[0].Seq != 1 {
		t.Fatalf("bad oplog decode: %+v", ops)
	}
}

func TestErrorPassthrough(t *testing.T) {
	c := stub(t, `echo "DIRTY_SOURCE app=es drift detected" >&2; exit 1`)
	_, err := c.Stats("es")
	if err == nil || !IsDirty(err) {
		t.Fatalf("want dirty error, got %v", err)
	}
}

func TestApplyStdin(t *testing.T) {
	c := stub(t, `read body; echo "$body" | grep -q '"result":"ok"' || exit 1
echo '{"ok":true,"message":"applied","detail":{"applied":true,"orphaned":false,"snapshot":null,"detail":{}}}'`)
	r, err := c.Apply("es", map[string]any{"op": "grade", "word": "x", "mode": "rec", "result": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Detail.Applied || r.Detail.Orphaned {
		t.Fatalf("bad receipt: %+v", r)
	}
}
