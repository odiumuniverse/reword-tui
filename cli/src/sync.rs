use crate::discover::App;
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Fingerprint {
    pub size_bytes: u64,
    pub mtime_ns: u64,
    pub words: i64,
    pub log_rows: i64,
    pub max_log_id: i64,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct GateState {
    apps: HashMap<String, Fingerprint>,
}
fn state_path(data_dir: &Path) -> PathBuf {
    data_dir.join("fingerprints.json")
}
fn load_state(data_dir: &Path) -> GateState {
    std::fs::read_to_string(state_path(data_dir))
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}
fn save_state(data_dir: &Path, st: &GateState) -> Result<()> {
    std::fs::create_dir_all(data_dir)
        .with_context(|| format!("cannot create {}", data_dir.display()))?;
    std::fs::write(state_path(data_dir), serde_json::to_string_pretty(st)?)
        .with_context(|| format!("cannot write {}", state_path(data_dir).display()))?;
    Ok(())
}
fn mtime_ns(path: &Path) -> Result<u64> {
    let md = std::fs::metadata(path).with_context(|| format!("cannot stat {}", path.display()))?;
    Ok(md
        .modified()
        .unwrap_or(SystemTime::UNIX_EPOCH)
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos() as u64)
        .unwrap_or(0))
}
pub fn fingerprint(app: &App) -> Result<Fingerprint> {
    let meta = crate::store::retry(
        || {
            std::fs::metadata(&app.backup_path)
                .with_context(|| format!("cannot stat {}", app.backup_path.display()))
        },
        "stat backup",
    )?;
    let mtime = mtime_ns(&app.backup_path)?;
    let conn = crate::store::open_ro(&app.backup_path)?;
    let q = |sql: &str| -> Result<i64> {
        conn.query_row(sql, [], |r| r.get(0))
            .with_context(|| format!("cannot run {sql}"))
    };
    Ok(Fingerprint {
        size_bytes: meta.len(),
        mtime_ns: mtime,
        words: q("SELECT COUNT(*) FROM WORD")?,
        log_rows: q("SELECT COUNT(*) FROM LOG")?,
        max_log_id: q("SELECT COALESCE(MAX(ID), 0) FROM LOG")?,
    })
}
pub fn stored(data_dir: &Path, app_id: &str) -> Option<Fingerprint> {
    load_state(data_dir).apps.get(app_id).cloned()
}
pub fn adopt(data_dir: &Path, app: &App, fp: &Fingerprint) -> Result<()> {
    let mut st = load_state(data_dir);
    st.apps.insert(app.id.clone(), fp.clone());
    save_state(data_dir, &st)
}
pub fn check(data_dir: &Path, app: &App, current: &Fingerprint) -> Result<()> {
    match stored(data_dir, &app.id) {
        None => Ok(()),
        Some(want) if want == *current => Ok(()),
        Some(want) => anyhow::bail!(
            "DIRTY_SOURCE app={} drift detected (writes blocked):\n  stored:  {:?}\n  current: {:?}\nrun `pull` to adopt the iCloud state, or investigate first",
            app.id,
            want,
            current
        ),
    }
}
pub fn pull(data_dir: &Path, app: &App) -> Result<PathBuf> {
    let qdir = data_dir
        .join("quarantine")
        .join(format!("{}-{}", epoch_secs(), app.id));
    std::fs::create_dir_all(&qdir).with_context(|| format!("cannot create {}", qdir.display()))?;
    let qfile = qdir.join(&app.backup_name);
    std::fs::copy(&app.backup_path, &qfile).with_context(|| {
        format!(
            "cannot quarantine {} -> {}",
            app.backup_path.display(),
            qfile.display()
        )
    })?;
    let fp = fingerprint(app)?;
    adopt(data_dir, app, &fp)?;
    Ok(qfile)
}
fn epoch_secs() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}
#[cfg(test)]
mod tests {
    use super::*;
    fn fixture_app() -> (tempfile::TempDir, App, PathBuf) {
        let tmp = tempfile::tempdir().unwrap();
        let docs = tmp
            .path()
            .join("icloud")
            .join("iCloud~ru~poas~englishwords~esen")
            .join("Documents");
        std::fs::create_dir_all(&docs).unwrap();
        let db = docs.join("reword_es.backup");
        let conn = rusqlite::Connection::open(&db).unwrap();
        conn.execute_batch(
            "CREATE TABLE WORD (ID INTEGER PRIMARY KEY, WORD TEXT);
             CREATE TABLE LOG (ID INTEGER PRIMARY KEY);
             INSERT INTO WORD VALUES (1, 'x');",
        )
        .unwrap();
        conn.close().unwrap();
        let data = tmp.path().join("data");
        let app = App {
            id: "es".to_string(),
            container: "iCloud~ru~poas~englishwords~esen".to_string(),
            backup_path: db,
            backup_name: "reword_es.backup".to_string(),
        };
        (tmp, app, data)
    }
    #[test]
    fn gate_blocks_external_drift_until_pull() {
        let (_tmp, app, data) = fixture_app();
        let fp0 = fingerprint(&app).unwrap();
        check(&data, &app, &fp0).unwrap();
        adopt(&data, &app, &fp0).unwrap();
        check(&data, &app, &fingerprint(&app).unwrap()).unwrap();
        {
            let conn = rusqlite::Connection::open(&app.backup_path).unwrap();
            conn.execute("INSERT INTO WORD VALUES (2, 'y')", [])
                .unwrap();
            conn.close().unwrap();
        }
        let fp1 = fingerprint(&app).unwrap();
        assert_ne!(fp0, fp1);
        let err = check(&data, &app, &fp1).unwrap_err();
        assert!(format!("{err:#}").contains("DIRTY_SOURCE"));
        let q = pull(&data, &app).unwrap();
        assert!(q.exists());
        check(&data, &app, &fingerprint(&app).unwrap()).unwrap();
    }
}
