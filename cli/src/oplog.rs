use crate::model::{CardMode, WordId};
use crate::sync::Fingerprint;
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
pub const OP_VERSION: u32 = 1;
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum OpKind {
    Added {
        id: i64,
        text: String,
        transcription: Option<String>,
        tr: Vec<(String, String)>,
    },
    Graded {
        id: i64,
        mode: i64,
        ok: bool,
        pre_e: f64,
        pre_f: i64,
    },
    Triaged {
        id: i64,
        decision: String,
    },
    Enrolled {
        id: i64,
    },
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Op {
    pub v: u32,
    pub seq: u64,
    pub ts: i64,
    pub app: String,
    #[serde(flatten)]
    pub kind: OpKind,
    pub pre: Fingerprint,
    pub post: Fingerprint,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Orphan {
    pub ts: i64,
    pub app: String,
    pub reason: String,
    pub op: Option<OpKind>,
    pub word_query: Option<String>,
}
pub fn oplog_path(data_dir: &Path) -> PathBuf {
    data_dir.join("oplog.jsonl")
}
pub fn orphans_path(data_dir: &Path) -> PathBuf {
    data_dir.join("orphans.jsonl")
}
fn read_lines(path: &Path) -> Vec<String> {
    std::fs::read_to_string(path)
        .map(|s| s.lines().map(str::to_string).collect())
        .unwrap_or_default()
}
fn next_seq(data_dir: &Path) -> u64 {
    read_lines(&oplog_path(data_dir))
        .iter()
        .filter_map(|l| serde_json::from_str::<Op>(l).ok())
        .map(|o| o.seq)
        .max()
        .unwrap_or(0)
        + 1
}
pub fn append(
    data_dir: &Path,
    app_id: &str,
    ts: i64,
    kind: OpKind,
    pre: &Fingerprint,
    post: &Fingerprint,
) -> Result<Op> {
    if let Some(parent) = oplog_path(data_dir).parent() {
        std::fs::create_dir_all(parent)
            .with_context(|| format!("cannot create {}", parent.display()))?;
    }
    let op = Op {
        v: OP_VERSION,
        seq: next_seq(data_dir),
        ts,
        app: app_id.to_string(),
        kind,
        pre: pre.clone(),
        post: post.clone(),
    };
    let mut f = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(oplog_path(data_dir))
        .context("cannot open oplog")?;
    use std::io::Write as _;
    writeln!(f, "{}", serde_json::to_string(&op)?).context("cannot append op")?;
    f.sync_all().ok();
    Ok(op)
}
pub fn tail(data_dir: &Path, limit: usize) -> Vec<Op> {
    let mut ops: Vec<Op> = read_lines(&oplog_path(data_dir))
        .iter()
        .filter_map(|l| serde_json::from_str(l).ok())
        .collect();
    ops.sort_by_key(|o| o.seq);
    let skip = ops.len().saturating_sub(limit);
    ops.into_iter().skip(skip).collect()
}
pub fn shelve_orphan(
    data_dir: &Path,
    ts: i64,
    app_id: &str,
    reason: &str,
    op: Option<OpKind>,
    word_query: Option<String>,
) -> Result<()> {
    if let Some(parent) = orphans_path(data_dir).parent() {
        std::fs::create_dir_all(parent)
            .with_context(|| format!("cannot create {}", parent.display()))?;
    }
    let o = Orphan {
        ts,
        app: app_id.to_string(),
        reason: reason.to_string(),
        op,
        word_query,
    };
    let mut f = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(orphans_path(data_dir))
        .context("cannot open orphan shelf")?;
    use std::io::Write as _;
    writeln!(f, "{}", serde_json::to_string(&o)?).context("cannot shelve orphan")?;
    f.sync_all().ok();
    Ok(())
}
pub fn orphans(data_dir: &Path) -> Vec<Orphan> {
    read_lines(&orphans_path(data_dir))
        .iter()
        .filter_map(|l| serde_json::from_str(l).ok())
        .collect()
}
pub fn replay_decision(conn: &rusqlite::Connection, app_id: &str, op: &Op) -> Result<ReplayAction> {
    if op.app != app_id {
        return Ok(ReplayAction::Skip("other app".to_string()));
    }
    let exists = |id: i64| -> Result<bool> {
        conn.query_row("SELECT COUNT(*) FROM WORD WHERE ID=?", [id], |r| {
            r.get::<_, i64>(0)
        })
        .map(|n| n > 0)
        .context("orphan check")
    };
    let q_of = |id: i64| -> Result<(i64, i64)> {
        conn.query_row("SELECT Q_REC, Q_REP FROM WORD WHERE ID=?", [id], |r| {
            Ok((r.get(0)?, r.get(1)?))
        })
        .context("Q check")
    };
    match &op.kind {
        OpKind::Added {
            id,
            text,
            transcription,
            tr,
        } => {
            if exists(*id)? {
                Ok(ReplayAction::Skip("already exists".to_string()))
            } else {
                Ok(ReplayAction::ApplyAdd {
                    id: WordId(*id),
                    text: text.clone(),
                    transcription: transcription.clone(),
                    tr: tr.clone(),
                })
            }
        }
        OpKind::Graded {
            id,
            mode,
            ok,
            pre_e,
            pre_f,
        } => {
            if !exists(*id)? {
                return Ok(ReplayAction::Orphan("word missing".to_string()));
            }
            if *ok {
                let covered: i64 = conn
                    .query_row(
                        "SELECT COUNT(*) FROM LOG WHERE WORD_ID=? AND MODE=?
                         AND QUEUE=2 AND TIMESTAMP>=?",
                        rusqlite::params![*id, *mode, op.ts],
                        |r| r.get(0),
                    )
                    .unwrap_or(0);
                if covered > 0 {
                    Ok(ReplayAction::Skip("covered by a review row".to_string()))
                } else {
                    Ok(ReplayAction::ApplyGrade {
                        id: WordId(*id),
                        mode: CardMode::from_i64(*mode),
                    })
                }
            } else {
                let sfx = if *mode == 1 { "REC" } else { "REP" };
                let cur: Option<(f64, i64)> = conn
                    .query_row(
                        &format!("SELECT E_{sfx}, F_{sfx} FROM WORD WHERE ID=?"),
                        [*id],
                        |r| Ok((r.get(0)?, r.get(1)?)),
                    )
                    .ok();
                match cur {
                    Some((e, f)) if e == *pre_e && f == *pre_f => Ok(ReplayAction::ApplyGrade {
                        id: WordId(*id),
                        mode: CardMode::from_i64(*mode),
                    }),
                    _ => Ok(ReplayAction::Skip(
                        "fail already applied/diverged".to_string(),
                    )),
                }
            }
        }
        OpKind::Triaged { id, decision } => {
            if !exists(*id)? {
                return Ok(ReplayAction::Orphan("word missing".to_string()));
            }
            let (qr, _qp) = q_of(*id)?;
            let target = if decision == "known" { 3 } else { 2 };
            if qr >= target {
                Ok(ReplayAction::Skip("Q already at target".to_string()))
            } else {
                Ok(ReplayAction::ApplyTriage {
                    id: WordId(*id),
                    known: decision == "known",
                })
            }
        }
        OpKind::Enrolled { id } => {
            if !exists(*id)? {
                return Ok(ReplayAction::Orphan("word missing".to_string()));
            }
            let (qr, _qp) = q_of(*id)?;
            if qr >= 2 {
                Ok(ReplayAction::Skip("already enrolled".to_string()))
            } else {
                Ok(ReplayAction::ApplyEnroll { id: WordId(*id) })
            }
        }
    }
}
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ReplayAction {
    Skip(String),
    Orphan(String),
    ApplyAdd {
        id: WordId,
        text: String,
        transcription: Option<String>,
        tr: Vec<(String, String)>,
    },
    ApplyGrade {
        id: WordId,
        mode: CardMode,
    },
    ApplyTriage {
        id: WordId,
        known: bool,
    },
    ApplyEnroll {
        id: WordId,
    },
}
#[cfg(test)]
mod tests {
    use super::*;
    fn fp(words: i64, log: i64, max: i64) -> Fingerprint {
        Fingerprint {
            size_bytes: 1,
            mtime_ns: 1,
            words,
            log_rows: log,
            max_log_id: max,
        }
    }
    #[test]
    fn append_tail_and_replay_skips() {
        let tmp = tempfile::tempdir().unwrap();
        let data = tmp.path();
        let db = data.join("t.db");
        let conn = rusqlite::Connection::open(&db).unwrap();
        conn.execute_batch(
            "CREATE TABLE WORD (ID INTEGER PRIMARY KEY, Q_REC INTEGER DEFAULT 0,
             Q_REP INTEGER DEFAULT 0, T_REC INTEGER DEFAULT NULL, T_REP INTEGER DEFAULT NULL,
             E_REC REAL DEFAULT 2.5, F_REC INTEGER DEFAULT 0);
             CREATE TABLE LOG (ID INTEGER PRIMARY KEY, TIMESTAMP INTEGER NOT NULL,
              LOCAL_DATE TEXT NOT NULL, WORD_ID INTEGER NOT NULL, MODE INTEGER NOT NULL,
              QUEUE INTEGER NOT NULL, STEP INTEGER NOT NULL, NQUEUE INTEGER NOT NULL,
              FLAGS INTEGER NOT NULL DEFAULT 0);
             INSERT INTO WORD VALUES (1, 0, 0, NULL, NULL, 2.5, 0);
             INSERT INTO WORD VALUES (2, 3, 3, 100, 100, 3.0, 0);
             INSERT INTO WORD VALUES (3, 2, 2, 50, 50, 2.0, 1);
             INSERT INTO LOG VALUES (1, 100, '2026-09-13', 2, 1, 2, 1, 2, 0);",
        )
        .unwrap();
        conn.close().unwrap();
        append(
            data,
            "es",
            50,
            OpKind::Graded {
                id: 2,
                mode: 1,
                ok: true,
                pre_e: 3.0,
                pre_f: 0,
            },
            &fp(2, 0, 0),
            &fp(2, 1, 1),
        )
        .unwrap();
        append(
            data,
            "es",
            200,
            OpKind::Graded {
                id: 1,
                mode: 2,
                ok: true,
                pre_e: 2.5,
                pre_f: 0,
            },
            &fp(2, 0, 0),
            &fp(2, 1, 1),
        )
        .unwrap();
        append(
            data,
            "es",
            300,
            OpKind::Graded {
                id: 9,
                mode: 1,
                ok: false,
                pre_e: 2.5,
                pre_f: 0,
            },
            &fp(2, 0, 0),
            &fp(2, 1, 1),
        )
        .unwrap();
        append(
            data,
            "en",
            400,
            OpKind::Graded {
                id: 1,
                mode: 1,
                ok: true,
                pre_e: 2.5,
                pre_f: 0,
            },
            &fp(1, 0, 0),
            &fp(1, 1, 1),
        )
        .unwrap();
        append(
            data,
            "es",
            500,
            OpKind::Graded {
                id: 3,
                mode: 1,
                ok: false,
                pre_e: 2.0,
                pre_f: 1,
            },
            &fp(3, 0, 0),
            &fp(3, 1, 1),
        )
        .unwrap();
        append(
            data,
            "es",
            600,
            OpKind::Graded {
                id: 3,
                mode: 1,
                ok: false,
                pre_e: 2.5,
                pre_f: 0,
            },
            &fp(3, 0, 0),
            &fp(3, 1, 1),
        )
        .unwrap();
        let ops = tail(data, 100);
        assert_eq!(ops.len(), 6);
        assert_eq!(ops[0].seq + 1, ops[1].seq);
        let conn = rusqlite::Connection::open(&db).unwrap();
        let acts: Vec<ReplayAction> = ops
            .iter()
            .map(|o| replay_decision(&conn, "es", o).unwrap())
            .collect();
        assert!(matches!(acts[0], ReplayAction::Skip(_)));
        assert!(matches!(acts[1], ReplayAction::ApplyGrade { .. }));
        assert!(matches!(acts[2], ReplayAction::Orphan(_)));
        assert!(matches!(acts[3], ReplayAction::Skip(_)));
        assert!(matches!(acts[4], ReplayAction::ApplyGrade { .. }));
        assert!(matches!(acts[5], ReplayAction::Skip(_)));
        shelve_orphan(data, 1, "es", "test", None, Some("x".to_string())).unwrap();
        assert_eq!(orphans(data).len(), 1);
    }
}
