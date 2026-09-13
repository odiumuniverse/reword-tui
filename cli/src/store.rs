use crate::discover::App;
use crate::model::{
    AttemptKind, CardMode, Category, Lang, LogEntry, ModeState, Queue, Settings, Stats, Word,
    WordId,
};
use anyhow::{Context, Result};
use rusqlite::{Connection, OpenFlags, Row, Transaction};
use std::collections::HashMap;
use std::fs::OpenOptions;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};
fn is_base_col(name: &str) -> bool {
    matches!(
        name,
        "ID" | "EXT_SOURCE"
            | "EXT_SOURCE_ID"
            | "WORD"
            | "POS"
            | "REG"
            | "TRANSCRIPTION"
            | "PICTURE_ID"
            | "Q_REC"
            | "Q_REP"
            | "T_REC"
            | "T_REP"
            | "I_REC"
            | "I_REP"
            | "S_REC"
            | "S_REP"
            | "E_REC"
            | "E_REP"
            | "F_REC"
            | "F_REP"
    )
}
pub fn open_ro(path: &Path) -> Result<Connection> {
    Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_ONLY)
        .with_context(|| format!("cannot open {} read-only", path.display()))
}
pub fn settings(conn: &Connection) -> Result<Settings> {
    let mut st = conn.prepare("SELECT NAME, VALUE FROM SETTINGS")?;
    let rows = st.query_map([], |r| {
        let k: String = r.get(0)?;
        let v: Option<String> = r.get(1)?;
        Ok((k, v))
    })?;
    let mut map: HashMap<String, String> = HashMap::new();
    for r in rows {
        let (k, v) = r.context("decode SETTINGS row")?;
        map.insert(k, v.context("NULL SETTINGS value")?);
    }
    Ok(Settings {
        native_language: map.remove("native_language"),
        daily_goal: map.remove("daily_goal"),
        ui_language: map.remove("ui_language"),
    })
}
pub fn lang_cols(conn: &Connection) -> Result<Vec<Lang>> {
    let mut st = conn.prepare("PRAGMA table_info(WORD)")?;
    let cols = st.query_map([], |r| r.get::<_, String>(1))?;
    let mut out = Vec::new();
    for name in cols {
        let name = name.context("decode PRAGMA row")?;
        if name.len() == 3
            && name.chars().all(|c| c.is_ascii_uppercase())
            && !is_base_col(&name)
            && let Some(lang) = Lang::parse(&name)
        {
            out.push(lang);
        }
    }
    out.sort_by(|a, b| a.code().cmp(b.code()));
    Ok(out)
}
fn col_index(conn: &Connection) -> Result<HashMap<String, usize>> {
    let mut st = conn.prepare("PRAGMA table_info(WORD)")?;
    let rows = st.query_map([], |r| {
        let i: usize = r.get(0)?;
        let n: String = r.get(1)?;
        Ok((n, i))
    })?;
    rows.map(|r| r.context("decode PRAGMA row"))
        .collect::<Result<HashMap<_, _>>>()
}
fn mode_state(
    row: &Row,
    idx: &HashMap<String, usize>,
    suffix: &str,
) -> rusqlite::Result<ModeState> {
    let col = |base: &str| idx[&format!("{base}_{suffix}")];
    Ok(ModeState {
        level: row.get(col("Q"))?,
        step: row.get(col("S"))?,
        easiness: row.get(col("E"))?,
        fails: row.get(col("F"))?,
        last_review_ts: row.get(col("T"))?,
        interval_secs: row.get(col("I"))?,
    })
}
fn word_from_row(
    row: &Row,
    langs: &[Lang],
    idx: &HashMap<String, usize>,
) -> rusqlite::Result<Word> {
    let id: i64 = row.get(idx["ID"])?;
    let text: String = row.get(idx["WORD"])?;
    let transcription: Option<String> = row.get(idx["TRANSCRIPTION"])?;
    let mut translations = std::collections::BTreeMap::new();
    let mut examples = std::collections::BTreeMap::new();
    for lang in langs {
        let code = lang.code();
        if let Some(i) = idx.get(code) {
            let v: Option<String> = row.get(*i)?;
            if let Some(t) = v
                && !t.trim().is_empty()
            {
                translations.insert(lang.clone(), t);
            }
        }
        let ex_col = format!("EXAMPLES_{code}");
        if let Some(&i) = idx.get(ex_col.as_str()) {
            let v: Option<String> = row.get(i)?;
            if let Some(t) = v
                && !t.trim().is_empty()
            {
                examples.insert(lang.clone(), t);
            }
        }
    }
    Ok(Word {
        id: WordId(id),
        text,
        transcription,
        translations,
        examples,
        recognition: mode_state(row, idx, "REC")?,
        reproduction: mode_state(row, idx, "REP")?,
    })
}
fn log_from_row(row: &Row) -> rusqlite::Result<LogEntry> {
    let mode: i64 = row.get("MODE")?;
    let queue: i64 = row.get("QUEUE")?;
    let nqueue: i64 = row.get("NQUEUE")?;
    let flags: i64 = row.get("FLAGS")?;
    Ok(LogEntry {
        id: row.get("ID")?,
        ts: row.get("TIMESTAMP")?,
        date: row.get("LOCAL_DATE")?,
        word_id: WordId(row.get("WORD_ID")?),
        mode: CardMode::from_i64(mode),
        queue: Queue::from_i64(queue),
        step: row.get("STEP")?,
        next_queue: Queue::from_i64(nqueue),
        kind: AttemptKind::from_i64(flags),
    })
}
pub fn log_entries(conn: &Connection, word: WordId, limit: usize) -> Result<Vec<LogEntry>> {
    let mut st = conn.prepare(
        "SELECT ID, TIMESTAMP, LOCAL_DATE, WORD_ID, MODE, QUEUE, STEP, NQUEUE, FLAGS
         FROM LOG WHERE WORD_ID = ? ORDER BY ID DESC LIMIT ?",
    )?;
    let rows = st.query_map(rusqlite::params![word.0, limit as i64], log_from_row)?;
    let mut out = rows
        .map(|r| r.context("decode LOG row"))
        .collect::<Result<Vec<_>>>()?;
    out.reverse();
    Ok(out)
}
pub fn due_words(conn: &Connection, now: i64) -> Result<Vec<(Word, Vec<CardMode>)>> {
    let langs = lang_cols(conn)?;
    let idx = col_index(conn)?;
    let mut st = conn.prepare(
        "SELECT WORD.* FROM WORD
         JOIN WORD_CATEGORY wc ON wc.WORD_ID = WORD.ID
         JOIN CATEGORY c ON c.ID = wc.CATEGORY_ID
         WHERE c.IS_SELECTED = 1
         GROUP BY WORD.ID ORDER BY WORD.ID",
    )?;
    let rows = st.query_map([], |r| word_from_row(r, &langs, &idx))?;
    let mut out = Vec::new();
    for r in rows {
        let w = r.context("decode WORD row")?;
        let modes = crate::interval::due_modes(&w, now);
        if !modes.is_empty() {
            out.push((w, modes));
        }
    }
    Ok(out)
}
pub struct WordFilter<'a> {
    pub search: Option<&'a str>,
    pub category: Option<&'a str>,
    pub limit: i64,
}
pub fn list_words(conn: &Connection, f: &WordFilter) -> Result<Vec<Word>> {
    let langs = lang_cols(conn)?;
    let idx = col_index(conn)?;
    let mut sql = String::from("SELECT WORD.* FROM WORD");
    if f.category.is_some() {
        sql.push_str(
            " JOIN WORD_CATEGORY wc ON wc.WORD_ID = WORD.ID JOIN CATEGORY c ON c.ID = wc.CATEGORY_ID",
        );
    }
    let mut conds = Vec::new();
    if f.search.is_some() {
        conds.push("WORD.WORD LIKE '%' || REPLACE(REPLACE(REPLACE(?, '\\', '\\\\'), '%', '\\%'), '_', '\\_') || '%' ESCAPE '\\'");
    }
    if f.category.is_some() {
        conds.push("(c.ID = ? OR c.NAME_ENG = ?)");
    }
    if !conds.is_empty() {
        sql.push_str(" WHERE ");
        sql.push_str(&conds.join(" AND "));
    }
    sql.push_str(" GROUP BY WORD.ID ORDER BY WORD.ID LIMIT ?");
    let mut st = conn.prepare(&sql)?;
    if f.limit < 0 {
        anyhow::bail!("limit must be >= 0");
    }
    let mut params: Vec<rusqlite::types::Value> = Vec::new();
    if let Some(s) = f.search {
        params.push(rusqlite::types::Value::Text(s.to_string()));
    }
    if let Some(c) = f.category {
        params.push(rusqlite::types::Value::Text(c.to_string()));
        params.push(rusqlite::types::Value::Text(c.to_string()));
    }
    params.push(rusqlite::types::Value::Integer(f.limit));
    let rows = st.query_map(rusqlite::params_from_iter(params), |r| {
        word_from_row(r, &langs, &idx)
    })?;
    rows.map(|r| r.context("decode WORD row"))
        .collect::<Result<Vec<_>>>()
}
pub fn get_word(conn: &Connection, query: &str) -> Result<Option<Word>> {
    let langs = lang_cols(conn)?;
    let idx = col_index(conn)?;
    let (sql, params): (&str, Vec<&dyn rusqlite::ToSql>) = if query.parse::<i64>().is_ok() {
        (
            "SELECT * FROM WORD WHERE ID = ? ORDER BY ID LIMIT 1",
            vec![&query],
        )
    } else {
        (
            "SELECT * FROM WORD WHERE WORD = ? ORDER BY ID LIMIT 1",
            vec![&query],
        )
    };
    let mut st = conn.prepare(sql)?;
    let mut rows = st.query_map(params.as_slice(), |r| word_from_row(r, &langs, &idx))?;
    Ok(rows.next().transpose()?)
}
pub fn categories(conn: &Connection) -> Result<Vec<Category>> {
    let mut st = conn.prepare(
        "SELECT c.ID, c.IS_CUSTOM, c.NAME_ENG, COUNT(wc.WORD_ID)
         FROM CATEGORY c LEFT JOIN WORD_CATEGORY wc ON wc.CATEGORY_ID = c.ID
         GROUP BY c.ID ORDER BY c.IS_CUSTOM DESC, c.ID",
    )?;
    let rows = st.query_map([], |r| {
        Ok(Category {
            id: r.get(0)?,
            custom: r.get::<_, i64>(1)? != 0,
            name_en: r.get(2)?,
            words: r.get(3)?,
        })
    })?;
    rows.map(|r| r.context("decode CATEGORY row"))
        .collect::<Result<Vec<_>>>()
}
fn count(conn: &Connection, table: &str) -> Result<i64> {
    conn.query_row(&format!("SELECT COUNT(*) FROM {table}"), [], |r| r.get(0))
        .with_context(|| format!("cannot count {table}"))
}
pub fn stats(conn: &Connection) -> Result<Stats> {
    Ok(Stats {
        words: count(conn, "WORD")?,
        categories: count(conn, "CATEGORY")?,
        log_rows: count(conn, "LOG")?,
        pictures: count(conn, "PICTURE")?,
        audio: count(conn, "AUDIO")?,
        settings: settings(conn)?,
        last_log_ts: conn
            .query_row("SELECT MAX(TIMESTAMP) FROM LOG", [], |r| {
                r.get::<_, Option<i64>>(0)
            })
            .context("cannot read last LOG timestamp")?,
    })
}
fn epoch_secs() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}
static TMP_COUNTER: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
pub(crate) fn retry<T>(mut f: impl FnMut() -> Result<T>, what: &str) -> Result<T> {
    let mut last: Option<anyhow::Error> = None;
    for attempt in 0..4 {
        match f() {
            Ok(v) => return Ok(v),
            Err(e) => {
                last = Some(e);
                if attempt < 3 {
                    std::thread::sleep(std::time::Duration::from_millis(200));
                }
            }
        }
    }
    Err(last.unwrap()).with_context(|| format!("{what} failed after retries"))
}
fn lock_path(cache_dir: &Path, app_id: &str) -> PathBuf {
    cache_dir
        .join("locks")
        .join(format!("rwcore-{app_id}.lock"))
}
struct LockGuard {
    path: PathBuf,
}
impl LockGuard {
    fn acquire(cache_dir: &Path, app_id: &str) -> Result<LockGuard> {
        let path = lock_path(cache_dir, app_id);
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .with_context(|| format!("cannot create {}", parent.display()))?;
        }
        match OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(mut f) => {
                use std::io::Write as _;
                let _ = writeln!(f, "{}", std::process::id());
                Ok(LockGuard { path })
            }
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {
                anyhow::bail!(
                    "another rwcore writer holds {} (stale after a crash? remove it manually)",
                    path.display()
                )
            }
            Err(e) => Err(e).with_context(|| format!("cannot create lock {}", path.display())),
        }
    }
}
impl Drop for LockGuard {
    fn drop(&mut self) {
        std::fs::remove_file(&self.path).ok();
    }
}
fn next_id(conn: &Connection, table: &str) -> Result<i64> {
    let max: i64 = conn
        .query_row(
            &format!("SELECT COALESCE(MAX(ID), 0) FROM {table}"),
            [],
            |r| r.get(0),
        )
        .with_context(|| format!("cannot read MAX(ID) of {table}"))?;
    max.checked_add(1).context("ID space exhausted")
}
pub fn snapshot(app: &App, cache_dir: &Path) -> Result<PathBuf> {
    let dir = cache_dir.join("snapshots").join(format!(
        "{}-{}-{}",
        epoch_secs(),
        std::process::id(),
        app.id
    ));
    std::fs::create_dir_all(&dir).with_context(|| format!("cannot create {}", dir.display()))?;
    let dst = dir.join(&app.backup_name);
    retry(
        || {
            std::fs::copy(&app.backup_path, &dst).with_context(|| {
                format!(
                    "cannot snapshot {} -> {}",
                    app.backup_path.display(),
                    dst.display()
                )
            })
        },
        "snapshot",
    )?;
    Ok(dst)
}
fn check_staged(tmp: &Path) -> Result<()> {
    let conn = Connection::open_with_flags(tmp, OpenFlags::SQLITE_OPEN_READ_ONLY)
        .with_context(|| format!("cannot open staged {}", tmp.display()))?;
    let verdict: String = conn
        .query_row("PRAGMA quick_check", [], |r| r.get(0))
        .context("quick_check failed")?;
    if verdict == "ok" {
        Ok(())
    } else {
        anyhow::bail!("staged copy fails quick_check: {verdict}")
    }
}
pub fn modify(
    app: &App,
    cache_dir: &Path,
    data_dir: &Path,
    record: bool,
    f: impl FnOnce(&Transaction) -> Result<Vec<crate::oplog::OpKind>>,
) -> Result<PathBuf> {
    let _lock = LockGuard::acquire(cache_dir, &app.id)?;
    let fp_pre = crate::sync::fingerprint(app)?;
    crate::sync::check(data_dir, app, &fp_pre)?;
    let snap = snapshot(app, cache_dir)?;
    let tmp = std::env::temp_dir().join(format!(
        "rwcore-{}-{}-{}-{}.db",
        app.id,
        std::process::id(),
        epoch_secs(),
        TMP_COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
    ));
    let mut f_opt = Some(f);
    let mut attempt = 0u32;
    let ops: Vec<crate::oplog::OpKind> = loop {
        retry(
            || {
                std::fs::copy(&app.backup_path, &tmp)
                    .with_context(|| format!("cannot stage {}", app.backup_path.display()))
            },
            "stage copy",
        )?;
        let step_result = (|| -> Result<Vec<crate::oplog::OpKind>> {
            let mut conn = Connection::open(&tmp).context("cannot open staged copy")?;
            let tx = conn.transaction().context("cannot begin transaction")?;
            let step = f_opt.take().context("internal: closure already used")?;
            let ops = step(&tx)?;
            tx.commit().context("cannot commit transaction")?;
            conn.close()
                .map_err(|(_, e)| e)
                .context("cannot close staged copy")?;
            check_staged(&tmp).context("staged copy unhealthy")?;
            Ok(ops)
        })();
        match step_result {
            Ok(ops) => break ops,
            Err(e) if attempt == 0 => {
                eprintln!("warning: staged copy failed verification, re-staging: {e:#}");
                attempt += 1;
            }
            Err(e) => {
                std::fs::remove_file(&tmp).ok();
                return Err(e);
            }
        }
    };
    let wb: Result<()> = (|| {
        let mut src = std::fs::File::open(&tmp).context("cannot reopen staged copy")?;
        let mut live = OpenOptions::new()
            .write(true)
            .truncate(true)
            .open(&app.backup_path)
            .with_context(|| format!("cannot open live file {}", app.backup_path.display()))?;
        std::io::copy(&mut src, &mut live).context("cannot write live file")?;
        live.sync_all().context("fsync live file failed")?;
        Ok(())
    })();
    std::fs::remove_file(&tmp).ok();
    wb?;
    let fp_post = crate::sync::fingerprint(app)?;
    crate::sync::adopt(data_dir, app, &fp_post)?;
    if record {
        let ts = epoch_secs() as i64;
        for kind in ops {
            crate::oplog::append(data_dir, &app.id, ts, kind, &fp_pre, &fp_post)?;
        }
    }
    Ok(snap)
}
pub fn add_word(
    conn: &Connection,
    word: &str,
    transcription: Option<&str>,
    tr: &[(Lang, String)],
) -> Result<WordId> {
    let word_id = next_id(conn, "WORD")?;
    insert_word(conn, word_id, word, transcription, tr)?;
    Ok(WordId(word_id))
}
pub fn readd_word(
    conn: &Connection,
    id: WordId,
    word: &str,
    transcription: Option<&str>,
    tr: &[(String, String)],
) -> Result<()> {
    let langs = lang_cols(conn)?;
    let mut typed = Vec::with_capacity(tr.len());
    for (code, text) in tr {
        let lang = Lang::parse(code).with_context(|| format!("bad stored lang '{code}'"))?;
        if !langs.iter().any(|l| l == &lang) {
            anyhow::bail!("unknown translation column '{code}' for this app");
        }
        typed.push((lang, text.clone()));
    }
    let taken: i64 =
        conn.query_row("SELECT COUNT(*) FROM WORD WHERE ID=?", [id.0], |r| r.get(0))?;
    if taken > 0 {
        anyhow::bail!("word id {} already exists, refusing replay insert", id.0);
    }
    insert_word(conn, id.0, word, transcription, &typed)
}
fn insert_word(
    conn: &Connection,
    word_id: i64,
    word: &str,
    transcription: Option<&str>,
    tr: &[(Lang, String)],
) -> Result<()> {
    let custom_cat: String = conn
        .query_row(
            "SELECT ID FROM CATEGORY WHERE IS_CUSTOM = 1 ORDER BY ID LIMIT 1",
            [],
            |r| r.get(0),
        )
        .context("no custom category (create one in the app first)")?;
    let wc_id = next_id(conn, "WORD_CATEGORY")?;
    let langs = lang_cols(conn)?;
    let mut cols = vec!["ID", "WORD"];
    let mut placeholders = vec!["?", "?"];
    if transcription.is_some() {
        cols.push("TRANSCRIPTION");
        placeholders.push("?");
    }
    let mut values: Vec<rusqlite::types::Value> = vec![
        rusqlite::types::Value::Integer(word_id),
        rusqlite::types::Value::Text(word.to_string()),
    ];
    if let Some(t) = transcription {
        values.push(rusqlite::types::Value::Text(t.to_string()));
    }
    for (lang, text) in tr {
        if !langs.iter().any(|l| l == lang) {
            anyhow::bail!("unknown translation column '{}' for this app", lang.code());
        }
        cols.push(lang.code());
        placeholders.push("?");
        values.push(rusqlite::types::Value::Text(text.clone()));
    }
    let sql = format!(
        "INSERT INTO WORD ({}) VALUES ({})",
        cols.join(", "),
        placeholders.join(", ")
    );
    conn.execute(&sql, rusqlite::params_from_iter(values))
        .context("insert WORD")?;
    conn.execute(
        "INSERT INTO WORD_CATEGORY (ID, WORD_ID, CATEGORY_ID) VALUES (?, ?, ?)",
        rusqlite::params![wc_id, word_id, custom_cat],
    )
    .context("insert WORD_CATEGORY")?;
    Ok(())
}
pub fn grade_review(
    conn: &Connection,
    word: WordId,
    mode: CardMode,
    ok: bool,
    now_ts: i64,
    local_date: &str,
) -> Result<(f64, i64)> {
    let m = mode.value();
    if m != 1 && m != 2 {
        anyhow::bail!("unsupported mode {m}");
    }
    let sfx = if m == 1 { "REC" } else { "REP" };
    if ok {
        let (s, e, f): (i64, f64, i64) = conn
            .query_row(
                &format!("SELECT S_{sfx}, E_{sfx}, F_{sfx} FROM WORD WHERE ID = ?"),
                [word.0],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
            )
            .context("read mode state")?;
        let log_id = next_id(conn, "LOG")?;
        conn.execute(
            "INSERT INTO LOG (ID, TIMESTAMP, LOCAL_DATE, WORD_ID, MODE, QUEUE, STEP, NQUEUE, FLAGS)
             VALUES (?, ?, ?, ?, ?, 2, ?, 2, 0)",
            rusqlite::params![log_id, now_ts, local_date, word.0, m, s],
        )
        .context("insert LOG review row")?;
        let s_new = s.checked_add(1).context("S overflow")?;
        let (s_new, e_new) = (s_new, e + 0.25);
        let i_new = crate::interval::ladder_interval(s_new, e_new, mode);
        let other = if m == 1 { "REP" } else { "REC" };
        conn.execute(
            &format!(
                "UPDATE WORD SET S_{sfx}=?, E_{sfx}=?, F_{sfx}=0, T_{sfx}=?,
                 I_{sfx}=?, I_{other}=? WHERE ID=?"
            ),
            rusqlite::params![
                s_new,
                e_new,
                now_ts,
                i_new,
                crate::interval::CROSS_SIDE_INTERVAL_SECS,
                word.0
            ],
        )
        .context("advance WORD")?;
        Ok((e, f))
    } else {
        let (_s, e, f): (i64, f64, i64) = conn
            .query_row(
                &format!("SELECT S_{sfx}, E_{sfx}, F_{sfx} FROM WORD WHERE ID = ?"),
                [word.0],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
            )
            .context("read mode state")?;
        conn.execute(
            &format!("UPDATE WORD SET E_{sfx}=E_{sfx}-0.5, F_{sfx}=F_{sfx}+1 WHERE ID=?"),
            [word.0],
        )
        .context("record fail")?;
        Ok((e, f))
    }
}
pub fn triage_known(conn: &Connection, word: WordId, now_ts: i64, local_date: &str) -> Result<()> {
    conn.execute(
        "UPDATE WORD SET Q_REC=3, Q_REP=3, S_REC=0, S_REP=0,
        E_REC=2.5, E_REP=2.5, F_REC=0, F_REP=0, I_REC=NULL, I_REP=NULL,
        T_REC=?, T_REP=? WHERE ID=?",
        rusqlite::params![now_ts, now_ts, word.0],
    )
    .context("park WORD")?;
    let log_id = next_id(conn, "LOG")?;
    for (i, mode) in [1i64, 2].iter().enumerate() {
        let id = log_id.checked_add(i as i64).context("LOG ID overflow")?;
        conn.execute(
            "INSERT INTO LOG (ID, TIMESTAMP, LOCAL_DATE, WORD_ID, MODE, QUEUE, STEP, NQUEUE, FLAGS)
             VALUES (?, ?, ?, ?, ?, 0, 0, 3, 0)",
            rusqlite::params![id, now_ts, local_date, word.0, mode],
        )
        .context("insert LOG graduation rows")?;
    }
    Ok(())
}
pub fn enroll_word(conn: &Connection, word: WordId, now_ts: i64, local_date: &str) -> Result<()> {
    conn.execute(
        "UPDATE WORD SET Q_REC=2, Q_REP=2, S_REC=1, S_REP=1, T_REC=?, T_REP=? WHERE ID=?",
        rusqlite::params![now_ts, now_ts, word.0],
    )
    .context("enroll WORD")?;
    let log_id = next_id(conn, "LOG")?;
    conn.execute(
        "INSERT INTO LOG (ID, TIMESTAMP, LOCAL_DATE, WORD_ID, MODE, QUEUE, STEP, NQUEUE, FLAGS)
         VALUES (?, ?, ?, ?, 2, 1, 1, 2, 0)",
        rusqlite::params![log_id, now_ts, local_date, word.0],
    )
    .context("enroll LOG active")?;
    conn.execute(
        "INSERT INTO LOG (ID, TIMESTAMP, LOCAL_DATE, WORD_ID, MODE, QUEUE, STEP, NQUEUE, FLAGS)
         VALUES (?, ?, ?, ?, 1, 1, 1, 2, 2)",
        rusqlite::params![
            log_id.checked_add(1).context("LOG ID overflow")?,
            now_ts,
            local_date,
            word.0
        ],
    )
    .context("enroll LOG passive")?;
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    use crate::discover;
    use std::os::unix::fs::MetadataExt;
    const SCHEMA: &str = "
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
        INSERT INTO SETTINGS VALUES ('native_language', 'RUS'), ('daily_goal', '30'), ('ui_language', 'en');
        INSERT INTO CATEGORY VALUES ('custom', 1, 1, NULL, NULL, 'My words');
        INSERT INTO CATEGORY VALUES ('food', 0, 0, NULL, NULL, 'Food');
        INSERT INTO WORD (ID, WORD, ENG, RUS) VALUES (1, 'el pan', 'bread', 'хлеб');
        INSERT INTO WORD (ID, WORD, ENG, RUS) VALUES (2, 'la leche', 'milk', 'молоко');
        INSERT INTO WORD_CATEGORY VALUES (1, 1, 'custom');
        INSERT INTO WORD_CATEGORY VALUES (2, 2, 'food');
    ";
    fn fixture_root(suffix: &str, file: &str) -> (tempfile::TempDir, PathBuf) {
        let tmp = tempfile::tempdir().unwrap();
        let docs = tmp
            .path()
            .join("icloud")
            .join(format!("iCloud~ru~poas~{suffix}"))
            .join("Documents");
        std::fs::create_dir_all(&docs).unwrap();
        let db = docs.join(file);
        let conn = Connection::open(&db).unwrap();
        conn.execute_batch(SCHEMA).unwrap();
        conn.close().unwrap();
        (tmp, db)
    }
    fn fixture_db() -> (tempfile::TempDir, PathBuf) {
        fixture_root("englishwords~esen", "reword_es.backup")
    }
    #[test]
    fn discover_maps_ids() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path();
        for (container, file, schema) in [
            ("iCloud~ru~poas~englishwords", "reword_en.backup", true),
            ("iCloud~ru~poas~englishwords~esen", "reword_es.backup", true),
            ("iCloud~ru~poas~frenchwords", "reword_fr.backup", true),
            ("iCloud~com~example~words", "reword_xx.backup", true),
            ("iCloud~ru~poas~notreword", "notes.backup", false),
            ("iCloud~ru~poas~empty", "empty.backup", false),
        ] {
            let docs = root.join(container).join("Documents");
            std::fs::create_dir_all(&docs).unwrap();
            let p = docs.join(file);
            if schema {
                let conn = rusqlite::Connection::open(&p).unwrap();
                conn.execute_batch(
                    "CREATE TABLE WORD (ID INTEGER PRIMARY KEY);
                     CREATE TABLE CATEGORY (ID TEXT PRIMARY KEY);
                     CREATE TABLE LOG (ID INTEGER PRIMARY KEY);
                     CREATE TABLE SETTINGS (NAME TEXT PRIMARY KEY);",
                )
                .unwrap();
                conn.close().unwrap();
            } else {
                std::fs::write(&p, b"not a database").unwrap();
            }
        }
        let apps = discover::discover(root).unwrap();
        let ids: Vec<_> = apps.iter().map(|a| a.id.as_str()).collect();
        assert_eq!(ids, vec!["en", "es", "fr", "xx"]);
        assert_eq!(discover::resolve(root, "1").unwrap().id, "en");
        assert_eq!(discover::resolve(root, "4").unwrap().id, "xx");
        assert_eq!(discover::resolve(root, "es").unwrap().id, "es");
        assert!(discover::resolve(root, "9").is_err());
        assert!(discover::resolve(root, "de").is_err());
    }
    #[test]
    fn reads_fixture() {
        let (_tmp, db) = fixture_db();
        let conn = open_ro(&db).unwrap();
        let s = stats(&conn).unwrap();
        assert_eq!((s.words, s.categories), (2, 2));
        assert_eq!(s.settings.native_language.as_deref(), Some("RUS"));
        let all = list_words(
            &conn,
            &WordFilter {
                search: None,
                category: None,
                limit: 50,
            },
        )
        .unwrap();
        assert_eq!(all.len(), 2);
        assert_eq!(
            all[0].translations.get(&Lang::Rus).map(String::as_str),
            Some("хлеб")
        );
        assert_eq!(all[0].recognition.level, 0);
        let milk = list_words(
            &conn,
            &WordFilter {
                search: Some("leche"),
                category: None,
                limit: 50,
            },
        )
        .unwrap();
        assert_eq!(milk.len(), 1);
        let custom = list_words(
            &conn,
            &WordFilter {
                search: None,
                category: Some("custom"),
                limit: 50,
            },
        )
        .unwrap();
        assert_eq!(custom.len(), 1);
        let by_id = get_word(&conn, "2").unwrap().unwrap();
        assert_eq!(by_id.text, "la leche");
        let cats = categories(&conn).unwrap();
        assert!(
            cats.iter()
                .any(|c| c.id == "custom" && c.custom && c.words == 1)
        );
    }
    #[test]
    fn modify_adds_word_preserving_inode() {
        let (tmp, db) = fixture_db();
        let ino_before = std::fs::metadata(&db).unwrap().ino();
        let app = discover::App {
            id: "es".to_string(),
            container: "iCloud~ru~poas~englishwords~esen".to_string(),
            backup_path: db.clone(),
            backup_name: "reword_es.backup".to_string(),
        };
        let cache = tmp.path().join("cache");
        let data = tmp.path().join("data");
        let snap = modify(&app, &cache, &data, true, |tx| {
            let id = add_word(tx, "zztest", None, &[(Lang::Eng, "test".to_string())])?;
            assert_eq!(id, WordId(3));
            Ok(vec![crate::oplog::OpKind::Added {
                id: 3,
                text: "zztest".to_string(),
                transcription: None,
                tr: vec![("ENG".to_string(), "test".to_string())],
            }])
        })
        .unwrap();
        assert!(snap.exists(), "snapshot must exist");
        assert_eq!(std::fs::metadata(&db).unwrap().ino(), ino_before);
        let logged = crate::oplog::tail(&data, 10);
        assert_eq!(logged.len(), 1);
        assert!(matches!(
            logged[0].kind,
            crate::oplog::OpKind::Added { id: 3, .. }
        ));
        let conn = open_ro(&db).unwrap();
        let w = get_word(&conn, "zztest").unwrap().unwrap();
        assert_eq!(
            w.translations.get(&Lang::Eng).map(String::as_str),
            Some("test")
        );
        let custom = list_words(
            &conn,
            &WordFilter {
                search: None,
                category: Some("custom"),
                limit: 50,
            },
        )
        .unwrap();
        assert_eq!(custom.len(), 2);
    }
    #[test]
    fn add_word_rejects_unknown_lang() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        let err = add_word(
            &conn,
            "x",
            None,
            &[(Lang::Other("SPA".to_string()), "y".to_string())],
        );
        assert!(err.is_err());
    }
    #[test]
    fn enroll_matches_app_fingerprint() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        let id = add_word(&conn, "zz", None, &[(Lang::Eng, "t".to_string())]).unwrap();
        enroll_word(&conn, id, 1_000_000, "2026-09-12").unwrap();
        let w = get_word(&conn, "zz").unwrap().unwrap();
        assert_eq!((w.recognition.level, w.recognition.step), (2, 1));
        assert_eq!((w.reproduction.level, w.reproduction.step), (2, 1));
        assert_eq!(w.recognition.last_review_ts, Some(1_000_000));
        let entries = log_entries(&conn, id, 100).unwrap();
        let shape: Vec<(i64, i64, i64, i64, i64)> = entries
            .iter()
            .map(|e| {
                (
                    e.mode.value(),
                    e.queue.value(),
                    e.step,
                    e.next_queue.value(),
                    e.kind.value(),
                )
            })
            .collect();
        assert_eq!(shape, vec![(2, 1, 1, 2, 0), (1, 1, 1, 2, 2)]);
    }
    #[test]
    fn due_pool_matches_badge_rule() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute_batch(
            "UPDATE WORD SET T_REC=1000, I_REC=100, T_REP=9000, I_REP=100 WHERE ID=1;
             UPDATE WORD SET T_REC=1000, I_REC=100 WHERE ID=2;
             INSERT INTO WORD (ID, WORD) VALUES (3, 'parked');
             INSERT INTO WORD_CATEGORY VALUES (10, 3, 'custom');
             INSERT INTO WORD (ID, WORD, T_REC, I_REC, T_REP, I_REP) VALUES (5, 'both', 1000, 100, 1000, 100);
             INSERT INTO WORD_CATEGORY VALUES (11, 5, 'custom');
             INSERT INTO WORD (ID, WORD, Q_REC, Q_REP, T_REC, T_REP) VALUES (6, 'known', 3, 3, 1000, 1000);
             INSERT INTO WORD_CATEGORY VALUES (12, 6, 'custom');",
        )
        .unwrap();
        let due = due_words(&conn, 2000).unwrap();
        let ids: Vec<(i64, usize)> = due.iter().map(|(w, m)| (w.id.0, m.len())).collect();
        assert_eq!(ids, vec![(1, 1), (5, 2)]);
        assert_eq!(due[0].1, vec![CardMode::Recognition]);
    }
    fn log_count(conn: &Connection) -> i64 {
        conn.query_row("SELECT COUNT(*) FROM LOG", [], |r| r.get(0))
            .unwrap()
    }
    #[test]
    fn review_ok_tested_matches_pescado() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute("UPDATE WORD SET S_REP=1 WHERE ID=1", [])
            .unwrap();
        let before = log_count(&conn);
        grade_review(
            &conn,
            WordId(1),
            CardMode::Reproduction,
            true,
            5000,
            "2026-09-13",
        )
        .unwrap();
        assert_eq!(log_count(&conn), before + 1);
        let row: (i64, i64, i64, i64, i64, i64) = conn
            .query_row(
                "SELECT MODE, QUEUE, STEP, NQUEUE, FLAGS, TIMESTAMP FROM LOG ORDER BY ID DESC LIMIT 1",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?, r.get(5)?)),
            )
            .unwrap();
        assert_eq!(row, (2, 2, 1, 2, 0, 5000));
        let st: (i64, f64, i64, i64, i64, Option<i64>) = conn
            .query_row(
                "SELECT S_REP, E_REP, F_REP, T_REP, I_REP, I_REC FROM WORD WHERE ID=1",
                [],
                |r| {
                    Ok((
                        r.get(0)?,
                        r.get(1)?,
                        r.get(2)?,
                        r.get(3)?,
                        r.get(4)?,
                        r.get(5)?,
                    ))
                },
            )
            .unwrap();
        assert_eq!(st.0, 2);
        assert!((st.1 - 2.75).abs() < 1e-9);
        assert_eq!((st.2, st.3), (0, 5000));
        assert_eq!(st.4, 10800);
        assert_eq!(st.5, Some(crate::interval::CROSS_SIDE_INTERVAL_SECS));
    }
    #[test]
    fn review_ok_self_matches_alce() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute("UPDATE WORD SET S_REC=1 WHERE ID=1", [])
            .unwrap();
        grade_review(
            &conn,
            WordId(1),
            CardMode::Recognition,
            true,
            6000,
            "2026-09-13",
        )
        .unwrap();
        let mode: i64 = conn
            .query_row("SELECT MODE FROM LOG ORDER BY ID DESC LIMIT 1", [], |r| {
                r.get(0)
            })
            .unwrap();
        assert_eq!(mode, 1);
        let (s, e, i): (i64, f64, i64) = conn
            .query_row("SELECT S_REC, E_REC, I_REC FROM WORD WHERE ID=1", [], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?))
            })
            .unwrap();
        assert_eq!(s, 2);
        assert!((e - 2.75).abs() < 1e-9);
        assert_eq!(i, 10800);
    }
    #[test]
    fn review_fail_is_silent() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute(
            "UPDATE WORD SET S_REP=1, E_REP=2.5, T_REP=100 WHERE ID=1",
            [],
        )
        .unwrap();
        let before = log_count(&conn);
        grade_review(
            &conn,
            WordId(1),
            CardMode::Reproduction,
            false,
            7000,
            "2026-09-13",
        )
        .unwrap();
        assert_eq!(log_count(&conn), before);
        let st: (i64, f64, i64, Option<i64>, Option<i64>) = conn
            .query_row(
                "SELECT S_REP, E_REP, F_REP, T_REP, I_REP FROM WORD WHERE ID=1",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?)),
            )
            .unwrap();
        assert_eq!(st.0, 1);
        assert!((st.1 - 2.0).abs() < 1e-9);
        assert_eq!(st.2, 1);
        assert_eq!(st.3, Some(100));
        assert_eq!(st.4, None);
    }
    #[test]
    fn triage_known_parks() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        triage_known(&conn, WordId(1), 8000, "2026-09-13").unwrap();
        let st: (i64, i64, i64, f64, i64, i64) = conn
            .query_row(
                "SELECT Q_REC, Q_REP, S_REC, E_REC, T_REC, T_REP FROM WORD WHERE ID=1",
                [],
                |r| {
                    Ok((
                        r.get(0)?,
                        r.get(1)?,
                        r.get(2)?,
                        r.get(3)?,
                        r.get(4)?,
                        r.get(5)?,
                    ))
                },
            )
            .unwrap();
        assert_eq!(st, (3, 3, 0, 2.5, 8000, 8000));
        let mut stmt = conn
            .prepare(
                "SELECT MODE, QUEUE, STEP, NQUEUE, FLAGS FROM LOG WHERE WORD_ID=1 ORDER BY MODE",
            )
            .unwrap();
        let rows: Vec<(i64, i64, i64, i64, i64)> = stmt
            .query_map([], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?))
            })
            .unwrap()
            .collect::<std::result::Result<Vec<_>, _>>()
            .unwrap();
        assert_eq!(rows, vec![(1, 0, 0, 3, 0), (2, 0, 0, 3, 0)]);
    }
    #[test]
    fn triage_known_resets_progress() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute(
            "UPDATE WORD SET S_REC=5, S_REP=4, E_REC=3.5, E_REP=3.0, F_REC=2,
             I_REC=999, I_REP=888, T_REC=111, T_REP=222 WHERE ID=1",
            [],
        )
        .unwrap();
        triage_known(&conn, WordId(1), 8000, "2026-09-13").unwrap();
        let st: (i64, f64, i64, Option<i64>, i64) = conn
            .query_row(
                "SELECT S_REC, E_REC, F_REC, I_REC, T_REC FROM WORD WHERE ID=1",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?)),
            )
            .unwrap();
        assert_eq!(st, (0, 2.5, 0, None, 8000));
    }
    #[test]
    fn grade_rejects_unknown_mode() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        assert!(
            grade_review(
                &conn,
                WordId(1),
                CardMode::Unknown(9),
                true,
                1,
                "2026-09-13"
            )
            .is_err()
        );
    }
}
