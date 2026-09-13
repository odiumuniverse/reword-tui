use crate::discover::App;
use crate::model::{
    AttemptKind, CardMode, Category, Lang, LogEntry, ModeState, Queue, Settings, Stats, Word,
    WordId,
};
use anyhow::{Context, Result};
use rusqlite::{Connection, OpenFlags, OptionalExtension, Row, Transaction};
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
    retry(
        || {
            Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_ONLY)
                .with_context(|| format!("cannot open {} read-only", path.display()))
        },
        "open read-only",
    )
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
        if let Some(v) = v {
            map.insert(k, v);
        }
    }
    Ok(Settings {
        native_language: map.remove("native_language"),
        daily_goal: map.remove("daily_goal"),
        ui_language: map.remove("ui_language"),
    })
}
/// Column holding category titles. Apps store them per interface language
/// (NAME_ENG, NAME_RUS, ...) and the target language never gets its own
/// column, so take English, then the native language, then any.
pub(crate) fn cat_name_col(conn: &Connection) -> Result<String> {
    let mut st = conn.prepare("PRAGMA table_info(CATEGORY)")?;
    let names: Vec<String> = st
        .query_map([], |r| r.get::<_, String>(1))?
        .collect::<rusqlite::Result<Vec<_>>>()
        .context("decode PRAGMA row")?
        .into_iter()
        .filter(|c| {
            c.len() == 8
                && c.starts_with("NAME_")
                && c[5..].chars().all(|ch| ch.is_ascii_uppercase())
        })
        .collect();
    if names.iter().any(|c| c == "NAME_ENG") {
        return Ok("NAME_ENG".to_string());
    }
    if let Some(native) = settings(conn).ok().and_then(|s| s.native_language) {
        let col = format!("NAME_{native}");
        if names.contains(&col) {
            return Ok(col);
        }
    }
    names
        .into_iter()
        .next()
        .context("CATEGORY has no NAME_* title column")
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
fn col_of(idx: &HashMap<String, usize>, name: &str) -> rusqlite::Result<usize> {
    idx.get(name)
        .copied()
        .ok_or_else(|| rusqlite::Error::InvalidColumnName(name.to_string()))
}
fn mode_state(
    row: &Row,
    idx: &HashMap<String, usize>,
    suffix: &str,
) -> rusqlite::Result<ModeState> {
    let col = |base: &str| col_of(idx, &format!("{base}_{suffix}"));
    Ok(ModeState {
        level: row.get(col("Q")?)?,
        step: row.get(col("S")?)?,
        easiness: row.get(col("E")?)?,
        fails: row.get(col("F")?)?,
        last_review_ts: row.get(col("T")?)?,
        interval_secs: row.get(col("I")?)?,
    })
}
fn word_from_row(
    row: &Row,
    langs: &[Lang],
    idx: &HashMap<String, usize>,
) -> rusqlite::Result<Word> {
    let id: i64 = row.get(col_of(idx, "ID")?)?;
    let text: String = row.get(col_of(idx, "WORD")?)?;
    let transcription: Option<String> = row.get(col_of(idx, "TRANSCRIPTION")?)?;
    let pos: Option<i64> = idx.get("POS").and_then(|i| row.get(*i).ok()).flatten();
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
        pos,
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
          AND ((WORD.Q_REC = 2 AND WORD.T_REC IS NOT NULL AND WORD.I_REC IS NOT NULL
                AND WORD.T_REC + WORD.I_REC <= ? + 30)
           OR (WORD.Q_REP = 2 AND WORD.T_REP IS NOT NULL AND WORD.I_REP IS NOT NULL
                AND WORD.T_REP + WORD.I_REP <= ? + 30))
         GROUP BY WORD.ID ORDER BY WORD.ID",
    )?;
    let rows = st.query_map(rusqlite::params![now, now], |r| {
        word_from_row(r, &langs, &idx)
    })?;
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
        conds.push("WORD.WORD LIKE '%' || REPLACE(REPLACE(REPLACE(?, '\\', '\\\\'), '%', '\\%'), '_', '\\_') || '%' ESCAPE '\\'".to_string());
    }
    if f.category.is_some() {
        conds.push(format!("(c.ID = ? OR c.{} = ?)", cat_name_col(conn)?));
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
    if query.parse::<i64>().is_ok() {
        let mut st = conn.prepare("SELECT * FROM WORD WHERE ID = ? ORDER BY ID LIMIT 1")?;
        let mut rows = st.query_map([query], |r| word_from_row(r, &langs, &idx))?;
        if let Some(w) = rows.next().transpose()? {
            return Ok(Some(w));
        }
    }
    let mut st = conn.prepare("SELECT * FROM WORD WHERE WORD = ? ORDER BY ID LIMIT 1")?;
    let mut rows = st.query_map([query], |r| word_from_row(r, &langs, &idx))?;
    Ok(rows.next().transpose()?)
}
pub fn categories(conn: &Connection) -> Result<Vec<Category>> {
    let name = cat_name_col(conn)?;
    let mut st = conn.prepare(&format!(
        "SELECT c.ID, c.IS_CUSTOM, c.IS_SELECTED, c.{name}, COUNT(DISTINCT wc.WORD_ID)
         FROM CATEGORY c LEFT JOIN WORD_CATEGORY wc ON wc.CATEGORY_ID = c.ID
         GROUP BY c.ID ORDER BY c.IS_CUSTOM DESC, c.ID"
    ))?;
    let rows = st.query_map([], |r| {
        Ok(Category {
            id: r.get(0)?,
            custom: r.get::<_, i64>(1)? != 0,
            selected: r.get::<_, i64>(2)? != 0,
            name_en: r.get(3)?,
            words: r.get(4)?,
        })
    })?;
    rows.map(|r| r.context("decode CATEGORY row"))
        .collect::<Result<Vec<_>>>()
}
pub fn category_stats(conn: &Connection) -> Result<Vec<crate::model::CategoryStat>> {
    let mut st = conn.prepare(
        "SELECT wc.CATEGORY_ID, COUNT(DISTINCT w.ID),
                COUNT(DISTINCT CASE WHEN MAX(w.Q_REC, w.Q_REP) > 0 THEN w.ID END)
         FROM WORD w JOIN WORD_CATEGORY wc ON wc.WORD_ID = w.ID
         GROUP BY wc.CATEGORY_ID ORDER BY wc.CATEGORY_ID",
    )?;
    let rows = st.query_map([], |r| {
        Ok(crate::model::CategoryStat {
            category: r.get(0)?,
            total: r.get(1)?,
            started: r.get(2)?,
        })
    })?;
    rows.map(|r| r.context("decode category stat row"))
        .collect::<Result<Vec<_>>>()
}
pub fn set_selected(conn: &Connection, category: &str, selected: bool) -> Result<String> {
    let id: Option<String> = conn
        .query_row(
            &format!(
                "SELECT c.ID FROM CATEGORY c WHERE c.ID = ? OR c.{} = ? ORDER BY c.ID LIMIT 1",
                cat_name_col(conn)?
            ),
            rusqlite::params![category, category],
            |r| r.get(0),
        )
        .optional()
        .context("lookup category")?;
    let id = id.with_context(|| format!("no category '{category}'"))?;
    conn.execute(
        "UPDATE CATEGORY SET IS_SELECTED = ? WHERE ID = ?",
        rusqlite::params![if selected { 1 } else { 0 }, id],
    )
    .context("update IS_SELECTED")?;
    Ok(id)
}
pub fn today(conn: &Connection, local_today: &str) -> Result<crate::model::TodayStats> {
    let (learned, reviewed): (i64, i64) = conn
        .query_row(
            "SELECT COUNT(DISTINCT CASE WHEN QUEUE = 1 THEN WORD_ID END),
                    COUNT(DISTINCT CASE WHEN QUEUE = 2 THEN WORD_ID END)
             FROM LOG WHERE LOCAL_DATE = ? AND QUEUE IN (1, 2)",
            [local_today],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .context("count learned/reviewed")?;
    let learning_now: i64 = conn
        .query_row("SELECT COUNT(*) FROM WORD WHERE Q_REC = 1", [], |r| {
            r.get(0)
        })
        .context("count learning")?;
    let mastered: i64 = conn
        .query_row(
            "SELECT COUNT(*) FROM WORD WHERE MAX(S_REC, S_REP) >= 7",
            [],
            |r| r.get(0),
        )
        .context("count mastered")?;
    let known: i64 = conn
        .query_row(
            "SELECT COUNT(*) FROM WORD WHERE Q_REC = 3 AND Q_REP = 3",
            [],
            |r| r.get(0),
        )
        .context("count known")?;
    let mut st = conn.prepare(
        "SELECT DISTINCT LOCAL_DATE FROM LOG WHERE QUEUE = 1 ORDER BY LOCAL_DATE DESC LIMIT 400",
    )?;
    let mut dates: Vec<String> = st
        .query_map([], |r| r.get(0))?
        .map(|r| r.context("decode date"))
        .collect::<Result<_>>()?;
    dates.sort();
    let mut best = 0i64;
    let mut run = 0i64;
    let mut prev: Option<&str> = None;
    for d in &dates {
        let consecutive = match prev {
            None => false,
            Some(p) => next_day(p).as_deref() == Some(d.as_str()),
        };
        if prev.is_none() || consecutive {
            run += 1;
        } else {
            run = 1;
        }
        if run > best {
            best = run;
        }
        prev = Some(d);
    }
    let mut cur = 0i64;
    if dates.iter().any(|d| d == local_today) {
        cur = run_ending(&dates, local_today);
    } else if let Some(y) = prev_day(local_today)
        && dates.iter().any(|d| d == &y)
    {
        cur = run_ending(&dates, &y);
    }
    let goal_key = local_today.replace('-', "");
    let goal: Option<i64> = conn
        .query_row(
            "SELECT GOAL FROM DAILY_GOAL WHERE DATE = ?",
            [&goal_key],
            |r| r.get(0),
        )
        .optional()
        .unwrap_or(None)
        .flatten()
        .or_else(|| {
            conn.query_row(
                "SELECT VALUE FROM SETTINGS WHERE NAME = 'daily_goal'",
                [],
                |r| r.get::<_, Option<String>>(0),
            )
            .ok()
            .flatten()
            .and_then(|v| v.parse().ok())
        });
    Ok(crate::model::TodayStats {
        today: local_today.to_string(),
        learned,
        reviewed,
        memorizing: learning_now,
        mastered,
        known,
        goal,
        streak_cur: cur,
        streak_best: best,
        active_dates: dates,
    })
}
fn run_ending(dates: &[String], anchor: &str) -> i64 {
    let mut cur = 1i64;
    let mut d = anchor.to_string();
    while let Some(p) = prev_day(&d) {
        if dates.iter().any(|x| x == &p) {
            cur += 1;
            d = p;
        } else {
            break;
        }
    }
    cur
}
fn split_day(d: &str) -> Option<(i32, u32, u32)> {
    let mut it = d.split('-');
    let y = it.next()?.parse().ok()?;
    let m = it.next()?.parse().ok()?;
    let day = it.next()?.parse().ok()?;
    if it.next().is_some() {
        return None;
    }
    Some((y, m, day))
}
fn days_from_civil(y: i32, m: u32, d: u32) -> i64 {
    let y = if m <= 2 { y as i64 - 1 } else { y as i64 };
    let era = y.div_euclid(400);
    let yoe = y.rem_euclid(400) as u64;
    let mp = ((m as i64 + 9) % 12) as u64;
    let doy = (153 * mp + 2) / 5 + d as u64 - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146097 + doe as i64 - 719468
}
fn civil_from_days(z: i64) -> (i32, u32, u32) {
    let z = z + 719468;
    let era = z.div_euclid(146097);
    let doe = z.rem_euclid(146097) as u64;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    let y = if m <= 2 { y + 1 } else { y } as i32;
    (y, m, d)
}
fn shift_day(d: &str, delta: i64) -> Option<String> {
    let (y, m, day) = split_day(d)?;
    let (y2, m2, d2) = civil_from_days(days_from_civil(y, m, day) + delta);
    Some(format!("{y2:04}-{m2:02}-{d2:02}"))
}
fn next_day(d: &str) -> Option<String> {
    shift_day(d, 1)
}
fn prev_day(d: &str) -> Option<String> {
    shift_day(d, -1)
}
pub fn add_category(conn: &Connection, id: &str, name: &str) -> Result<()> {
    let id = id.trim();
    let name = name.trim();
    if id.is_empty() || name.is_empty() {
        anyhow::bail!("category id and title are required");
    }
    let col = cat_name_col(conn)?;
    let taken: i64 = conn
        .query_row(
            &format!("SELECT COUNT(*) FROM CATEGORY WHERE ID = ? OR {col} = ?"),
            rusqlite::params![id, name],
            |r| r.get(0),
        )
        .context("lookup category")?;
    if taken > 0 {
        anyhow::bail!("Category with this title already exists");
    }
    conn.execute(
        &format!("INSERT INTO CATEGORY (ID, IS_CUSTOM, IS_SELECTED, {col}) VALUES (?, 1, 0, ?)"),
        rusqlite::params![id, name],
    )
    .context("insert CATEGORY")?;
    Ok(())
}
pub fn import_rows(
    conn: &Connection,
    category: &str,
    rows: &[CsvRow],
    lang: &Lang,
) -> Result<(usize, usize)> {
    let cid: Option<String> = conn
        .query_row(
            &format!(
                "SELECT c.ID FROM CATEGORY c WHERE c.ID = ? OR c.{} = ? ORDER BY c.ID LIMIT 1",
                cat_name_col(conn)?
            ),
            rusqlite::params![category, category],
            |r| r.get(0),
        )
        .optional()
        .context("lookup category")?;
    let cid = cid.with_context(|| format!("no category '{category}'"))?;
    let mut existing: std::collections::HashSet<String> = conn
        .prepare("SELECT WORD FROM WORD")?
        .query_map([], |r| r.get::<_, Option<String>>(0))?
        .filter_map(|r| r.transpose())
        .collect::<rusqlite::Result<_>>()
        .context("dup check")?;
    let mut added = 0usize;
    let mut skipped = 0usize;
    for row in rows {
        let word = row.word.trim();
        let tr = row.tr.trim();
        if word.is_empty() || tr.is_empty() || existing.contains(word) {
            skipped += 1;
            continue;
        }
        let word_id = next_id(conn, "WORD")?;
        let ex: Vec<(Lang, String)> = if row.examples.is_empty() {
            Vec::new()
        } else {
            let items: Vec<serde_json::Value> = row
                .examples
                .iter()
                .map(|(o, t)| serde_json::json!({"o": o, "t": t}))
                .collect();
            vec![(
                lang.clone(),
                serde_json::to_string(&items).context("encode examples")?,
            )]
        };
        insert_word_row(
            conn,
            word_id,
            word,
            row.transcription.as_deref(),
            &[(lang.clone(), tr.to_string())],
            &ex,
        )?;
        let wc_id = next_id(conn, "WORD_CATEGORY")?;
        conn.execute(
            "INSERT INTO WORD_CATEGORY (ID, WORD_ID, CATEGORY_ID) VALUES (?, ?, ?)",
            rusqlite::params![wc_id, word_id, cid],
        )
        .context("link WORD_CATEGORY")?;
        existing.insert(word.to_string());
        added += 1;
    }
    Ok((added, skipped))
}
pub struct CsvRow {
    pub word: String,
    pub transcription: Option<String>,
    pub tr: String,
    pub examples: Vec<(String, String)>,
}
pub fn parse_csv(text: &str) -> Vec<CsvRow> {
    let mut out = Vec::new();
    for line in text.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        let mut parts: Vec<String> = Vec::new();
        let mut cur = String::new();
        let mut in_q = false;
        for ch in line.chars() {
            if ch == '"' {
                in_q = !in_q;
            } else if ch == ';' && !in_q {
                parts.push(cur.trim().to_string());
                cur.clear();
            } else {
                cur.push(ch);
            }
        }
        parts.push(cur.trim().to_string());
        if parts.len() < 2 {
            continue;
        }
        let word = parts[0].clone();
        let (transcription, tr, rest) = if parts.len() == 2 {
            (None, parts[1].clone(), &[][..])
        } else {
            (
                Some(parts[1].clone()).filter(|s| !s.is_empty()),
                parts[2].clone(),
                &parts[3..],
            )
        };
        let mut examples = Vec::new();
        let mut it = rest.iter();
        while let Some(o) = it.next() {
            let t = it.next().cloned().unwrap_or_default();
            if !o.is_empty() {
                examples.push((o.clone(), t));
            }
        }
        out.push(CsvRow {
            word,
            transcription,
            tr,
            examples,
        });
    }
    out
}
pub type WordPayload = (
    String,
    Option<String>,
    Vec<(String, String)>,
    Vec<(String, String)>,
);
pub fn word_payload(conn: &Connection, id: WordId) -> Result<WordPayload> {
    let w = get_word(conn, &id.0.to_string())?.with_context(|| format!("no word id {id}"))?;
    let tr = w
        .translations
        .iter()
        .map(|(l, t)| (l.code().to_string(), t.clone()))
        .collect();
    let ex = w
        .examples
        .iter()
        .map(|(l, t)| (l.code().to_string(), t.clone()))
        .collect();
    Ok((w.text, w.transcription, tr, ex))
}
pub fn remove_word(conn: &Connection, word: WordId) -> Result<String> {
    let text: String = conn
        .query_row("SELECT WORD FROM WORD WHERE ID = ?", [word.0], |r| r.get(0))
        .with_context(|| format!("no word id {}", word.0))?;
    conn.execute("DELETE FROM LOG WHERE WORD_ID = ?", [word.0])
        .context("delete LOG")?;
    conn.execute("DELETE FROM WORD_CATEGORY WHERE WORD_ID = ?", [word.0])
        .context("delete links")?;
    conn.execute("DELETE FROM WORD WHERE ID = ?", [word.0])
        .context("delete WORD")?;
    Ok(text)
}
pub fn reset_word(conn: &Connection, word: WordId) -> Result<()> {
    let n = conn
        .execute(
            "UPDATE WORD SET Q_REC=0, Q_REP=0, S_REC=0, S_REP=0,
             E_REC=2.5, E_REP=2.5, F_REC=0, F_REP=0, I_REC=NULL, I_REP=NULL,
             T_REC=NULL, T_REP=NULL WHERE ID=?",
            [word.0],
        )
        .context("reset WORD")?;
    if n == 0 {
        anyhow::bail!("no word id {}", word.0);
    }
    Ok(())
}
pub fn postpone_word(conn: &Connection, word: WordId, now_ts: i64) -> Result<()> {
    let n = conn
        .execute(
            "UPDATE WORD SET T_REC=?, T_REP=?, I_REC=86400, I_REP=86400 WHERE ID=?",
            rusqlite::params![now_ts, now_ts, word.0],
        )
        .context("postpone WORD")?;
    if n == 0 {
        anyhow::bail!("no word id {}", word.0);
    }
    Ok(())
}
pub fn category_words(conn: &Connection, category: &str) -> Result<(String, Vec<WordId>)> {
    let cid: Option<String> = conn
        .query_row(
            &format!(
                "SELECT c.ID FROM CATEGORY c WHERE c.ID = ? OR c.{} = ? ORDER BY c.ID LIMIT 1",
                cat_name_col(conn)?
            ),
            rusqlite::params![category, category],
            |r| r.get(0),
        )
        .optional()
        .context("lookup category")?;
    let cid = cid.with_context(|| format!("no category '{category}'"))?;
    let mut st = conn.prepare("SELECT WORD_ID FROM WORD_CATEGORY WHERE CATEGORY_ID = ?")?;
    let ids: Vec<WordId> = st
        .query_map([cid.clone()], |r| r.get::<_, i64>(0))?
        .map(|r| r.context("decode id").map(WordId))
        .collect::<Result<_>>()?;
    Ok((cid, ids))
}
pub fn clear_category(conn: &Connection, category: &str) -> Result<usize> {
    let custom: Option<i64> = conn
        .query_row(
            &format!(
                "SELECT IS_CUSTOM FROM CATEGORY WHERE ID = ? OR {} = ?",
                cat_name_col(conn)?
            ),
            rusqlite::params![category, category],
            |r| r.get(0),
        )
        .optional()
        .context("lookup category")?;
    match custom {
        None => anyhow::bail!("no category '{category}'"),
        Some(0) => anyhow::bail!("only custom categories can be cleared"),
        _ => {}
    }
    let (_, ids) = category_words(conn, category)?;
    let n = ids.len();
    for id in ids {
        remove_word(conn, id)?;
    }
    Ok(n)
}
pub fn remove_category(conn: &Connection, category: &str) -> Result<usize> {
    let (cid, _) = category_words(conn, category)?;
    let custom: i64 = conn
        .query_row("SELECT IS_CUSTOM FROM CATEGORY WHERE ID = ?", [&cid], |r| {
            r.get(0)
        })
        .context("lookup category")?;
    if custom == 0 {
        anyhow::bail!("only custom categories can be removed");
    }
    let n = clear_category(conn, &cid)?;
    conn.execute("DELETE FROM CATEGORY WHERE ID = ?", [&cid])
        .context("delete CATEGORY")?;
    Ok(n)
}
pub fn reset_category(conn: &Connection, category: &str) -> Result<usize> {
    let (_, ids) = category_words(conn, category)?;
    let n = ids.len();
    for id in ids {
        reset_word(conn, id)?;
    }
    Ok(n)
}
pub fn get_goal(conn: &Connection, today: &str) -> Result<Option<i64>> {
    let g: Option<i64> = conn
        .query_row("SELECT GOAL FROM DAILY_GOAL WHERE DATE = ?", [today], |r| {
            r.get(0)
        })
        .optional()
        .unwrap_or(None)
        .flatten();
    Ok(g)
}
pub fn set_goal(conn: &Connection, today: &str, goal: i64) -> Result<()> {
    if goal < 0 {
        anyhow::bail!("goal must be >= 0");
    }
    conn.execute_batch(
        "CREATE TABLE IF NOT EXISTS DAILY_GOAL (DATE TEXT PRIMARY KEY,
         GOAL INTEGER DEFAULT NULL, ADJUSTED_GOAL INTEGER DEFAULT NULL)",
    )
    .context("ensure goal table")?;
    conn.execute(
        "INSERT INTO DAILY_GOAL (DATE, GOAL, ADJUSTED_GOAL) VALUES (?, ?, ?)
         ON CONFLICT(DATE) DO UPDATE SET GOAL=excluded.GOAL, ADJUSTED_GOAL=excluded.ADJUSTED_GOAL",
        rusqlite::params![today, goal, goal],
    )
    .context("write goal")?;
    Ok(())
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
                let msg = format!("{e:#}");
                let transient = msg.contains("locked")
                    || msg.contains("busy")
                    || msg.contains("No such file")
                    || msg.contains("I/O")
                    || msg.contains("interrupted");
                last = Some(e);
                if transient && attempt < 3 {
                    std::thread::sleep(std::time::Duration::from_millis(200));
                } else {
                    break;
                }
            }
        }
    }
    Err(last.unwrap()).with_context(|| format!("{what} failed after retries"))
}
const LOCK_TTL_SECS: u64 = 900;
const GLOBAL_LOCK_WAIT: std::time::Duration = std::time::Duration::from_secs(5);
pub(crate) struct LockGuard {
    path: PathBuf,
}
impl LockGuard {
    pub(crate) fn acquire_global(data_dir: &Path) -> Result<LockGuard> {
        let path = data_dir.join("locks").join("rwcore-global.lock");
        let deadline = std::time::Instant::now() + GLOBAL_LOCK_WAIT;
        loop {
            if let Some(g) = Self::try_acquire(&path)? {
                return Ok(g);
            }
            if std::time::Instant::now() >= deadline {
                return Self::acquire_path(&path);
            }
            std::thread::sleep(std::time::Duration::from_millis(50));
        }
    }
    pub(crate) fn lock_for_app(data_dir: &Path, app_id: &str) -> Result<LockGuard> {
        Self::acquire_path(&data_dir.join("locks").join(format!("rwcore-{app_id}.lock")))
    }
    fn acquire_path(path: &Path) -> Result<LockGuard> {
        Self::try_acquire(path)?.with_context(|| {
            format!(
                "another rwcore writer holds {} (stale locks expire after {LOCK_TTL_SECS}s)",
                path.display()
            )
        })
    }
    fn try_acquire(path: &Path) -> Result<Option<LockGuard>> {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .with_context(|| format!("cannot create {}", parent.display()))?;
        }
        match OpenOptions::new().write(true).create_new(true).open(path) {
            Ok(mut f) => {
                use std::io::Write as _;
                let _ = writeln!(f, "{} {}", std::process::id(), epoch_secs());
                Ok(Some(LockGuard {
                    path: path.to_path_buf(),
                }))
            }
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {
                if lock_stale(path)? {
                    std::fs::remove_file(path).ok();
                    return Self::try_acquire(path);
                }
                Ok(None)
            }
            Err(e) => Err(e).with_context(|| format!("cannot create lock {}", path.display())),
        }
    }
}
fn lock_stale(path: &Path) -> Result<bool> {
    let text = std::fs::read_to_string(path).unwrap_or_default();
    let ts: Option<u64> = text.split_whitespace().nth(1).and_then(|s| s.parse().ok());
    match ts {
        Some(t) => Ok(epoch_secs().saturating_sub(t) > LOCK_TTL_SECS),
        None => Ok(false),
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
        "{}-{}-{}-{}",
        epoch_secs(),
        std::process::id(),
        TMP_COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed),
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
pub fn check_file(path: &Path) -> Result<()> {
    let conn = Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_ONLY)
        .with_context(|| format!("cannot open {}", path.display()))?;
    let verdict: String = conn
        .query_row("PRAGMA quick_check", [], |r| r.get(0))
        .context("quick_check failed")?;
    if verdict == "ok" {
        Ok(())
    } else {
        anyhow::bail!("{} fails quick_check: {verdict}", path.display())
    }
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
    record_ts: Option<i64>,
    mut f: impl FnMut(&Transaction) -> Result<Vec<crate::oplog::OpKind>>,
) -> Result<PathBuf> {
    let _lock = LockGuard::lock_for_app(data_dir, &app.id)?;
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
            let ops = f(&tx)?;
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
    let _global = LockGuard::acquire_global(data_dir)?;
    if crate::sync::fingerprint(app)? != fp_pre {
        std::fs::remove_file(&tmp).ok();
        anyhow::bail!(
            "DIRTY_SOURCE app={} changed while staging the write (writes blocked); run `pull`",
            app.id
        );
    }
    let wb: Result<()> = (|| {
        let mut src = std::fs::File::open(&tmp).context("cannot reopen staged copy")?;
        let mut live = retry(
            || {
                OpenOptions::new()
                    .write(true)
                    .truncate(true)
                    .open(&app.backup_path)
                    .with_context(|| format!("cannot open live file {}", app.backup_path.display()))
            },
            "open live file",
        )?;
        if let Err(e) = std::io::copy(&mut src, &mut live).and_then(|_| live.sync_all()) {
            drop(live);
            let mut snap_src = std::fs::File::open(&snap)
                .context("writeback failed; cannot reopen snapshot for restore")?;
            let mut live2 = OpenOptions::new()
                .write(true)
                .truncate(true)
                .open(&app.backup_path)
                .context("writeback failed; cannot reopen live file for restore")?;
            std::io::copy(&mut snap_src, &mut live2)
                .and_then(|_| live2.sync_all())
                .context("writeback failed; snapshot restore failed")?;
            return Err(e).context("writeback failed; live file restored from snapshot");
        }
        Ok(())
    })();
    std::fs::remove_file(&tmp).ok();
    wb?;
    check_live(&app.backup_path).context("live copy unhealthy after write")?;
    let fp_post = crate::sync::fingerprint(app)?;
    if let Some(ts) = record_ts
        && !ops.is_empty()
    {
        crate::oplog::append_many(data_dir, &app.id, ts, ops, &fp_pre, &fp_post)?;
    }
    crate::sync::adopt(data_dir, app, &fp_post)?;
    Ok(snap)
}
fn check_live(path: &Path) -> Result<()> {
    let conn = retry(
        || {
            Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_ONLY)
                .with_context(|| format!("cannot open live {}", path.display()))
        },
        "open live",
    )?;
    let verdict: String = conn
        .query_row("PRAGMA quick_check", [], |r| r.get(0))
        .context("quick_check failed")?;
    if verdict == "ok" {
        Ok(())
    } else {
        anyhow::bail!("live copy fails quick_check: {verdict}")
    }
}
pub fn add_word(
    conn: &Connection,
    word: &str,
    transcription: Option<&str>,
    tr: &[(Lang, String)],
) -> Result<WordId> {
    let word = word.trim();
    if word.is_empty() {
        anyhow::bail!("word must not be empty");
    }
    let taken: i64 = conn
        .query_row("SELECT COUNT(*) FROM WORD WHERE WORD = ?", [word], |r| {
            r.get(0)
        })
        .context("dup check")?;
    if taken > 0 {
        anyhow::bail!("This word already exists");
    }
    let word_id = next_id(conn, "WORD")?;
    insert_word_row(conn, word_id, word, transcription, tr, &[])?;
    link_custom(conn, word_id)?;
    Ok(WordId(word_id))
}
pub fn readd_word(
    conn: &Connection,
    id: WordId,
    word: &str,
    transcription: Option<&str>,
    tr: &[(String, String)],
    ex: &[(String, String)],
    category: &str,
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
    let mut extyped = Vec::with_capacity(ex.len());
    for (code, text) in ex {
        let lang = Lang::parse(code).with_context(|| format!("bad stored lang '{code}'"))?;
        extyped.push((lang, text.clone()));
    }
    let taken: i64 =
        conn.query_row("SELECT COUNT(*) FROM WORD WHERE ID=?", [id.0], |r| r.get(0))?;
    if taken > 0 {
        anyhow::bail!("word id {} already exists, refusing replay insert", id.0);
    }
    insert_word_row(conn, id.0, word, transcription, &typed, &extyped)?;
    let cat_ok: i64 = conn
        .query_row(
            "SELECT COUNT(*) FROM CATEGORY WHERE ID=?",
            [category],
            |r| r.get(0),
        )
        .unwrap_or(0);
    if cat_ok > 0 {
        link_category(conn, id.0, category)?;
    } else {
        link_custom(conn, id.0)?;
    }
    Ok(())
}
pub(crate) fn custom_category(conn: &Connection) -> Result<String> {
    conn.query_row(
        "SELECT ID FROM CATEGORY WHERE IS_CUSTOM = 1 ORDER BY ID LIMIT 1",
        [],
        |r| r.get(0),
    )
    .context("no custom category (create one in the app first)")
}
fn link_custom(conn: &Connection, word_id: i64) -> Result<()> {
    let custom_cat = custom_category(conn)?;
    let wc_id = next_id(conn, "WORD_CATEGORY")?;
    conn.execute(
        "INSERT INTO WORD_CATEGORY (ID, WORD_ID, CATEGORY_ID) VALUES (?, ?, ?)",
        rusqlite::params![wc_id, word_id, custom_cat],
    )
    .context("insert WORD_CATEGORY")?;
    Ok(())
}
fn link_category(conn: &Connection, word_id: i64, category: &str) -> Result<()> {
    let wc_id = next_id(conn, "WORD_CATEGORY")?;
    conn.execute(
        "INSERT INTO WORD_CATEGORY (ID, WORD_ID, CATEGORY_ID) VALUES (?, ?, ?)",
        rusqlite::params![wc_id, word_id, category],
    )
    .context("insert WORD_CATEGORY")?;
    Ok(())
}
fn insert_word_row(
    conn: &Connection,
    word_id: i64,
    word: &str,
    transcription: Option<&str>,
    tr: &[(Lang, String)],
    ex: &[(Lang, String)],
) -> Result<()> {
    let langs = lang_cols(conn)?;
    let mut cols: Vec<String> = vec!["ID".to_string(), "WORD".to_string()];
    let mut placeholders: Vec<String> = vec!["?".to_string(), "?".to_string()];
    if transcription.is_some() {
        cols.push("TRANSCRIPTION".to_string());
        placeholders.push("?".to_string());
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
        cols.push(lang.code().to_string());
        placeholders.push("?".to_string());
        values.push(rusqlite::types::Value::Text(text.clone()));
    }
    for (lang, text) in ex {
        let col = format!("EXAMPLES_{}", lang.code());
        if !langs
            .iter()
            .any(|l| format!("EXAMPLES_{}", l.code()) == col)
        {
            anyhow::bail!("unknown examples column '{col}' for this app");
        }
        cols.push(col);
        placeholders.push("?".to_string());
        values.push(rusqlite::types::Value::Text(text.clone()));
    }
    let sql = format!(
        "INSERT INTO WORD ({}) VALUES ({})",
        cols.join(", "),
        placeholders.join(", ")
    );
    conn.execute(&sql, rusqlite::params_from_iter(values))
        .context("insert WORD")?;
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
        let other = if m == 1 { "REP" } else { "REC" };
        if s_new >= 7 {
            conn.execute(
                &format!(
                    "UPDATE WORD SET S_{sfx}=?, E_{sfx}=?, F_{sfx}=0, T_{sfx}=?,
                     I_{sfx}=NULL, I_{other}=NULL WHERE ID=?"
                ),
                rusqlite::params![s_new, e_new, now_ts, word.0],
            )
            .context("master WORD")?;
        } else {
            let i_new = crate::interval::ladder_interval(s_new, e_new, mode);
            let cross: Option<i64> = conn
                .query_row(
                    &format!("SELECT I_{other} FROM WORD WHERE ID = ?"),
                    [word.0],
                    |r| r.get(0),
                )
                .context("read cross interval")?;
            let cross = cross.map(|v| v.max(crate::interval::CROSS_SIDE_INTERVAL_SECS));
            conn.execute(
                &format!(
                    "UPDATE WORD SET S_{sfx}=?, E_{sfx}=?, F_{sfx}=0, T_{sfx}=?,
                     I_{sfx}=?, I_{other}=? WHERE ID=?"
                ),
                rusqlite::params![s_new, e_new, now_ts, i_new, cross, word.0],
            )
            .context("advance WORD")?;
        }
        Ok((e, f))
    } else {
        let (s, e, f): (i64, f64, i64) = conn
            .query_row(
                &format!("SELECT S_{sfx}, E_{sfx}, F_{sfx} FROM WORD WHERE ID = ?"),
                [word.0],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
            )
            .context("read mode state")?;
        let s_new = s.min(3);
        conn.execute(
            &format!(
                "UPDATE WORD SET S_{sfx}=?, E_{sfx}=MAX(E_{sfx}-0.5, 1.5), F_{sfx}=F_{sfx}+1,
                 T_{sfx}=?, I_{sfx}=30 WHERE ID=?"
            ),
            rusqlite::params![s_new, now_ts, word.0],
        )
        .context("record fail")?;
        Ok((e, f))
    }
}
pub fn triage_known(conn: &Connection, word: WordId, now_ts: i64, local_date: &str) -> Result<()> {
    conn.execute(
        "UPDATE WORD SET Q_REC=3, Q_REP=3, S_REC=0, S_REP=0,
        I_REC=NULL, I_REP=NULL WHERE ID=?",
        rusqlite::params![word.0],
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
pub fn advance_learn(
    conn: &Connection,
    word: WordId,
    now_ts: i64,
    local_date: &str,
) -> Result<Option<crate::oplog::OpKind>> {
    let (qr, qp): (i64, i64) = conn
        .query_row(
            "SELECT Q_REC, Q_REP FROM WORD WHERE ID = ?",
            [word.0],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .with_context(|| format!("no word id {}", word.0))?;
    if qr <= 0 && qp <= 0 {
        conn.execute(
            "UPDATE WORD SET Q_REC=1, Q_REP=1, S_REC=1, S_REP=1, T_REC=?, T_REP=?,
             I_REC=30, I_REP=30 WHERE ID=?",
            rusqlite::params![now_ts, now_ts, word.0],
        )
        .context("start learning WORD")?;
    } else if qr < 2 || qp < 2 {
        enroll_word(conn, word, now_ts, local_date)?;
    } else {
        return Ok(None);
    }
    Ok(Some(crate::oplog::OpKind::Triaged {
        id: word.0,
        decision: "learn".to_string(),
    }))
}
pub fn enroll_word(conn: &Connection, word: WordId, now_ts: i64, local_date: &str) -> Result<()> {
    let (e_rec, e_rep): (f64, f64) = conn
        .query_row(
            "SELECT E_REC, E_REP FROM WORD WHERE ID = ?",
            [word.0],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .context("read easiness")?;
    conn.execute(
        "UPDATE WORD SET Q_REC=2, Q_REP=2, S_REC=1, S_REP=1, T_REC=?, T_REP=?,
         I_REC=?, I_REP=? WHERE ID=?",
        rusqlite::params![
            now_ts,
            now_ts,
            crate::interval::ladder_interval(1, e_rec, crate::model::CardMode::Recognition),
            crate::interval::ladder_interval(1, e_rep, crate::model::CardMode::Reproduction),
            word.0
        ],
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
        CREATE TABLE DAILY_GOAL (DATE TEXT PRIMARY KEY, GOAL INTEGER DEFAULT NULL,
            ADJUSTED_GOAL INTEGER DEFAULT NULL);
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
    fn category_stats_counts_started_per_category() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute("UPDATE WORD SET Q_REC = 1 WHERE ID = 1", [])
            .unwrap();
        conn.execute(
            "INSERT INTO WORD (ID, WORD, Q_REC, Q_REP) VALUES (3, 'el agua', 2, 2)",
            [],
        )
        .unwrap();
        conn.execute("INSERT INTO WORD_CATEGORY VALUES (3, 3, 'food')", [])
            .unwrap();
        let stats = category_stats(&conn).unwrap();
        assert_eq!(stats.len(), 2);
        let custom = stats.iter().find(|s| s.category == "custom").unwrap();
        assert_eq!((custom.total, custom.started), (1, 1));
        let food = stats.iter().find(|s| s.category == "food").unwrap();
        assert_eq!((food.total, food.started), (2, 1));
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
        let snap = modify(&app, &cache, &data, Some(1_700_000_000), |tx| {
            let id = add_word(tx, "zztest", None, &[(Lang::Eng, "test".to_string())])?;
            assert_eq!(id, WordId(3));
            Ok(vec![crate::oplog::OpKind::Added {
                id: 3,
                text: "zztest".to_string(),
                transcription: None,
                tr: vec![("ENG".to_string(), "test".to_string())],
                ex: vec![],
                category: "custom".to_string(),
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
    fn modify_refuses_to_clobber_concurrent_live_change() {
        let (tmp, db) = fixture_db();
        let app = discover::App {
            id: "es".to_string(),
            container: "iCloud~ru~poas~englishwords~esen".to_string(),
            backup_path: db.clone(),
            backup_name: "reword_es.backup".to_string(),
        };
        let cache = tmp.path().join("cache");
        let data = tmp.path().join("data");
        let err = modify(&app, &cache, &data, Some(1_700_000_000), |tx| {
            add_word(tx, "ours", None, &[(Lang::Eng, "o".to_string())])?;
            let live = Connection::open(&app.backup_path)?;
            live.execute("INSERT INTO WORD (ID, WORD) VALUES (50, 'phone')", [])?;
            Ok(vec![])
        })
        .unwrap_err();
        assert!(format!("{err:#}").contains("DIRTY_SOURCE"));
        let conn = open_ro(&db).unwrap();
        assert!(get_word(&conn, "phone").unwrap().is_some());
        assert!(get_word(&conn, "ours").unwrap().is_none());
        assert!(crate::oplog::tail(&data, 10).is_empty());
    }
    #[test]
    fn categories_follow_the_title_column() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute_batch("ALTER TABLE CATEGORY RENAME COLUMN NAME_ENG TO NAME_RUS;")
            .unwrap();
        assert_eq!(cat_name_col(&conn).unwrap(), "NAME_RUS");
        let cats = categories(&conn).unwrap();
        assert!(
            cats.iter()
                .any(|c| c.id == "food" && c.name_en.as_deref() == Some("Food"))
        );
        assert_eq!(set_selected(&conn, "Food", true).unwrap(), "food");
        let food = list_words(
            &conn,
            &WordFilter {
                search: None,
                category: Some("Food"),
                limit: 50,
            },
        )
        .unwrap();
        assert_eq!(food.len(), 1);
        add_category(&conn, "mine", "Мои слова").unwrap();
        assert!(
            categories(&conn)
                .unwrap()
                .iter()
                .any(|c| c.id == "mine" && c.name_en.as_deref() == Some("Мои слова"))
        );
    }
    #[test]
    fn cross_null_stays_null_on_review() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute("UPDATE WORD SET S_REP=1, I_REP=1800 WHERE ID=1", [])
            .unwrap();
        grade_review(
            &conn,
            WordId(1),
            CardMode::Reproduction,
            true,
            5000,
            "2026-09-13",
        )
        .unwrap();
        let i: Option<i64> = conn
            .query_row("SELECT I_REC FROM WORD WHERE ID=1", [], |r| r.get(0))
            .unwrap();
        assert_eq!(i, None);
    }
    #[test]
    fn fail_easiness_floors() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute("UPDATE WORD SET E_REP=1.6 WHERE ID=1", [])
            .unwrap();
        grade_review(
            &conn,
            WordId(1),
            CardMode::Reproduction,
            false,
            5000,
            "2026-09-13",
        )
        .unwrap();
        let e: f64 = conn
            .query_row("SELECT E_REP FROM WORD WHERE ID=1", [], |r| r.get(0))
            .unwrap();
        assert!((e - 1.5).abs() < 1e-9);
    }
    #[test]
    fn mastered_counts_max_step() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute("UPDATE WORD SET S_REC=7, S_REP=2 WHERE ID=1", [])
            .unwrap();
        let t = today(&conn, "2026-09-13").unwrap();
        assert_eq!(t.mastered, 1);
    }
    #[test]
    fn goal_roundtrip() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        assert_eq!(get_goal(&conn, "20260913").unwrap(), None);
        set_goal(&conn, "20260913", 25).unwrap();
        assert_eq!(get_goal(&conn, "20260913").unwrap(), Some(25));
        set_goal(&conn, "20260913", 30).unwrap();
        assert_eq!(get_goal(&conn, "20260913").unwrap(), Some(30));
        let t = today(&conn, "2026-09-13").unwrap();
        assert_eq!(t.goal, Some(30));
    }
    #[test]
    fn add_word_rejects_duplicates() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        assert!(add_word(&conn, "el pan", None, &[(Lang::Eng, "x".to_string())]).is_err());
    }
    #[test]
    fn postpone_pushes_both_sides_one_day() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        postpone_word(&conn, WordId(1), 5000).unwrap();
        let st: (Option<i64>, Option<i64>, Option<i64>, Option<i64>) = conn
            .query_row(
                "SELECT T_REC, T_REP, I_REC, I_REP FROM WORD WHERE ID=1",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)),
            )
            .unwrap();
        assert_eq!(st, (Some(5000), Some(5000), Some(86400), Some(86400)));
        assert_eq!(log_count(&conn), 0);
        assert!(postpone_word(&conn, WordId(99), 5000).is_err());
    }
    #[test]
    fn remove_cascades_like_phone_delete() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        let id = add_word(&conn, "zz", None, &[(Lang::Eng, "t".to_string())]).unwrap();
        enroll_word(&conn, id, 1000, "2026-09-13").unwrap();
        let text = remove_word(&conn, id).unwrap();
        assert_eq!(text, "zz");
        assert!(get_word(&conn, "zz").unwrap().is_none());
        assert_eq!(log_count(&conn), 0);
    }
    #[test]
    fn reset_clears_progress_keeps_history() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        let id = add_word(&conn, "zz", None, &[(Lang::Eng, "t".to_string())]).unwrap();
        enroll_word(&conn, id, 1000, "2026-09-13").unwrap();
        grade_review(&conn, id, CardMode::Reproduction, true, 2000, "2026-09-13").unwrap();
        reset_word(&conn, id).unwrap();
        let st: (i64, i64, f64, i64, Option<i64>, Option<i64>) = conn
            .query_row(
                "SELECT Q_REC, S_REP, E_REP, F_REP, T_REP, I_REP FROM WORD WHERE ID=?",
                [id.0],
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
        assert_eq!(st, (0, 0, 2.5, 0, None, None));
        assert!(log_count(&conn) > 0);
    }
    #[test]
    fn category_admin_roundtrip() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        add_category(&conn, "mine", "Mine").unwrap();
        assert!(add_category(&conn, "mine", "Mine").is_err());
        assert!(add_category(&conn, "x", "My words").is_err());
        let rows = parse_csv("\"a\";\"x\"\n badline\n\"b\" ; \"y\" \n");
        assert_eq!(rows.len(), 2);
        let (added, skipped) = import_rows(&conn, "mine", &rows, &Lang::Rus).unwrap();
        assert_eq!((added, skipped), (2, 0));
        let (added2, skipped2) = import_rows(&conn, "mine", &rows, &Lang::Rus).unwrap();
        assert_eq!((added2, skipped2), (0, 2));
        let (_, ids) = category_words(&conn, "mine").unwrap();
        assert_eq!(ids.len(), 2);
        assert_eq!(reset_category(&conn, "mine").unwrap(), 2);
        assert_eq!(clear_category(&conn, "mine").unwrap(), 2);
        let (_, ids) = category_words(&conn, "mine").unwrap();
        assert!(ids.is_empty());
        assert!(clear_category(&conn, "food").is_err());
        assert!(remove_category(&conn, "food").is_err());
        assert_eq!(remove_category(&conn, "mine").unwrap(), 0);
        assert!(category_words(&conn, "mine").is_err());
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
             "UPDATE WORD SET Q_REC=2, Q_REP=2, T_REC=1000, I_REC=100, T_REP=9000, I_REP=100 WHERE ID=1;
              UPDATE WORD SET Q_REC=2, Q_REP=2, T_REC=1000, I_REC=100 WHERE ID=2;
              INSERT INTO WORD (ID, WORD) VALUES (3, 'parked');
              INSERT INTO WORD_CATEGORY VALUES (10, 3, 'custom');
              INSERT INTO WORD (ID, WORD, Q_REC, Q_REP, T_REC, I_REC, T_REP, I_REP) VALUES (5, 'both', 2, 2, 1000, 100, 1000, 100);
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
        conn.execute(
            "UPDATE WORD SET S_REP=1, I_REP=1800, I_REC=999 WHERE ID=1",
            [],
        )
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
        assert_eq!(st.3, Some(7000));
        assert_eq!(st.4, Some(30));
    }
    #[test]
    fn review_fail_caps_step_and_reschedules() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute(
            "UPDATE WORD SET S_REP=5, E_REP=3.0, T_REP=100, I_REP=999 WHERE ID=1",
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
        let st: (i64, i64, i64) = conn
            .query_row("SELECT S_REP, T_REP, I_REP FROM WORD WHERE ID=1", [], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?))
            })
            .unwrap();
        assert_eq!(st, (3, 7000, 30));
    }
    #[test]
    fn review_sixth_ok_masters_both_sides() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute(
            "UPDATE WORD SET S_REP=6, I_REP=5184000, I_REC=1 WHERE ID=1",
            [],
        )
        .unwrap();
        grade_review(
            &conn,
            WordId(1),
            CardMode::Reproduction,
            true,
            7000,
            "2026-09-13",
        )
        .unwrap();
        let st: (i64, Option<i64>, Option<i64>) = conn
            .query_row("SELECT S_REP, I_REP, I_REC FROM WORD WHERE ID=1", [], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?))
            })
            .unwrap();
        assert_eq!(st, (7, None, None));
    }
    #[test]
    fn triage_known_parks() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        triage_known(&conn, WordId(1), 8000, "2026-09-13").unwrap();
        let st: (i64, i64, i64, f64, Option<i64>, Option<i64>) = conn
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
        assert_eq!(st, (3, 3, 0, 2.5, None, None));
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
        let st: (i64, f64, i64, Option<i64>, Option<i64>) = conn
            .query_row(
                "SELECT S_REC, E_REC, F_REC, I_REC, T_REC FROM WORD WHERE ID=1",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?)),
            )
            .unwrap();
        assert_eq!(st, (0, 3.5, 2, None, Some(111)));
    }
    #[test]
    fn advance_learn_walks_stages() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        let op = advance_learn(&conn, WordId(1), 1000, "2026-09-13").unwrap();
        assert!(matches!(
            op,
            Some(crate::oplog::OpKind::Triaged { ref decision, .. }) if decision == "learn"
        ));
        let st: (i64, i64, i64, Option<i64>) = conn
            .query_row(
                "SELECT Q_REC, S_REC, T_REC, I_REC FROM WORD WHERE ID=1",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)),
            )
            .unwrap();
        assert_eq!(st, (1, 1, 1000, Some(30)));
        assert_eq!(log_count(&conn), 0);
        advance_learn(&conn, WordId(1), 2000, "2026-09-13").unwrap();
        let st2: (i64, i64) = conn
            .query_row("SELECT Q_REC, S_REC FROM WORD WHERE ID=1", [], |r| {
                Ok((r.get(0)?, r.get(1)?))
            })
            .unwrap();
        assert_eq!(st2, (2, 1));
        assert_eq!(log_count(&conn), 2);
        let i: Option<i64> = conn
            .query_row("SELECT I_REP FROM WORD WHERE ID=1", [], |r| r.get(0))
            .unwrap();
        assert_eq!(i, Some(1800));
        let noop = advance_learn(&conn, WordId(1), 3000, "2026-09-13").unwrap();
        assert!(noop.is_none());
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
    #[test]
    fn select_roundtrip_and_categories_flag() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        let cats = categories(&conn).unwrap();
        assert!(cats.iter().find(|c| c.id == "custom").unwrap().selected);
        assert!(!cats.iter().find(|c| c.id == "food").unwrap().selected);
        assert_eq!(set_selected(&conn, "food", true).unwrap(), "food");
        assert!(
            categories(&conn)
                .unwrap()
                .iter()
                .find(|c| c.id == "food")
                .unwrap()
                .selected
        );
        set_selected(&conn, "My words", false).unwrap();
        assert!(
            !categories(&conn)
                .unwrap()
                .iter()
                .find(|c| c.id == "custom")
                .unwrap()
                .selected
        );
        assert!(set_selected(&conn, "nope", true).is_err());
    }
    #[test]
    fn today_counts_learned_reviewed_and_streak() {
        let (_tmp, db) = fixture_db();
        let conn = Connection::open(&db).unwrap();
        conn.execute_batch(
            "INSERT INTO LOG VALUES (1, 100, '2026-09-10', 1, 1, 1, 1, 2, 2);
             INSERT INTO LOG VALUES (2, 200, '2026-09-11', 1, 1, 1, 1, 2, 2);
             INSERT INTO LOG VALUES (3, 300, '2026-09-12', 2, 1, 1, 1, 2, 2);
             INSERT INTO LOG VALUES (4, 400, '2026-09-13', 1, 2, 2, 2, 2, 0);
             INSERT INTO LOG VALUES (5, 500, '2026-09-13', 2, 2, 1, 1, 2, 0);
             UPDATE WORD SET Q_REC=1, Q_REP=1 WHERE ID=2;
             INSERT INTO DAILY_GOAL VALUES ('20260913', 40, 40);",
        )
        .unwrap();
        let t = today(&conn, "2026-09-13").unwrap();
        assert_eq!(t.learned, 1);
        assert_eq!(t.reviewed, 1);
        assert_eq!(t.memorizing, 1);
        assert_eq!(t.mastered, 0);
        assert_eq!(t.known, 0);
        assert_eq!(t.goal, Some(40));
        assert_eq!((t.streak_cur, t.streak_best), (4, 4));
        assert_eq!(t.active_dates.len(), 4);
        let t2 = today(&conn, "2026-09-14").unwrap();
        assert_eq!(t2.streak_cur, 4);
        assert_eq!((t2.learned, t2.reviewed), (0, 0));
        let t3 = today(&conn, "2026-09-11").unwrap();
        assert_eq!((t3.streak_cur, t3.streak_best), (2, 4));
    }
}
