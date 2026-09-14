//! Picking the next card the way the phone does (ReWord 4.3.4:
//! WordPresenter.j over the word repository a42 — n new words, m learning,
//! y review, l/q counts, o next review, w the card and its choose-from-4).
//! The SQL keeps the app's own text, so filters, ties and even its quirks
//! behave the same on the same SQLite.
use crate::model::{Word, WordId};
use crate::rules::{Blocks, NewMode, ReviewFrom, Rules, SideMode};
use crate::sched::Side;
use anyhow::{Context, Result};
use rusqlite::{Connection, OptionalExtension};
use serde::Serialize;
use std::collections::HashMap;

/// Which cards a session draws (ima).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Session {
    Smart,
    ReviewOnly,
    NewOnly,
}

impl Session {
    pub fn parse(s: &str) -> Result<Self> {
        match s.to_ascii_lowercase().as_str() {
            "smart" | "mixed" => Ok(Self::Smart),
            "review" | "review-only" => Ok(Self::ReviewOnly),
            "new" | "new-only" | "learn" => Ok(Self::NewOnly),
            _ => anyhow::bail!("bad session '{s}'; want smart|review|new"),
        }
    }

    fn learns(self) -> bool {
        self != Self::ReviewOnly
    }

    fn reviews(self) -> bool {
        self != Self::NewOnly
    }
}

/// splitmix64. The phone draws with java.util.Random; only fairness has
/// to match, and a seed keeps tests repeatable.
pub struct Rng(u64);

impl Rng {
    pub fn new(seed: u64) -> Self {
        Self(seed)
    }

    fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9E37_79B9_7F4A_7C15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        z ^ (z >> 31)
    }

    /// Random.nextInt(n) for n > 0.
    pub fn below(&mut self, n: usize) -> usize {
        (self.next() % n as u64) as usize
    }

    /// Random.nextInt() % 2 == 0.
    fn even(&mut self) -> bool {
        self.next() & 1 == 0
    }

    /// Collections.shuffle.
    pub fn shuffle<T>(&mut self, v: &mut [T]) {
        for i in (1..v.len()).rev() {
            let j = self.below(i + 1);
            v.swap(i, j);
        }
    }
}

/// The slice of the dictionary the phone shows (om8.o, om8.u): words
/// translated into the native language, in categories titled in it, for
/// the chosen English variant.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Scope {
    pub native: String,
    pub cat_col: Option<String>,
    pub region: Option<i64>,
}

fn columns(conn: &Connection, table: &str) -> Result<Vec<String>> {
    let mut st = conn.prepare(&format!("PRAGMA table_info({table})"))?;
    st.query_map([], |r| r.get::<_, String>(1))?
        .collect::<rusqlite::Result<Vec<_>>>()
        .with_context(|| format!("read {table} columns"))
}

fn settings_map(conn: &Connection) -> Result<HashMap<String, String>> {
    let mut st = conn.prepare("SELECT NAME, VALUE FROM SETTINGS WHERE VALUE IS NOT NULL")?;
    st.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?
        .collect::<rusqlite::Result<HashMap<_, _>>>()
        .context("decode SETTINGS row")
}

impl Scope {
    pub fn load(conn: &Connection) -> Result<Self> {
        let s = settings_map(conn)?;
        let native = s
            .get("native_language")
            .map(|v| v.to_ascii_uppercase())
            .context("no native_language in SETTINGS; the phone asks for it before any card")?;
        if native.len() != 3 || !native.bytes().all(|b| b.is_ascii_uppercase()) {
            anyhow::bail!("bad native_language '{native}'");
        }
        if !columns(conn, "WORD")?.contains(&native) {
            anyhow::bail!("WORD has no {native} column");
        }
        let cat = format!("NAME_{native}");
        let cat_col = columns(conn, "CATEGORY")?.contains(&cat).then_some(cat);
        // om8.u + iv7.b: a region counts only when the app offers it; the
        // American variant is REG 1, the British one REG 2.
        let offered = s.get("capability_regions").map(String::as_str).unwrap_or("");
        let region = s.get("region").map(String::as_str).unwrap_or("");
        let region = (!offered.is_empty() && offered.split(',').any(|r| r == region))
            .then(|| match region.to_ascii_lowercase().as_str() {
                "us" => Some(1),
                "br" => Some(2),
                _ => None,
            })
            .flatten();
        Ok(Self {
            native,
            cat_col,
            region,
        })
    }

    fn cat(&self) -> String {
        let mut s = String::new();
        if let Some(c) = &self.cat_col {
            s.push_str(&format!(" AND c.{c} IS NOT NULL"));
        }
        if let Some(r) = self.region {
            s.push_str(&format!(" AND (c.reg IS NULL OR c.reg = {r})"));
        }
        s
    }

    fn word(&self) -> String {
        let mut s = format!(" AND w.{} IS NOT NULL", self.native);
        if let Some(r) = self.region {
            s.push_str(&format!(" AND (w.reg IS NULL OR w.reg = {r})"));
        }
        s
    }
}

const FROM: &str = " FROM word w INNER JOIN word_category wc ON wc.word_id = w.id \
                    INNER JOIN category c ON c.id = wc.category_id";

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Source {
    New,
    Learning,
    Review,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Picked {
    pub word: WordId,
    pub side: Side,
    pub source: Source,
}

fn exclude(prev: Option<WordId>) -> String {
    prev.map(|p| format!(" AND w.id != {}", p.0)).unwrap_or_default()
}

/// Both sides allowed and eligible: a coin (even picks recognition);
/// otherwise the eligible one.
fn side_for(rec: bool, rep: bool, rng: &mut Rng) -> Side {
    if if rec && rep { rng.even() } else { rec } {
        Side::Rec
    } else {
        Side::Rep
    }
}

fn pick_flagged(conn: &Connection, sql: &str, source: Source, rng: &mut Rng) -> Result<Option<Picked>> {
    let mut st = conn.prepare(sql)?;
    let rows = st
        .query_map([], |r| Ok((r.get::<_, i64>(0)?, r.get::<_, bool>(1)?, r.get::<_, bool>(2)?)))?
        .collect::<rusqlite::Result<Vec<_>>>()?;
    if rows.is_empty() {
        return Ok(None);
    }
    let (id, rec, rep) = rows[rng.below(rows.len())];
    Ok(Some(Picked {
        word: WordId(id),
        side: side_for(rec, rep, rng),
        source,
    }))
}

/// a42.n: a random selected category that still has new words, then a
/// random new word in it — small categories come up as often as big ones.
/// `postponed` keeps words the user postponed out until they are due.
pub fn new_word(
    conn: &Connection,
    scope: &Scope,
    now: i64,
    mode: NewMode,
    postponed: bool,
    rng: &mut Rng,
) -> Result<Option<Picked>> {
    let post = if postponed {
        format!(" AND (w.t_rec IS NULL OR (w.i_rec IS NOT NULL AND w.t_rec + w.i_rec <= {now}))")
    } else {
        String::new()
    };
    let (cat, word) = (scope.cat(), scope.word());
    let cats: Vec<String> = conn
        .prepare(&format!(
            "SELECT DISTINCT c.id{FROM} WHERE c.is_selected{cat}{word} AND w.q_rec = 0 AND w.q_rep = 0{post}"
        ))?
        .query_map([], |r| r.get(0))?
        .collect::<rusqlite::Result<_>>()?;
    if cats.is_empty() {
        return Ok(None);
    }
    let chosen = &cats[rng.below(cats.len())];
    let words: Vec<i64> = conn
        .prepare(&format!(
            "SELECT DISTINCT w.id{FROM} WHERE c.id = ?1 AND c.is_selected{cat}{word} \
             AND w.q_rec = 0 AND w.q_rep = 0{post}"
        ))?
        .query_map([chosen], |r| r.get(0))?
        .collect::<rusqlite::Result<_>>()?;
    if words.is_empty() {
        return Ok(None);
    }
    let id = words[rng.below(words.len())];
    // we6: recognition, reproduction, or an odd draw for reproduction.
    let side = match mode {
        NewMode::Recognition => Side::Rec,
        NewMode::Reproduction => Side::Rep,
        NewMode::Random => {
            if rng.even() {
                Side::Rec
            } else {
                Side::Rep
            }
        }
    };
    Ok(Some(Picked {
        word: WordId(id),
        side,
        source: Source::New,
    }))
}

fn mode_cond(rec: bool, rep: bool) -> &'static str {
    match (rec, rep) {
        (true, true) => "(_mode_rec_allowed OR _mode_rep_allowed)",
        (true, false) => "_mode_rec_allowed",
        _ => "_mode_rep_allowed",
    }
}

/// a42.m: a random learning word in the selected categories. Unless `any`,
/// only a side whose 30 s or reset timer ran out counts.
pub fn learning_word(
    conn: &Connection,
    scope: &Scope,
    now: i64,
    mode: SideMode,
    any: bool,
    prev: Option<WordId>,
    rng: &mut Rng,
) -> Result<Option<Picked>> {
    let (rec_on, rep_on) = mode.allows();
    let flag = |on: bool, s: &str| {
        if !on {
            return "0".to_string();
        }
        let due = if any {
            String::new()
        } else {
            format!(" AND w.t_{s} IS NOT NULL AND w.i_{s} IS NOT NULL AND w.t_{s} + w.i_{s} <= {now}")
        };
        format!("(w.q_{s} = 1{due})")
    };
    let sql = format!(
        "SELECT DISTINCT w.id, {} AS _mode_rec_allowed, {} AS _mode_rep_allowed{FROM} \
         WHERE 1 AND c.is_selected{}{} AND {}{}",
        flag(rec_on, "rec"),
        flag(rep_on, "rep"),
        scope.cat(),
        scope.word(),
        mode_cond(rec_on, rep_on),
        exclude(prev)
    );
    pick_flagged(conn, &sql, Source::Learning, rng)
}

/// Review eligibility of one side: in review, the other side past
/// learning (review or retired), and due by `now`.
fn review_flag(on: bool, s: &str, o: &str, until: &str) -> String {
    if !on {
        return "0".to_string();
    }
    format!(
        "(w.q_{s} = 2 AND w.q_{o} IN (2, 4) AND w.t_{s} IS NOT NULL AND w.i_{s} IS NOT NULL{until})"
    )
}

fn review_where(scope: &Scope, selected_only: bool) -> String {
    format!(
        " WHERE 1{}{}{}",
        if selected_only { " AND c.is_selected" } else { "" },
        scope.cat(),
        scope.word()
    )
}

/// a42.y: the ten review cards due longest ago (then lowest step), and a
/// random one of them. `grace` lets cards due within the next minute in.
#[allow(clippy::too_many_arguments)]
pub fn review_word(
    conn: &Connection,
    scope: &Scope,
    now: i64,
    mode: SideMode,
    selected_only: bool,
    grace: bool,
    prev: Option<WordId>,
    rng: &mut Rng,
) -> Result<Option<Picked>> {
    let (rec_on, rep_on) = mode.allows();
    let plus = if grace { " + 60" } else { "" };
    let sql = format!(
        "SELECT DISTINCT w.id, {} AS _mode_rec_allowed, {} AS _mode_rep_allowed, \
         w.q_rec, w.q_rep, w.t_rec, w.i_rec, w.t_rep, w.i_rep, w.s_rec, w.s_rep{FROM}{} AND {}{} \
         ORDER BY CASE \
           WHEN w.q_rec = 2 AND w.q_rep = 2 THEN MIN(t_rec + i_rec, t_rep + i_rep) \
           WHEN w.q_rec = 2 THEN t_rec + i_rec \
           WHEN w.q_rep = 2 THEN t_rep + i_rep END ASC, CASE \
           WHEN w.q_rec = 2 AND w.q_rep = 2 THEN MIN(s_rec, s_rep) \
           WHEN w.q_rec = 2 THEN s_rec \
           WHEN w.q_rep = 2 THEN s_rep END ASC LIMIT 10",
        review_flag(rec_on, "rec", "rep", &format!(" AND w.t_rec + w.i_rec <= {now}{plus}")),
        review_flag(rep_on, "rep", "rec", &format!(" AND w.t_rep + w.i_rep <= {now}{plus}")),
        review_where(scope, selected_only),
        mode_cond(rec_on, rep_on),
        exclude(prev)
    );
    pick_flagged(conn, &sql, Source::Review, rng)
}

/// a42.l: learning words in the selected categories.
pub fn learning_count(conn: &Connection, scope: &Scope) -> Result<i64> {
    Ok(conn.query_row(
        &format!(
            "SELECT COUNT(*) FROM (SELECT DISTINCT w.id{FROM} WHERE 1 AND c.is_selected{}{} \
             AND (w.q_rec = 1 OR w.q_rep = 1))",
            scope.cat(),
            scope.word()
        ),
        [],
        |r| r.get(0),
    )?)
}

/// a42.q: the badge — review words due within the next minute.
pub fn due_count(conn: &Connection, scope: &Scope, now: i64, mode: SideMode, selected_only: bool) -> Result<i64> {
    let (rec_on, rep_on) = mode.allows();
    let sql = format!(
        "SELECT COUNT(DISTINCT w.id){FROM}{} AND {}",
        review_where(scope, selected_only),
        match (rec_on, rep_on) {
            (true, true) => format!(
                "({} OR {})",
                review_flag(true, "rec", "rep", &format!(" AND w.t_rec + w.i_rec <= {now} + 60")),
                review_flag(true, "rep", "rec", &format!(" AND w.t_rep + w.i_rep <= {now} + 60"))
            ),
            (true, false) => review_flag(true, "rec", "rep", &format!(" AND w.t_rec + w.i_rec <= {now} + 60")),
            _ => review_flag(true, "rep", "rec", &format!(" AND w.t_rep + w.i_rep <= {now} + 60")),
        }
    );
    Ok(conn.query_row(&sql, [], |r| r.get(0))?)
}

/// a42.o: when the next review card falls due, a minute early.
pub fn next_review(conn: &Connection, scope: &Scope, mode: SideMode, selected_only: bool) -> Result<Option<i64>> {
    let (rec_on, rep_on) = mode.allows();
    let rec = review_flag(rec_on, "rec", "rep", "");
    let rep = review_flag(rep_on, "rep", "rec", "");
    let cond = match (rec_on, rep_on) {
        (true, true) => format!("({rec} OR {rep})"),
        (true, false) => rec.clone(),
        _ => rep.clone(),
    };
    let sql = format!(
        "SELECT CASE WHEN {rec} AND {rep} THEN MIN(t_rec + i_rec, t_rep + i_rep) \
         WHEN {rec} THEN t_rec + i_rec WHEN {rep} THEN t_rep + i_rep ELSE NULL END AS _ts_earliest\
         {FROM}{} AND {cond} AND _ts_earliest IS NOT NULL ORDER BY _ts_earliest ASC LIMIT 1",
        review_where(scope, selected_only)
    );
    let ts: Option<i64> = conn.query_row(&sql, [], |r| r.get(0)).optional()?;
    Ok(ts.map(|t| t - 60))
}

/// w32/E: words learned today — both sides' LOG rows moving from learning
/// to review, on or after `today` and before `tomorrow` (YYYY-MM-DD).
pub fn learned_today(conn: &Connection, today: &str, tomorrow: &str) -> Result<i64> {
    Ok(conn.query_row(
        "SELECT COUNT(DISTINCT lrec.word_id) FROM log lrec INNER JOIN log lrep \
         ON lrep.word_id = lrec.word_id AND lrep.queue = lrec.queue AND lrep.nqueue = lrec.nqueue \
         AND lrep.mode = 2 AND (lrep.flags & 1) = 0 \
         WHERE (lrec.queue = 1 AND lrec.nqueue = 2) AND lrec.mode = 1 AND (lrec.flags & 1) = 0 \
         AND MAX(lrec.local_date, lrep.local_date) >= ?1 AND MAX(lrec.local_date, lrep.local_date) < ?2",
        [today, tomorrow],
        |r| r.get(0),
    )?)
}

/// a42.k/o02.a: today's goal — the day's ADJUSTED_GOAL once the phone
/// opened that day, the daily_goal setting before it did.
pub fn goal_today(conn: &Connection, rules: &Rules, today: &str) -> Result<Option<i64>> {
    let has: i64 = conn.query_row(
        "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'DAILY_GOAL'",
        [],
        |r| r.get(0),
    )?;
    if has == 0 {
        return Ok(rules.daily_goal);
    }
    let row: Option<Option<i64>> = conn
        .query_row("SELECT ADJUSTED_GOAL FROM DAILY_GOAL WHERE DATE = ?", [today], |r| r.get(0))
        .optional()?;
    Ok(row.unwrap_or(rules.daily_goal))
}

/// o02.b: the day's goal before any "continue" raised it (DAILY_GOAL.GOAL).
fn base_goal(conn: &Connection, rules: &Rules, today: &str) -> Result<Option<i64>> {
    let has: i64 = conn.query_row(
        "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'DAILY_GOAL'",
        [],
        |r| r.get(0),
    )?;
    if has == 0 {
        return Ok(rules.daily_goal);
    }
    let row: Option<Option<i64>> = conn
        .query_row("SELECT GOAL FROM DAILY_GOAL WHERE DATE = ?", [today], |r| r.get(0))
        .optional()?;
    Ok(row.unwrap_or(rules.daily_goal))
}

/// Where the day stands, for the header and the empty screens.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct Day {
    pub learning: i64,
    pub learned_today: i64,
    pub goal: Option<i64>,
    /// The goal before "continue" raised it; the raise dialog starts from it.
    pub base_goal: Option<i64>,
    pub goal_reached: bool,
    pub due: i64,
    pub next_review: Option<i64>,
}

pub fn day(conn: &Connection, rules: &Rules, scope: &Scope, now: i64, today: &str, tomorrow: &str) -> Result<Day> {
    let selected_only = rules.review_from == ReviewFrom::Selected;
    let goal = goal_today(conn, rules, today)?;
    let learned_today = learned_today(conn, today, tomorrow)?;
    Ok(Day {
        learning: learning_count(conn, scope)?,
        learned_today,
        goal,
        base_goal: base_goal(conn, rules, today)?,
        goal_reached: goal.is_some_and(|g| learned_today >= g),
        due: due_count(conn, scope, now, rules.review, selected_only)?,
        next_review: next_review(conn, scope, rules.review, selected_only)?,
    })
}

/// WordPresenter.j: the next card of a session. New words come while the
/// words in learning plus those learned today stay under the goal; each
/// draw first tries a new word (4 in 11), then a due learning word (6 in
/// 11), then the due review, then falls back step by step.
#[allow(clippy::too_many_arguments)]
pub fn next(
    conn: &Connection,
    rules: &Rules,
    scope: &Scope,
    session: Session,
    now: i64,
    today: &Day,
    prev: Option<WordId>,
    rng: &mut Rng,
) -> Result<Option<Picked>> {
    let learning = today.learning;
    let learns = session.learns() && !today.goal_reached;
    let news = learns && today.goal.is_none_or(|g| learning + today.learned_today < g);
    let reviews = session.reviews();
    let selected_only = rules.review_from == ReviewFrom::Selected;
    let new = |postponed: bool, rng: &mut Rng| new_word(conn, scope, now, rules.new_words, postponed, rng);
    let learn = |any: bool, prev: Option<WordId>, rng: &mut Rng| {
        learning_word(conn, scope, now, rules.learning, any, prev, rng)
    };
    let review = |grace: bool, prev: Option<WordId>, rng: &mut Rng| {
        review_word(conn, scope, now, rules.review, selected_only, grace, prev, rng)
    };
    if news
        && today.goal.is_some()
        && rng.below(11) <= 3
        && let Some(p) = new(true, rng)?
    {
        return Ok(Some(p));
    }
    if learns
        && learning > 0
        && rng.below(11) <= 5
        && let Some(p) = learn(false, prev, rng)?
    {
        return Ok(Some(p));
    }
    if reviews && let Some(p) = review(false, prev, rng)? {
        return Ok(Some(p));
    }
    if learns && learning > 0 && let Some(p) = learn(false, prev, rng)? {
        return Ok(Some(p));
    }
    if news && let Some(p) = new(true, rng)? {
        return Ok(Some(p));
    }
    if learns && learning > 0 && let Some(p) = learn(true, prev, rng)? {
        return Ok(Some(p));
    }
    if reviews && let Some(p) = review(true, prev, rng)? {
        return Ok(Some(p));
    }
    if learns && learning > 0 && let Some(p) = learn(true, None, rng)? {
        return Ok(Some(p));
    }
    if reviews && let Some(p) = review(true, None, rng)? {
        return Ok(Some(p));
    }
    if news {
        return new(false, rng);
    }
    Ok(None)
}

/// v88.b: the word's status from both sides' queues.
pub fn status(q_rec: i64, q_rep: i64) -> i64 {
    match (q_rec, q_rep) {
        (0, 0) => 0,
        (3, 3) => 3,
        (4, 4) => 4,
        _ if q_rec == 1 || q_rep == 1 => 1,
        _ => 2,
    }
}

fn quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "''"))
}

/// a42.w: the choose-from-4 answers — the word and three others, ranked by
/// category, part of speech, being in play rather than new or known, and
/// length; empty unless the guessing setting offers the block on this side
/// and exactly three others turn up. The app means the category rank to
/// favour the card's own categories, but its subquery carries two
/// IS_SELECTED columns and SQLite orders by the first, the category's own
/// flag — so words from any selected category rank first. Running the app's
/// SQL keeps that behaviour.
pub fn variants(
    conn: &Connection,
    rules: &Rules,
    scope: &Scope,
    word: &Word,
    side: Side,
    rng: &mut Rng,
) -> Result<Vec<Word>> {
    let offered = match side {
        Side::Rec => word.recognition.level != 0 && rules.guessing == Blocks::Both,
        Side::Rep => word.reproduction.level != 0 && rules.guessing != Blocks::Disabled,
    };
    if !offered {
        return Ok(vec![]);
    }
    let name = scope.cat_col.clone();
    let cats: Vec<(String, bool, Option<String>)> = conn
        .prepare(&format!(
            "SELECT c.ID, c.IS_SELECTED, {} FROM CATEGORY c \
             INNER JOIN WORD_CATEGORY wc ON wc.CATEGORY_ID = c.ID WHERE wc.WORD_ID = ?",
            name.as_deref().map_or("NULL".to_string(), |n| format!("c.{n}"))
        ))?
        .query_map([word.id.0], |r| Ok((r.get(0)?, r.get::<_, i64>(1)? != 0, r.get(2)?)))?
        .collect::<rusqlite::Result<_>>()?;
    if cats.is_empty() {
        return Ok(vec![]);
    }
    let own: Vec<String> = cats
        .iter()
        .filter(|(_, sel, n)| *sel && n.as_deref().is_some_and(|n| !n.is_empty()))
        .map(|(id, _, _)| quote(id))
        .collect();
    let own = if own.is_empty() { "''".to_string() } else { own.join(",") };
    let (creg, wreg) = match scope.region {
        Some(r) => (
            format!(" AND (C.REG IS NULL OR C.REG = {r})"),
            format!(" AND (W.REG IS NULL OR W.REG = {r})"),
        ),
        None => (String::new(), String::new()),
    };
    let cat_named = name.map(|n| format!("        AND C.{n} IS NOT NULL")).unwrap_or_default();
    let sql = format!(
        "SELECT ID FROM WORD WHERE ID IN (SELECT DISTINCT TMP.ID FROM (SELECT    W.ID,    W.WORD,    \
         C.IS_SELECTED,    CASE WHEN POS IS NULL THEN 0 ELSE POS END AS POS_NORM,    \
         CASE WHEN C.ID IN ({own}) THEN 1 ELSE 0 END AS IS_SELECTED,    \
         CASE WHEN ((W.q_rec = 0 AND W.q_rep = 0) OR (W.q_rec = 3 AND W.q_rep = 3)) THEN 1 ELSE 0 END AS IS_NEW_OR_KNOWN    \
         FROM WORD W    INNER JOIN WORD_CATEGORY WC ON WC.WORD_ID = W.ID    \
         INNER JOIN CATEGORY C ON C.ID = WC.CATEGORY_ID    WHERE 1        AND W.ID != {}{creg}{wreg}{cat_named}        \
         AND W.{} IS NOT NULL        AND LOWER(W.WORD) != ?    ) AS TMP ORDER BY    TMP.IS_SELECTED DESC,    \
         CASE WHEN TMP.POS_NORM & {} THEN 1 ELSE 0 END DESC,    TMP.IS_NEW_OR_KNOWN ASC,    \
         ABS(LENGTH(TMP.WORD) - {}) ASC,    RANDOM() LIMIT 3)",
        word.id.0,
        scope.native,
        word.pos.unwrap_or(0),
        // Java measures the word in UTF-16 units.
        word.text.encode_utf16().count()
    );
    let ids: Vec<i64> = conn
        .prepare(&sql)?
        .query_map([word.text.to_lowercase()], |r| r.get(0))?
        .collect::<rusqlite::Result<_>>()?;
    if ids.len() != 3 {
        return Ok(vec![]);
    }
    let mut out = vec![word.clone()];
    for id in ids {
        out.push(crate::store::get_word(conn, &id.to_string())?.with_context(|| format!("no word id {id}"))?);
    }
    rng.shuffle(&mut out);
    Ok(out)
}

/// One card as the phone lays it out (hla + WordCardView.d): the word, the
/// side it asks, its choose-from-4, and which blocks it offers. A new word
/// has no blocks; the keyboard and choose come on reproduction cards, or on
/// both sides when the setting says both.
#[derive(Debug, Clone, Serialize)]
pub struct Card {
    pub word: Word,
    pub side: i64,
    pub source: Source,
    /// The asked side's queue, which picks what a swipe does.
    pub queue: i64,
    /// v88.b status, which names the swipe answers.
    pub status: i64,
    pub variants: Vec<Word>,
    pub keyboard: bool,
    pub choose: bool,
}

pub fn card(conn: &Connection, rules: &Rules, scope: &Scope, p: Picked, rng: &mut Rng) -> Result<Card> {
    card_with(conn, rules, scope, p, &[], rng)
}

/// A card with the choose-from-4 it showed before: the phone's undo brings
/// a word back with the answers it had (a42.w with the kept ids). Empty
/// `shown`, or a variant gone since, draws them afresh.
pub fn card_with(
    conn: &Connection,
    rules: &Rules,
    scope: &Scope,
    p: Picked,
    shown: &[WordId],
    rng: &mut Rng,
) -> Result<Card> {
    let word = crate::store::get_word(conn, &p.word.0.to_string())?.with_context(|| format!("no word id {}", p.word.0))?;
    let again: Option<Vec<Word>> = shown
        .iter()
        .map(|id| crate::store::get_word(conn, &id.0.to_string()).ok().flatten())
        .collect();
    let variants = match again {
        Some(v) if !v.is_empty() => v,
        _ => variants(conn, rules, scope, &word, p.side, rng)?,
    };
    let (q_rec, q_rep) = (word.recognition.level, word.reproduction.level);
    let status = status(q_rec, q_rep);
    let on_side = |b: Blocks| b == Blocks::Both || b != Blocks::Disabled && p.side == Side::Rep;
    Ok(Card {
        side: p.side.mode(),
        source: p.source,
        queue: if p.side == Side::Rec { q_rec } else { q_rep },
        status,
        keyboard: status != 0 && on_side(rules.keyboard),
        choose: status != 0 && on_side(rules.guessing) && !variants.is_empty(),
        variants,
        word,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    const NOW: i64 = 1_789_246_418;

    fn db(settings: &[(&str, &str)]) -> Connection {
        let conn = Connection::open_in_memory().unwrap();
        conn.execute_batch(
            "CREATE TABLE SETTINGS (NAME TEXT PRIMARY KEY NOT NULL, VALUE TEXT);
             CREATE TABLE CATEGORY (ID TEXT PRIMARY KEY NOT NULL, IS_CUSTOM INTEGER NOT NULL DEFAULT 0,
                 IS_SELECTED INTEGER NOT NULL DEFAULT 1, REG INTEGER DEFAULT NULL,
                 NAME_ENG TEXT, NAME_RUS TEXT);
             CREATE TABLE WORD (ID INTEGER PRIMARY KEY, WORD TEXT NOT NULL DEFAULT '', POS INTEGER,
                 REG INTEGER, TRANSCRIPTION TEXT, RUS TEXT, ENG TEXT,
                 Q_REC INTEGER NOT NULL DEFAULT 0, Q_REP INTEGER NOT NULL DEFAULT 0,
                 T_REC INTEGER, T_REP INTEGER, I_REC INTEGER, I_REP INTEGER,
                 S_REC INTEGER NOT NULL DEFAULT 0, S_REP INTEGER NOT NULL DEFAULT 0,
                 E_REC REAL NOT NULL DEFAULT 2.5, E_REP REAL NOT NULL DEFAULT 2.5,
                 F_REC INTEGER NOT NULL DEFAULT 0, F_REP INTEGER NOT NULL DEFAULT 0);
             CREATE TABLE WORD_CATEGORY (ID INTEGER PRIMARY KEY, WORD_ID INTEGER NOT NULL, CATEGORY_ID TEXT NOT NULL);
             CREATE TABLE LOG (ID INTEGER PRIMARY KEY, TIMESTAMP INTEGER NOT NULL, LOCAL_DATE TEXT NOT NULL,
                 WORD_ID INTEGER NOT NULL, MODE INTEGER NOT NULL, QUEUE INTEGER NOT NULL, STEP INTEGER NOT NULL,
                 NQUEUE INTEGER NOT NULL, FLAGS INTEGER NOT NULL DEFAULT 0);
             CREATE TABLE DAILY_GOAL (DATE TEXT PRIMARY KEY, GOAL INTEGER, ADJUSTED_GOAL INTEGER);
             INSERT INTO SETTINGS VALUES ('native_language', 'RUS');",
        )
        .unwrap();
        for (k, v) in settings {
            conn.execute("INSERT OR REPLACE INTO SETTINGS VALUES (?, ?)", [k, v]).unwrap();
        }
        conn
    }

    fn cat(conn: &Connection, id: &str, selected: bool) {
        conn.execute(
            "INSERT INTO CATEGORY (ID, IS_SELECTED, NAME_ENG, NAME_RUS) VALUES (?, ?, ?, ?)",
            rusqlite::params![id, selected as i64, id, id],
        )
        .unwrap();
    }

    /// A word in `category` with its scheduling columns set by `sql`
    /// (a SET list, may be empty).
    fn word(conn: &Connection, id: i64, text: &str, category: &str, set: &str) {
        conn.execute(
            "INSERT INTO WORD (ID, WORD, RUS) VALUES (?, ?, ?)",
            rusqlite::params![id, text, format!("перевод {text}")],
        )
        .unwrap();
        conn.execute(
            "INSERT INTO WORD_CATEGORY (WORD_ID, CATEGORY_ID) VALUES (?, ?)",
            rusqlite::params![id, category],
        )
        .unwrap();
        if !set.is_empty() {
            conn.execute(&format!("UPDATE WORD SET {set} WHERE ID = ?"), [id]).unwrap();
        }
    }

    fn scope(conn: &Connection) -> Scope {
        Scope::load(conn).unwrap()
    }

    const DUE_REVIEW: &str = "Q_REC=2, Q_REP=2, T_REC=1000, T_REP=1000, I_REC=100, I_REP=100, S_REC=2, S_REP=2";

    #[test]
    fn scope_reads_native_language_and_region() {
        let c = db(&[("capability_regions", "us,br"), ("region", "us")]);
        assert_eq!(
            scope(&c),
            Scope {
                native: "RUS".into(),
                cat_col: Some("NAME_RUS".into()),
                region: Some(1)
            }
        );
        let c = db(&[("capability_regions", "us,br"), ("region", "br")]);
        assert_eq!(scope(&c).region, Some(2));
        // A region the app does not offer counts as none.
        let c = db(&[("region", "us")]);
        assert_eq!(scope(&c).region, None);
        let c = Connection::open_in_memory().unwrap();
        c.execute_batch("CREATE TABLE SETTINGS (NAME TEXT, VALUE TEXT);").unwrap();
        assert!(Scope::load(&c).is_err(), "no native language, no cards");
    }

    #[test]
    fn new_words_draw_a_category_first() {
        let c = db(&[]);
        cat(&c, "small", true);
        cat(&c, "big", true);
        cat(&c, "off", false);
        word(&c, 1, "uno", "small", "");
        for id in 10..20 {
            word(&c, id, &format!("w{id}"), "big", "");
        }
        word(&c, 30, "hidden", "off", "");
        word(&c, 31, "started", "big", "Q_REC=1, Q_REP=1");
        word(&c, 32, "untranslated", "big", "");
        c.execute("UPDATE WORD SET RUS = NULL WHERE ID = 32", []).unwrap();
        word(&c, 33, "postponed", "small", &format!("T_REC={NOW}, I_REC=100000"));
        let s = scope(&c);
        let mut rng = Rng::new(7);
        let mut small = 0;
        for _ in 0..400 {
            let p = new_word(&c, &s, NOW, NewMode::Recognition, true, &mut rng).unwrap().unwrap();
            assert!(p.word.0 == 1 || (10..20).contains(&p.word.0), "picked {:?}", p.word);
            assert_eq!((p.side, p.source), (Side::Rec, Source::New));
            small += (p.word.0 == 1) as i32;
        }
        // One word alone in its category comes up about half the time.
        assert!((150..250).contains(&small), "small={small}");
        // Without the postponed filter the postponed word is back in play.
        let mut seen = false;
        for _ in 0..200 {
            let p = new_word(&c, &s, NOW - 200_000, NewMode::Reproduction, false, &mut rng).unwrap().unwrap();
            assert_eq!(p.side, Side::Rep);
            seen |= p.word.0 == 33;
        }
        assert!(seen);
    }

    #[test]
    fn learning_words_wait_for_their_timer() {
        let c = db(&[]);
        cat(&c, "a", true);
        word(&c, 1, "due", "a", &format!("Q_REC=1, Q_REP=1, T_REC={}, T_REP={}, I_REC=30, I_REP=30", NOW - 100, NOW - 100));
        word(&c, 2, "waiting", "a", &format!("Q_REC=1, Q_REP=1, T_REC={NOW}, T_REP={NOW}, I_REC=30, I_REP=30"));
        let s = scope(&c);
        let mut rng = Rng::new(1);
        for _ in 0..50 {
            let p = learning_word(&c, &s, NOW, SideMode::Reproduction, false, None, &mut rng).unwrap().unwrap();
            assert_eq!((p.word.0, p.side), (1, Side::Rep));
        }
        assert!(learning_word(&c, &s, NOW, SideMode::Reproduction, false, Some(WordId(1)), &mut rng).unwrap().is_none());
        let p = learning_word(&c, &s, NOW, SideMode::Reproduction, true, Some(WordId(1)), &mut rng).unwrap().unwrap();
        assert_eq!(p.word.0, 2, "any learning word once none is due");
        assert_eq!(learning_count(&c, &s).unwrap(), 2);
    }

    #[test]
    fn review_takes_the_ten_longest_due() {
        let c = db(&[]);
        cat(&c, "a", true);
        for id in 1..=12 {
            // Due at 1000 + id * 10: ids 11 and 12 fall outside the ten.
            word(
                &c,
                id,
                &format!("w{id}"),
                "a",
                &format!("Q_REC=2, Q_REP=2, T_REC={}, T_REP={}, I_REC=100, I_REP=100, S_REC=2, S_REP=2", 900 + id * 10, 900 + id * 10),
            );
        }
        word(&c, 20, "learning-other-side", "a", "Q_REC=2, Q_REP=1, T_REC=100, I_REC=100");
        word(&c, 21, "not-due", "a", &format!("Q_REC=2, Q_REP=2, T_REC={NOW}, T_REP={NOW}, I_REC=9999, I_REP=9999"));
        let s = scope(&c);
        let mut rng = Rng::new(3);
        let mut sides = [0, 0];
        for _ in 0..300 {
            let p = review_word(&c, &s, NOW, SideMode::RecognitionOrReproduction, true, false, None, &mut rng)
                .unwrap()
                .unwrap();
            assert!((1..=10).contains(&p.word.0), "picked {:?}", p.word);
            sides[(p.side == Side::Rep) as usize] += 1;
        }
        assert!(sides[0] > 100 && sides[1] > 100, "both sides drawn: {sides:?}");
        let p = review_word(&c, &s, NOW, SideMode::Recognition, true, false, None, &mut rng).unwrap().unwrap();
        assert_eq!(p.side, Side::Rec);
        assert_eq!(due_count(&c, &s, NOW, SideMode::RecognitionOrReproduction, true).unwrap(), 12);
        // Word 1 falls due first, at 910 + 100; the phone wakes a minute early.
        assert_eq!(next_review(&c, &s, SideMode::RecognitionOrReproduction, true).unwrap(), Some(950));
    }

    #[test]
    fn review_grace_and_category_scope() {
        let c = db(&[]);
        cat(&c, "on", true);
        cat(&c, "off", false);
        word(&c, 1, "soon", "on", &format!("Q_REC=2, Q_REP=2, T_REC={NOW}, T_REP={NOW}, I_REC=30, I_REP=30"));
        word(&c, 2, "elsewhere", "off", DUE_REVIEW);
        let s = scope(&c);
        let mut rng = Rng::new(5);
        let rr = SideMode::RecognitionOrReproduction;
        assert!(review_word(&c, &s, NOW, rr, true, false, None, &mut rng).unwrap().is_none());
        assert_eq!(review_word(&c, &s, NOW, rr, true, true, None, &mut rng).unwrap().unwrap().word.0, 1);
        assert_eq!(review_word(&c, &s, NOW, rr, false, false, None, &mut rng).unwrap().unwrap().word.0, 2);
        assert_eq!(due_count(&c, &s, NOW, rr, true).unwrap(), 1, "the badge counts a minute ahead");
    }

    #[test]
    fn learned_today_needs_both_sides() {
        let c = db(&[]);
        c.execute_batch(
            "INSERT INTO LOG VALUES (1, 1, '2026-09-14', 1, 2, 1, 1, 2, 0), (2, 1, '2026-09-14', 1, 1, 1, 1, 2, 2);
             INSERT INTO LOG VALUES (3, 1, '2026-09-14', 2, 2, 1, 1, 2, 0);
             INSERT INTO LOG VALUES (4, 1, '2026-09-13', 3, 2, 1, 1, 2, 0), (5, 1, '2026-09-13', 3, 1, 1, 1, 2, 2);
             INSERT INTO LOG VALUES (6, 1, '2026-09-14', 4, 2, 1, 1, 2, 1), (7, 1, '2026-09-14', 4, 1, 1, 1, 2, 2);",
        )
        .unwrap();
        assert_eq!(learned_today(&c, "2026-09-14", "2026-09-15").unwrap(), 1);
    }

    #[test]
    fn goal_follows_the_day_row() {
        let c = db(&[("daily_goal", "30")]);
        let rules = crate::store::rules(&c).unwrap();
        assert_eq!(goal_today(&c, &rules, "2026-09-14").unwrap(), Some(30));
        c.execute("INSERT INTO DAILY_GOAL VALUES ('2026-09-14', 30, 45)", []).unwrap();
        assert_eq!(goal_today(&c, &rules, "2026-09-14").unwrap(), Some(45));
    }

    fn today(c: &Connection, rules: &Rules) -> Day {
        day(c, rules, &scope(c), NOW, "2026-09-14", "2026-09-15").unwrap()
    }

    #[test]
    fn sessions_draw_what_they_allow() {
        let c = db(&[("daily_goal", "2")]);
        cat(&c, "a", true);
        word(&c, 1, "fresh", "a", "");
        word(&c, 2, "review", "a", DUE_REVIEW);
        let rules = crate::store::rules(&c).unwrap();
        let s = scope(&c);
        let mut rng = Rng::new(11);
        let d = today(&c, &rules);
        for _ in 0..50 {
            let p = next(&c, &rules, &s, Session::ReviewOnly, NOW, &d, None, &mut rng).unwrap().unwrap();
            assert_eq!(p.source, Source::Review);
            let p = next(&c, &rules, &s, Session::NewOnly, NOW, &d, None, &mut rng).unwrap().unwrap();
            assert_eq!(p.source, Source::New);
        }
        let mut seen = [false, false];
        for _ in 0..100 {
            let p = next(&c, &rules, &s, Session::Smart, NOW, &d, None, &mut rng).unwrap().unwrap();
            seen[(p.source == Source::Review) as usize] = true;
        }
        assert_eq!(seen, [true, true], "smart mixes new and review");
    }

    #[test]
    fn the_goal_caps_new_words() {
        let c = db(&[("daily_goal", "1")]);
        cat(&c, "a", true);
        word(&c, 1, "fresh", "a", "");
        word(&c, 2, "learning", "a", "Q_REC=1, Q_REP=1, T_REC=1, T_REP=1, I_REC=30, I_REP=30");
        let rules = crate::store::rules(&c).unwrap();
        let s = scope(&c);
        let mut rng = Rng::new(2);
        let d = today(&c, &rules);
        // One word in learning already fills a goal of one: no new word.
        for _ in 0..50 {
            let p = next(&c, &rules, &s, Session::NewOnly, NOW, &d, None, &mut rng).unwrap().unwrap();
            assert_eq!(p.word.0, 2);
        }
        // Once the goal is met, learning stops altogether.
        c.execute_batch(
            "INSERT INTO LOG VALUES (1, 1, '2026-09-14', 9, 2, 1, 1, 2, 0), (2, 1, '2026-09-14', 9, 1, 1, 1, 2, 2);",
        )
        .unwrap();
        let d = today(&c, &rules);
        assert!(d.goal_reached);
        assert!(next(&c, &rules, &s, Session::NewOnly, NOW, &d, None, &mut rng).unwrap().is_none());
    }

    #[test]
    fn choose_ties_on_the_category_flag_like_the_phone() {
        // The app means to rank words sharing the card's category first, but
        // its subquery has two IS_SELECTED columns and SQLite resolves
        // TMP.IS_SELECTED to the first, the category's own flag: words from
        // any selected category tie, and chance decides among them.
        let c = db(&[("enable_guessing_game", "foreign")]);
        cat(&c, "mine", true);
        cat(&c, "other", true);
        word(&c, 1, "casa", "mine", DUE_REVIEW);
        for (id, t) in [(2, "mesa"), (3, "sala"), (4, "cama")] {
            word(&c, id, t, "mine", "Q_REC=2, Q_REP=2");
        }
        for id in 10..16 {
            word(&c, id, &format!("ot{id}"), "other", "Q_REC=2, Q_REP=2");
        }
        let rules = crate::store::rules(&c).unwrap();
        let s = scope(&c);
        let mut rng = Rng::new(9);
        let w = crate::store::get_word(&c, "1").unwrap().unwrap();
        let mut others = false;
        for _ in 0..40 {
            let v = variants(&c, &rules, &s, &w, Side::Rep, &mut rng).unwrap();
            others |= v.iter().any(|w| w.id.0 >= 10);
        }
        assert!(others, "an equally ranked word from another selected category turns up");
    }

    #[test]
    fn choose_offers_four_with_selected_categories_first() {
        let c = db(&[("enable_guessing_game", "foreign")]);
        cat(&c, "mine", true);
        cat(&c, "other", false);
        word(&c, 1, "casa", "mine", DUE_REVIEW);
        for (id, t) in [(2, "mesa"), (3, "sala"), (4, "cama")] {
            word(&c, id, t, "mine", "Q_REC=2, Q_REP=2");
        }
        for id in 10..16 {
            word(&c, id, &format!("ot{id}"), "other", "Q_REC=2, Q_REP=2");
        }
        let rules = crate::store::rules(&c).unwrap();
        let s = scope(&c);
        let mut rng = Rng::new(9);
        let w = crate::store::get_word(&c, "1").unwrap().unwrap();
        for _ in 0..30 {
            let v = variants(&c, &rules, &s, &w, Side::Rep, &mut rng).unwrap();
            let mut ids: Vec<i64> = v.iter().map(|w| w.id.0).collect();
            ids.sort();
            assert_eq!(ids, vec![1, 2, 3, 4], "words from selected categories come first");
        }
        // Foreign guessing leaves recognition cards without the block.
        assert!(variants(&c, &rules, &s, &w, Side::Rec, &mut rng).unwrap().is_empty());
        let card = card(&c, &rules, &s, Picked { word: WordId(1), side: Side::Rep, source: Source::Review }, &mut rng).unwrap();
        assert!(card.keyboard && card.choose);
        assert_eq!((card.status, card.queue, card.variants.len()), (2, 2, 4));
        let card2 = super::card(&c, &rules, &s, Picked { word: WordId(1), side: Side::Rec, source: Source::Review }, &mut rng).unwrap();
        assert!(!card2.keyboard && !card2.choose);
    }

    #[test]
    fn a_new_word_has_no_blocks() {
        let c = db(&[("enable_guessing_game", "both"), ("enable_words_keyboard_input", "both")]);
        cat(&c, "a", true);
        word(&c, 1, "fresh", "a", "");
        for id in 2..6 {
            word(&c, id, &format!("w{id}"), "a", "");
        }
        let rules = crate::store::rules(&c).unwrap();
        let mut rng = Rng::new(4);
        let card = card(&c, &rules, &scope(&c), Picked { word: WordId(1), side: Side::Rep, source: Source::New }, &mut rng).unwrap();
        assert!(!card.keyboard && !card.choose && card.variants.is_empty());
    }

    #[test]
    fn status_matches_v88() {
        assert_eq!(
            [(0, 0), (1, 1), (2, 1), (2, 2), (2, 4), (3, 3), (4, 4)].map(|(a, b)| status(a, b)),
            [0, 1, 1, 2, 2, 3, 4]
        );
    }
}
