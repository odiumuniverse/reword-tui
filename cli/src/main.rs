mod discover;
mod interval;
mod matcher;
mod model;
mod oplog;
mod output;
mod rules;
mod sched;
mod select;
mod store;
mod sync;
use crate::model::Lang;
use crate::oplog::OpKind;
use anyhow::{Context, Result};
use clap::{Parser, Subcommand};
use output::Format;
use std::path::PathBuf;
struct Receipt {
    applied: bool,
    orphaned: bool,
    snapshot: Option<PathBuf>,
    detail: serde_json::Value,
}
fn now_local() -> (i64, String) {
    let now = chrono::Local::now();
    (now.timestamp(), now.format("%F").to_string())
}
fn body_time(v: &serde_json::Value) -> Result<(i64, String)> {
    match v["ts"].as_i64() {
        Some(ts) => {
            let date = chrono::DateTime::from_timestamp(ts, 0)
                .context("bad .ts")?
                .with_timezone(&chrono::Local)
                .format("%F")
                .to_string();
            Ok((ts, date))
        }
        None => Ok(now_local()),
    }
}
fn read_db(cli: &Cli, app: &discover::App) -> Result<rusqlite::Connection> {
    store::open_ro(cli.db.as_deref().unwrap_or(&app.backup_path))
}
fn apply_local(
    db: &std::path::Path,
    v: &serde_json::Value,
    ts: i64,
    date: &str,
) -> Result<serde_json::Value> {
    use crate::sched::Action;
    let mut conn =
        rusqlite::Connection::open(db).with_context(|| format!("cannot open {}", db.display()))?;
    let tx = conn.transaction()?;
    let word = |tx: &rusqlite::Connection| -> Result<crate::model::Word> {
        let q = v["word"].as_str().context("missing .word")?;
        store::get_word(tx, q)?.with_context(|| format!("no word '{q}'"))
    };
    let op = v["op"].as_str().unwrap_or("");
    let detail = match op {
        "grade" => {
            let mode = parse_mode(v["mode"].as_str().unwrap_or(""))?;
            let ok = parse_result(v["result"].as_str().unwrap_or(""))?;
            let w = word(&tx)?;
            store::grade_review(&tx, w.id, mode, ok, ts, date)?;
            serde_json::json!({ "word": w.id.0, "ok": ok })
        }
        "triage" | "enroll" => {
            let known = match (op, v["decision"].as_str().unwrap_or("")) {
                ("enroll", _) | (_, "learn") => false,
                (_, "known") => true,
                (_, x) => anyhow::bail!("bad decision '{x}'; want known|learn"),
            };
            let w = word(&tx)?;
            if known {
                store::triage_known(&tx, w.id, ts, date)?;
            } else {
                store::advance_learn(&tx, w.id, ts, date)?;
            }
            serde_json::json!({ "word": w.id.0, "known": known })
        }
        "answer" => {
            let mode = parse_mode(v["mode"].as_str().unwrap_or(""))?;
            let positive = v["positive"].as_bool().context("missing .positive")?;
            let w = word(&tx)?;
            let side = store::side_of(mode)?;
            let pre = store::sched_row(&tx, w.id)?;
            let action = Action::of(pre.queue(side), positive);
            store::answer(&tx, w.id, side, action, ts, date)?;
            serde_json::json!({ "word": w.id.0, "action": action.name(), "pre": pre, "at": ts })
        }
        "setting" => {
            let name = v["name"].as_str().context("missing .name")?;
            let value = v["value"].as_str().context("missing .value")?;
            store::set_setting(&tx, name, value)?;
            serde_json::json!({ "name": name, "value": value })
        }
        "restore" => {
            let w = word(&tx)?;
            let row: crate::sched::Row =
                serde_json::from_value(v["row"].clone()).context("bad .row")?;
            let at = v["at"].as_i64().context("missing .at")?;
            store::restore_answer(&tx, w.id, &row, at)?;
            serde_json::json!({ "word": w.id.0 })
        }
        "goal" => {
            let goal = v["goal"].as_i64().context("missing .goal")?;
            store::set_goal(&tx, date, goal)?;
            serde_json::json!({ "goal": goal })
        }
        "raise_goal" => {
            let by = v["by"].as_i64().context("missing .by")?;
            let adjusted = store::raise_goal(&tx, date, by)?;
            serde_json::json!({ "adjusted": adjusted })
        }
        "add" => {
            let text = v["word"].as_str().context("missing .word")?;
            if text.trim().is_empty() {
                anyhow::bail!("missing .word");
            }
            let trs = v["tr"]
                .as_array()
                .context("missing .tr array")?
                .iter()
                .map(|t| t.as_str().unwrap_or("").to_string())
                .collect::<Vec<_>>();
            let tr = parse_tr(&trs)?;
            if tr.is_empty() || tr.iter().any(|(_, t)| t.trim().is_empty()) {
                anyhow::bail!("bad .tr entries; want LANG=TEXT with non-empty TEXT");
            }
            let id = store::add_word(&tx, text, v["transcription"].as_str(), &tr)?;
            if v["enroll"].as_bool().unwrap_or(false) {
                store::enroll_word(&tx, id, ts, date)?;
            }
            serde_json::json!({ "word": text, "id": id.0 })
        }
        "remove" => {
            let w = word(&tx)?;
            store::remove_word(&tx, w.id)?;
            serde_json::json!({ "word": w.id.0 })
        }
        "reset" => {
            let w = word(&tx)?;
            store::reset_word(&tx, w.id)?;
            serde_json::json!({ "word": w.id.0 })
        }
        "postpone" => {
            let w = word(&tx)?;
            store::postpone_word(&tx, w.id, ts)?;
            serde_json::json!({ "word": w.id.0 })
        }
        "select" => {
            let category = v["category"].as_str().context("missing .category")?;
            let selected = v["selected"].as_bool().unwrap_or(false);
            store::set_selected(&tx, category, selected)?;
            serde_json::json!({ "category": category, "selected": selected })
        }
        _ => anyhow::bail!(
            "bad .op '{op}'; want grade|triage|answer|add|enroll|remove|reset|postpone|select|setting|restore|goal|raise_goal"
        ),
    };
    tx.commit()?;
    Ok(detail)
}
fn shelve_missing(
    data: &std::path::Path,
    app_id: &str,
    reason: &str,
    word_query: &str,
) -> Result<Receipt> {
    let (ts, _) = now_local();
    crate::oplog::shelve_orphan(data, ts, app_id, reason, None, Some(word_query.to_string()))?;
    Ok(Receipt {
        applied: false,
        orphaned: true,
        snapshot: None,
        detail: serde_json::json!({ "word": word_query }),
    })
}
fn do_grade(
    app: &discover::App,
    cache: &std::path::Path,
    data: &std::path::Path,
    word_query: &str,
    mode: crate::model::CardMode,
    ok: bool,
    at: (i64, &str),
) -> Result<Receipt> {
    let probe = store::open_ro(&app.backup_path)?;
    if store::get_word(&probe, word_query)?.is_none() {
        return shelve_missing(data, &app.id, "grade on missing word", word_query);
    }
    drop(probe);
    let (ts, date) = at;
    let snap = match store::modify(app, cache, data, Some(ts), |tx| {
        let w =
            store::get_word(tx, word_query)?.with_context(|| format!("no word '{word_query}'"))?;
        let (pre_e, pre_f) = store::grade_review(tx, w.id, mode, ok, ts, date)?;
        Ok(vec![OpKind::Graded {
            id: w.id.0,
            mode: mode.value(),
            ok,
            pre_e,
            pre_f,
        }])
    }) {
        Ok(s) => s,
        Err(e) => {
            let probe2 = store::open_ro(&app.backup_path)?;
            if store::get_word(&probe2, word_query)?.is_none() {
                return shelve_missing(data, &app.id, "grade on missing word", word_query);
            }
            return Err(e);
        }
    };
    Ok(Receipt {
        applied: true,
        orphaned: false,
        snapshot: Some(snap),
        detail: serde_json::json!({ "word": word_query, "ok": ok }),
    })
}
fn do_triage(
    app: &discover::App,
    cache: &std::path::Path,
    data: &std::path::Path,
    word_query: &str,
    known: bool,
    at: (i64, &str),
) -> Result<Receipt> {
    let probe = store::open_ro(&app.backup_path)?;
    if store::get_word(&probe, word_query)?.is_none() {
        return shelve_missing(data, &app.id, "triage on missing word", word_query);
    }
    drop(probe);
    let (ts, date) = at;
    let snap = match store::modify(app, cache, data, Some(ts), |tx| {
        let w =
            store::get_word(tx, word_query)?.with_context(|| format!("no word '{word_query}'"))?;
        let op = if known {
            store::triage_known(tx, w.id, ts, date)?;
            OpKind::Triaged {
                id: w.id.0,
                decision: "known".to_string(),
            }
        } else {
            match store::advance_learn(tx, w.id, ts, date)? {
                Some(op) => op,
                None => return Ok(vec![]),
            }
        };
        Ok(vec![op])
    }) {
        Ok(s) => s,
        Err(e) => {
            let probe2 = store::open_ro(&app.backup_path)?;
            if store::get_word(&probe2, word_query)?.is_none() {
                return shelve_missing(data, &app.id, "triage on missing word", word_query);
            }
            return Err(e);
        }
    };
    Ok(Receipt {
        applied: true,
        orphaned: false,
        snapshot: Some(snap),
        detail: serde_json::json!({ "word": word_query, "known": known }),
    })
}
fn do_answer(
    app: &discover::App,
    cache: &std::path::Path,
    data: &std::path::Path,
    word_query: &str,
    mode: crate::model::CardMode,
    positive: bool,
    at: (i64, &str),
) -> Result<Receipt> {
    use crate::sched::Action;
    let probe = store::open_ro(&app.backup_path)?;
    if store::get_word(&probe, word_query)?.is_none() {
        return shelve_missing(data, &app.id, "answer on missing word", word_query);
    }
    drop(probe);
    let side = store::side_of(mode)?;
    let (ts, date) = at;
    let done = std::cell::Cell::new(None);
    let snap = match store::modify(app, cache, data, Some(ts), |tx| {
        let w =
            store::get_word(tx, word_query)?.with_context(|| format!("no word '{word_query}'"))?;
        let action = Action::of(store::sched_row(tx, w.id)?.queue(side), positive);
        done.set(Some(action));
        let op = match action {
            Action::ReviewOk | Action::ReviewFail => {
                let ok = action == Action::ReviewOk;
                let (pre_e, pre_f) = store::grade_review(tx, w.id, mode, ok, ts, date)?;
                OpKind::Graded {
                    id: w.id.0,
                    mode: mode.value(),
                    ok,
                    pre_e,
                    pre_f,
                }
            }
            Action::AlreadyKnown | Action::StartLearning => {
                let known = action == Action::AlreadyKnown;
                if known {
                    store::triage_known(tx, w.id, ts, date)?;
                } else {
                    store::start_learning(tx, w.id, ts, date)?;
                }
                OpKind::Triaged {
                    id: w.id.0,
                    decision: if known { "known" } else { "learn" }.to_string(),
                }
            }
            Action::Memorized | Action::KeepShowing => {
                store::answer(tx, w.id, side, action, ts, date)?;
                OpKind::Learned {
                    id: w.id.0,
                    mode: mode.value(),
                    decision: if action == Action::Memorized {
                        "memorized"
                    } else {
                        "keep"
                    }
                    .to_string(),
                }
            }
        };
        Ok(vec![op])
    }) {
        Ok(s) => s,
        Err(e) => {
            let probe2 = store::open_ro(&app.backup_path)?;
            if store::get_word(&probe2, word_query)?.is_none() {
                return shelve_missing(data, &app.id, "answer on missing word", word_query);
            }
            return Err(e);
        }
    };
    Ok(Receipt {
        applied: true,
        orphaned: false,
        snapshot: Some(snap),
        detail: serde_json::json!({
            "word": word_query,
            "action": done.get().map(Action::name),
        }),
    })
}
fn do_remove(
    app: &discover::App,
    cache: &std::path::Path,
    data: &std::path::Path,
    word_query: &str,
) -> Result<Receipt> {
    let probe = store::open_ro(&app.backup_path)?;
    if store::get_word(&probe, word_query)?.is_none() {
        return shelve_missing(data, &app.id, "remove on missing word", word_query);
    }
    drop(probe);
    let (ts, _) = now_local();
    let snap = match store::modify(app, cache, data, Some(ts), |tx| {
        let w =
            store::get_word(tx, word_query)?.with_context(|| format!("no word '{word_query}'"))?;
        let text = store::remove_word(tx, w.id)?;
        Ok(vec![OpKind::Removed { id: w.id.0, text }])
    }) {
        Ok(s) => s,
        Err(e) => {
            let probe2 = store::open_ro(&app.backup_path)?;
            if store::get_word(&probe2, word_query)?.is_none() {
                return shelve_missing(data, &app.id, "remove on missing word", word_query);
            }
            return Err(e);
        }
    };
    Ok(Receipt {
        applied: true,
        orphaned: false,
        snapshot: Some(snap),
        detail: serde_json::json!({ "word": word_query }),
    })
}
fn print_receipt(action: &str, r: &Receipt, format: Format) {
    match format {
        Format::Json => output::ok_json(
            action,
            serde_json::json!({
                "applied": r.applied, "orphaned": r.orphaned,
                "snapshot": r.snapshot, "detail": r.detail,
            }),
        ),
        Format::Table => {
            if r.orphaned {
                println!("orphaned (word missing, shelved): {}", r.detail["word"]);
            } else {
                println!(
                    "{action} done (snapshot: {})",
                    r.snapshot
                        .as_ref()
                        .map(|p| p.display().to_string())
                        .unwrap_or_default()
                );
            }
        }
    }
}
#[derive(Parser)]
#[command(
    name = "rwcore",
    version,
    about = "ReWord backend for reword-tui (per-app iCloud backups)"
)]
struct Cli {
    #[arg(long, global = true)]
    icloud_root: Option<PathBuf>,
    #[arg(long, global = true)]
    cache_dir: Option<PathBuf>,
    #[arg(long, global = true)]
    data_dir: Option<PathBuf>,
    #[arg(long, global = true, value_enum, default_value = "table")]
    format: Format,
    #[arg(long, global = true)]
    app: Option<String>,
    #[arg(long, global = true)]
    db: Option<PathBuf>,
    #[command(subcommand)]
    cmd: Cmd,
}
#[derive(Subcommand)]
enum Cmd {
    Apps,
    Stats,
    Words {
        #[arg(long)]
        search: Option<String>,
        #[arg(long)]
        category: Option<String>,
        #[arg(long, default_value = "50")]
        limit: i64,
    },
    Show {
        query: String,
    },
    Log {
        word: String,
        #[arg(long, default_value = "50")]
        limit: usize,
    },
    Due {
        #[arg(long, default_value = "50")]
        limit: usize,
    },
    Review {
        word: String,
        #[arg(long)]
        mode: String,
        #[arg(long)]
        result: String,
        #[arg(long)]
        yes: bool,
    },
    Triage {
        word: String,
        #[arg(long)]
        decision: String,
        #[arg(long)]
        yes: bool,
    },
    Categories,
    Catstats,
    Today,
    Select {
        #[arg(long)]
        category: Option<String>,
        #[arg(long)]
        on: bool,
        #[arg(long)]
        off: bool,
        #[arg(long)]
        all: bool,
        #[arg(long)]
        yes: bool,
    },
    AddCategory {
        #[arg(long)]
        id: String,
        #[arg(long)]
        name: String,
        #[arg(long)]
        yes: bool,
    },
    ImportCsv {
        #[arg(long)]
        category: String,
        #[arg(long)]
        file: PathBuf,
        #[arg(long)]
        lang: Option<String>,
        #[arg(long)]
        yes: bool,
    },
    Remove {
        word: String,
        #[arg(long)]
        yes: bool,
    },
    Reset {
        word: String,
        #[arg(long)]
        yes: bool,
    },
    Postpone {
        word: String,
        #[arg(long)]
        yes: bool,
    },
    ClearCategory {
        #[arg(long)]
        category: String,
        #[arg(long)]
        yes: bool,
    },
    RemoveCategory {
        #[arg(long)]
        category: String,
        #[arg(long)]
        yes: bool,
    },
    ResetCategory {
        #[arg(long)]
        category: String,
        #[arg(long)]
        yes: bool,
    },
    Goal {
        #[arg(long)]
        set: Option<i64>,
    },
    Add {
        #[arg(long)]
        word: String,
        #[arg(long)]
        transcription: Option<String>,
        #[arg(long = "tr")]
        tr: Vec<String>,
        #[arg(long)]
        enroll: bool,
        #[arg(long)]
        yes: bool,
    },
    Snapshot,
    Next {
        #[arg(long, default_value = "smart")]
        session: String,
        #[arg(long)]
        exclude: Option<i64>,
        #[arg(long)]
        seed: Option<u64>,
    },
    Work {
        #[arg(long)]
        out: PathBuf,
    },
    Settings,
    Card {
        #[arg(long)]
        word: i64,
        #[arg(long)]
        side: i64,
        #[arg(long, value_delimiter = ',')]
        variants: Vec<i64>,
    },
    Check {
        #[arg(long)]
        typed: String,
        #[arg(long)]
        word: Option<i64>,
        #[arg(long)]
        side: Option<String>,
        #[arg(long)]
        expected: Option<String>,
        #[arg(long)]
        lang: Option<String>,
    },
    Oplog {
        #[arg(long, default_value = "50")]
        limit: usize,
    },
    Replay {
        #[arg(long)]
        apply: bool,
        #[arg(long)]
        yes: bool,
    },
    Orphans,
    Status,
    Pull,
    Install {
        #[arg(long)]
        to: Option<PathBuf>,
        #[arg(long)]
        copy: bool,
    },
    Uninstall {
        #[arg(long)]
        from: Option<PathBuf>,
    },
    Apply,
}
fn icloud_root(cli: &Cli) -> Result<PathBuf> {
    cli.icloud_root
        .clone()
        .map(Ok)
        .unwrap_or_else(discover::default_icloud_root)
}
fn cache_dir(cli: &Cli) -> Result<PathBuf> {
    cli.cache_dir
        .clone()
        .map(Ok)
        .unwrap_or_else(discover::default_cache_dir)
}
fn data_dir(cli: &Cli) -> Result<PathBuf> {
    cli.data_dir
        .clone()
        .map(Ok)
        .unwrap_or_else(discover::default_data_dir)
}
fn resolve_app(cli: &Cli, root: &std::path::Path) -> Result<discover::App> {
    if let Some(sel) = &cli.app {
        return discover::resolve(root, sel);
    }
    anyhow::bail!("no app selected; run `apps`, then pass `--app <id|N>` explicitly")
}
fn parse_tr(items: &[String]) -> Result<Vec<(Lang, String)>> {
    items
        .iter()
        .map(|s| {
            let (k, v) = s
                .split_once('=')
                .with_context(|| format!("bad --tr '{s}', want LANG=TEXT"))?;
            let lang = Lang::parse(k)
                .with_context(|| format!("bad lang '{k}'; known: {}", Lang::known_codes()))?;
            Ok((lang, v.to_string()))
        })
        .collect()
}
fn parse_result(s: &str) -> Result<bool> {
    match s.to_lowercase().as_str() {
        "ok" | "remembered" | "correct" | "got-it" | "gotit" => Ok(true),
        "fail" | "forgotten" | "missed" | "missed-it" | "missedit" => Ok(false),
        _ => anyhow::bail!("bad result '{s}'; want ok|fail"),
    }
}
fn parse_mode(s: &str) -> Result<crate::model::CardMode> {
    use crate::model::CardMode;
    match s.to_lowercase().as_str() {
        "rec" | "recognition" | "1" => Ok(CardMode::Recognition),
        "rep" | "reproduction" | "2" => Ok(CardMode::Reproduction),
        _ => anyhow::bail!("bad --mode '{s}'; want rec|rep"),
    }
}
fn now_epoch() -> Result<i64> {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .context("system clock before epoch")
}
fn default_bin_dir() -> Result<PathBuf> {
    let home = dirs::home_dir().context("cannot determine home dir")?;
    Ok(home.join(".local/bin"))
}
struct ReplayReport {
    outcomes: Vec<(u64, String, String)>,
    orphaned: Vec<(crate::oplog::Op, String)>,
}
fn replay_run(
    conn: &rusqlite::Connection,
    app_id: &str,
    ops: &[crate::oplog::Op],
) -> Result<ReplayReport> {
    let mut outcomes = Vec::new();
    let mut orphaned = Vec::new();
    for o in ops.iter().filter(|o| o.app == app_id) {
        let date = chrono::DateTime::from_timestamp(o.ts, 0)
            .context("bad op ts")?
            .with_timezone(&chrono::Local)
            .format("%F")
            .to_string();
        let tx: &rusqlite::Connection = conn;
        match crate::oplog::replay_decision(conn, app_id, o)? {
            crate::oplog::ReplayAction::ApplyAdd {
                id,
                text,
                transcription,
                tr,
                ex,
                category,
            } => {
                store::readd_word(tx, id, &text, transcription.as_deref(), &tr, &ex, &category)?;
                outcomes.push((o.seq, "apply-add".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyGrade { id, mode } => {
                let ok = matches!(o.kind, crate::oplog::OpKind::Graded { ok: true, .. });
                store::grade_review(tx, id, mode, ok, o.ts, &date)?;
                outcomes.push((o.seq, "apply-grade".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyTriage { id, known } => {
                if known {
                    store::triage_known(tx, id, o.ts, &date)?;
                } else {
                    store::start_learning(tx, id, o.ts, &date)?;
                }
                outcomes.push((o.seq, "apply-triage".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyEnroll { id } => {
                store::enroll_word(tx, id, o.ts, &date)?;
                outcomes.push((o.seq, "apply-enroll".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyLearned { id, mode, keep } => {
                let action = if keep {
                    crate::sched::Action::KeepShowing
                } else {
                    crate::sched::Action::Memorized
                };
                store::answer(tx, id, store::side_of(mode)?, action, o.ts, &date)?;
                outcomes.push((o.seq, "apply-learn".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplySelect { category, selected } => {
                store::set_selected(tx, &category, selected)?;
                outcomes.push((o.seq, "apply-select".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyCategory { id, name } => {
                store::add_category(tx, &id, &name)?;
                outcomes.push((o.seq, "apply-category".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyRemove { id } => {
                store::remove_word(tx, id)?;
                outcomes.push((o.seq, "apply-remove".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyReset { id } => {
                store::reset_word(tx, id)?;
                outcomes.push((o.seq, "apply-reset".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyPostpone { id } => {
                store::postpone_word(tx, id, o.ts)?;
                outcomes.push((o.seq, "apply-postpone".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyGoal { date, goal } => {
                store::set_goal(tx, &date, goal)?;
                outcomes.push((o.seq, "apply-goal".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplySetting { name, value } => {
                store::set_setting(tx, &name, &value)?;
                outcomes.push((o.seq, "apply-setting".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyRestore { id } => {
                if let crate::oplog::OpKind::Restored { row, at, .. } = &o.kind {
                    store::restore_answer(tx, id, row, *at)?;
                }
                outcomes.push((o.seq, "apply-restore".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyGoalRaise { date, adjusted } => {
                store::set_adjusted_goal(tx, &date, adjusted)?;
                outcomes.push((o.seq, "apply-goal-raise".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::ApplyCategoryAdmin { category, action } => {
                match action.as_str() {
                    "clear" => {
                        store::clear_category(tx, &category)?;
                    }
                    "remove" => {
                        store::remove_category(tx, &category)?;
                    }
                    "reset" => {
                        store::reset_category(tx, &category)?;
                    }
                    other => {
                        anyhow::bail!("unknown category action '{other}'");
                    }
                }
                outcomes.push((o.seq, "apply-catadmin".to_string(), String::new()));
            }
            crate::oplog::ReplayAction::Orphan(reason) => {
                orphaned.push((o.clone(), reason.clone()));
                outcomes.push((o.seq, "orphan".to_string(), reason));
            }
            crate::oplog::ReplayAction::Skip(reason) => {
                outcomes.push((o.seq, "skip".to_string(), reason));
            }
        }
    }
    Ok(ReplayReport { outcomes, orphaned })
}

fn main() -> Result<()> {
    let cli = Cli::parse();
    match &cli.cmd {
        Cmd::Apps => {
            let root = icloud_root(&cli)?;
            let apps = discover::discover(&root)?;
            let mut with_meta = Vec::new();
            for a in apps {
                match discover::file_meta(&a.backup_path) {
                    Ok(m) => with_meta.push((a, m)),
                    Err(e) => eprintln!("warning: skipping {}: {e:#}", a.backup_path.display()),
                }
            }
            output::apps(&with_meta, cli.format)?;
        }
        Cmd::Stats => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            output::stats(&app.id, &store::stats(&conn)?, cli.format)?;
        }
        Cmd::Words {
            search,
            category,
            limit,
        } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let list = store::list_words(
                &conn,
                &store::WordFilter {
                    search: search.as_deref(),
                    category: category.as_deref(),
                    limit: *limit,
                },
            )?;
            output::words(&list, cli.format)?;
        }
        Cmd::Show { query } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            match store::get_word(&conn, query)? {
                Some(w) => output::word(&w, cli.format)?,
                None => anyhow::bail!("no word '{query}'"),
            }
        }
        Cmd::Log { word, limit } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let w = store::get_word(&conn, word)?.with_context(|| format!("no word '{word}'"))?;
            let entries = store::log_entries(&conn, w.id, *limit)?;
            output::log_entries(&entries, cli.format)?;
        }
        Cmd::Due { limit } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let now = now_epoch()?;
            let mut due = store::due_words(&conn, now)?;
            due.truncate(*limit);
            output::due(&due, now, cli.format)?;
        }
        Cmd::Review {
            word,
            mode,
            result,
            yes,
        } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let ok = parse_result(result)?;
            let mode = parse_mode(mode)?;
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let (ts, date) = now_local();
            let r = do_grade(&app, &cache, &data, word, mode, ok, (ts, &date))?;
            print_receipt("graded", &r, cli.format);
        }
        Cmd::Triage {
            word,
            decision,
            yes,
        } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let known = match decision.to_lowercase().as_str() {
                "known" | "know" | "already-known" | "alreadyknown" => true,
                "learn" | "start-learning" | "startlearning" => false,
                _ => anyhow::bail!("bad --decision '{decision}'; want known|learn"),
            };
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let (ts, date) = now_local();
            let r = do_triage(&app, &cache, &data, word, known, (ts, &date))?;
            print_receipt("triaged", &r, cli.format);
        }
        Cmd::Categories => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            output::categories(&store::categories(&conn)?, cli.format)?;
        }
        Cmd::Catstats => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            output::category_stats(&store::category_stats(&conn)?, cli.format)?;
        }
        Cmd::Today => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let today = chrono::Local::now().format("%F").to_string();
            output::today(&store::today(&conn, &today)?, cli.format)?;
        }
        Cmd::Select {
            category,
            on,
            off,
            all,
            yes,
        } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            if *on == *off {
                anyhow::bail!("pass exactly one of --on / --off");
            }
            let selected = *on;
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let snap = store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                if *all {
                    let cats = store::categories(tx)?;
                    let mut ops = Vec::with_capacity(cats.len());
                    for c in &cats {
                        if c.selected != selected {
                            store::set_selected(tx, &c.id, selected)?;
                            ops.push(OpKind::Selected {
                                category: c.id.clone(),
                                selected,
                            });
                        }
                    }
                    Ok(ops)
                } else {
                    let category = category
                        .as_deref()
                        .context("missing --category (or pass --all)")?;
                    let id = store::set_selected(tx, category, selected)?;
                    Ok(vec![OpKind::Selected {
                        category: id,
                        selected,
                    }])
                }
            })?;
            match cli.format {
                Format::Json => output::ok_json(
                    "selected",
                    serde_json::json!({ "selected": selected, "snapshot": snap }),
                ),
                Format::Table => println!("selected={selected} (snapshot: {})", snap.display()),
            }
        }
        Cmd::AddCategory { id, name, yes } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let idc = id.clone();
            let namec = name.clone();
            let snap = store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                store::add_category(tx, &idc, &namec)?;
                Ok(vec![OpKind::CategoryAdded {
                    id: idc.clone(),
                    name: namec.clone(),
                }])
            })?;
            match cli.format {
                Format::Json => output::ok_json(
                    "category added",
                    serde_json::json!({ "id": id, "snapshot": snap }),
                ),
                Format::Table => println!("added category '{id}' (snapshot: {})", snap.display()),
            }
        }
        Cmd::ImportCsv {
            category,
            file,
            lang,
            yes,
        } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let text = std::fs::read_to_string(file)
                .with_context(|| "Could not load words from the selected file")?;
            let rows = store::parse_csv(&text);
            if rows.is_empty() {
                anyhow::bail!("Could not load words from the selected file");
            }
            let probe = store::open_ro(&app.backup_path)?;
            let tl = match lang.as_deref() {
                Some(l) => Lang::parse(l)
                    .with_context(|| format!("bad lang '{l}'; known: {}", Lang::known_codes()))?,
                None => {
                    let st = store::settings(&probe)?;
                    st.native_language
                        .as_deref()
                        .and_then(Lang::parse)
                        .unwrap_or(Lang::Eng)
                }
            };
            drop(probe);
            let catc = category.clone();
            let snap = store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                let since: i64 = tx
                    .query_row("SELECT COALESCE(MAX(ID), 0) FROM WORD", [], |r| r.get(0))
                    .context("read max id")?;
                let _ = store::import_rows(tx, &catc, &rows, &tl)?;
                let mut ops = Vec::new();
                let mut st = tx
                    .prepare("SELECT ID FROM WORD WHERE ID > ? ORDER BY ID")
                    .context("read back import")?;
                let ids: Vec<i64> = st
                    .query_map([since], |r| r.get(0))?
                    .collect::<std::result::Result<Vec<_>, _>>()
                    .context("read back import")?;
                for id in ids {
                    let (text, transcription, tr, ex) =
                        store::word_payload(tx, crate::model::WordId(id))?;
                    ops.push(OpKind::Added {
                        id,
                        text,
                        transcription,
                        tr,
                        ex,
                        category: catc.clone(),
                    });
                }
                Ok(ops)
            })?;
            match cli.format {
                Format::Json => output::ok_json(
                    "The words have been successfully imported",
                    serde_json::json!({ "category": category, "snapshot": snap }),
                ),
                Format::Table => {
                    println!(
                        "The words have been successfully imported (snapshot: {})",
                        snap.display()
                    )
                }
            }
        }
        Cmd::Remove { word, yes } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let qc = word.clone();
            let r = do_remove(&app, &cache, &data, &qc)?;
            print_receipt("removed", &r, cli.format);
        }
        Cmd::Reset { word, yes } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let qc = word.clone();
            let snap = match store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                let w = store::get_word(tx, &qc)?.with_context(|| format!("no word '{qc}'"))?;
                store::reset_word(tx, w.id)?;
                Ok(vec![OpKind::Reset { id: w.id.0 }])
            }) {
                Ok(s) => s,
                Err(e) => {
                    let probe = store::open_ro(&app.backup_path)?;
                    if store::get_word(&probe, &qc)?.is_none() {
                        let r = shelve_missing(
                            data_dir(&cli)?.as_path(),
                            &app.id,
                            "reset on missing word",
                            &qc,
                        )?;
                        print_receipt("reset", &r, cli.format);
                        return Ok(());
                    }
                    return Err(e);
                }
            };
            match cli.format {
                Format::Json => output::ok_json(
                    "Progress has been reset",
                    serde_json::json!({ "word": word, "snapshot": snap }),
                ),
                Format::Table => {
                    println!("Progress has been reset (snapshot: {})", snap.display())
                }
            }
        }
        Cmd::Postpone { word, yes } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let qc = word.clone();
            let (pts, _) = now_local();
            let snap = match store::modify(&app, &cache, &data, Some(pts), |tx| {
                let w = store::get_word(tx, &qc)?.with_context(|| format!("no word '{qc}'"))?;
                store::postpone_word(tx, w.id, pts)?;
                Ok(vec![OpKind::Postponed { id: w.id.0 }])
            }) {
                Ok(s) => s,
                Err(e) => {
                    let probe = store::open_ro(&app.backup_path)?;
                    if store::get_word(&probe, &qc)?.is_none() {
                        let r = shelve_missing(
                            data_dir(&cli)?.as_path(),
                            &app.id,
                            "postpone on missing word",
                            &qc,
                        )?;
                        print_receipt("postponed", &r, cli.format);
                        return Ok(());
                    }
                    return Err(e);
                }
            };
            match cli.format {
                Format::Json => output::ok_json(
                    "postponed",
                    serde_json::json!({ "word": word, "snapshot": snap }),
                ),
                Format::Table => {
                    println!("postponed '{word}' (snapshot: {})", snap.display())
                }
            }
        }
        Cmd::ClearCategory { category, yes } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let cc = category.clone();
            let snap = store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                store::clear_category(tx, &cc)?;
                Ok(vec![OpKind::CategoryAdmin {
                    category: cc.clone(),
                    action: "clear".to_string(),
                }])
            })?;
            match cli.format {
                Format::Json => output::ok_json(
                    "Words have been removed",
                    serde_json::json!({ "category": category, "snapshot": snap }),
                ),
                Format::Table => {
                    println!("Words have been removed (snapshot: {})", snap.display())
                }
            }
        }
        Cmd::RemoveCategory { category, yes } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let cc = category.clone();
            let snap = store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                store::remove_category(tx, &cc)?;
                Ok(vec![OpKind::CategoryAdmin {
                    category: cc.clone(),
                    action: "remove".to_string(),
                }])
            })?;
            match cli.format {
                Format::Json => output::ok_json(
                    "Category has been removed",
                    serde_json::json!({ "category": category, "snapshot": snap }),
                ),
                Format::Table => {
                    println!("Category has been removed (snapshot: {})", snap.display())
                }
            }
        }
        Cmd::ResetCategory { category, yes } => {
            if !yes {
                anyhow::bail!("refusing to write without --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let cc = category.clone();
            let snap = store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                store::reset_category(tx, &cc)?;
                Ok(vec![OpKind::CategoryAdmin {
                    category: cc.clone(),
                    action: "reset".to_string(),
                }])
            })?;
            match cli.format {
                Format::Json => output::ok_json(
                    "Progress has been reset",
                    serde_json::json!({ "category": category, "snapshot": snap }),
                ),
                Format::Table => {
                    println!("Progress has been reset (snapshot: {})", snap.display())
                }
            }
        }
        Cmd::Goal { set } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let key = chrono::Local::now().format("%F").to_string();
            if let Some(g) = set {
                let cache = cache_dir(&cli)?;
                let data = data_dir(&cli)?;
                let keyc = key.clone();
                let snap = store::modify(&app, &cache, &data, Some(now_local().0), |tx| {
                    store::set_goal(tx, &keyc, *g)?;
                    Ok(vec![OpKind::GoalSet {
                        date: keyc.clone(),
                        goal: *g,
                    }])
                })?;
                match cli.format {
                    Format::Json => output::ok_json(
                        "goal set",
                        serde_json::json!({ "goal": g, "snapshot": snap }),
                    ),
                    Format::Table => println!("daily goal: {g}"),
                }
            } else {
                output::goal(&store::get_goal(&conn, &key)?, cli.format)?;
            }
        }
        Cmd::Add {
            word,
            transcription,
            tr,
            enroll,
            yes,
        } => {
            if !yes {
                anyhow::bail!(
                    "refusing to write without --yes (snapshot + single-writer discipline, see README)"
                );
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let tr = parse_tr(tr)?;
            if word.trim().is_empty() {
                anyhow::bail!("--word must not be empty");
            }
            if tr.is_empty() {
                anyhow::bail!("give at least one --tr LANG=TEXT");
            }
            if tr.iter().any(|(_, t)| t.trim().is_empty()) {
                anyhow::bail!("--tr TEXT must not be empty");
            }
            let data = data_dir(&cli)?;
            let snap = store::modify(&app, &cache, &data, Some(now_local().0), |conn| {
                let id = store::add_word(conn, word, transcription.as_deref(), &tr)?;
                let cat = store::custom_category(conn)?;
                let mut ops = vec![OpKind::Added {
                    id: id.0,
                    text: (*word).to_string(),
                    transcription: transcription.clone(),
                    tr: tr
                        .iter()
                        .map(|(l, t)| (l.code().to_string(), t.clone()))
                        .collect(),
                    ex: vec![],
                    category: cat,
                }];
                if *enroll {
                    let now = chrono::Local::now();
                    store::enroll_word(conn, id, now.timestamp(), &now.format("%F").to_string())?;
                    ops.push(OpKind::Enrolled { id: id.0 });
                }
                eprintln!("inserted word id {id}");
                Ok(ops)
            })?;
            match cli.format {
                Format::Json => output::ok_json(
                    "word added",
                    serde_json::json!({ "word": word, "snapshot": snap }),
                ),
                Format::Table => {
                    println!("added '{word}' (snapshot: {})", snap.display());
                    println!(
                        "remember: iPhone picks this up only via manual Restore (full overwrite)."
                    );
                }
            }
        }
        Cmd::Status => {
            let root = icloud_root(&cli)?;
            let data = data_dir(&cli)?;
            let apps = match &cli.app {
                Some(sel) => vec![discover::resolve(&root, sel)?],
                None => discover::discover(&root)?,
            };
            let mut rows = Vec::new();
            for app in &apps {
                let current = sync::fingerprint(app)?;
                let state = match sync::stored(&data, &app.id) {
                    None => "untracked",
                    Some(want) if want == current => "clean",
                    Some(_) => "dirty",
                };
                rows.push(serde_json::json!({
                    "app": app.id, "state": state,
                    "current": current, "backup": app.backup_path,
                }));
            }
            match cli.format {
                Format::Json => println!("{}", serde_json::to_string_pretty(&rows)?),
                Format::Table => {
                    let (h0, h1, h2) = ("APP", "STATE", "BACKUP");
                    println!("{h0:<6} {h1:<9} {h2}");
                    for r in &rows {
                        println!(
                            "{:<6} {:<9} {}",
                            r["app"].as_str().unwrap_or("?"),
                            r["state"].as_str().unwrap_or("?"),
                            r["backup"].as_str().unwrap_or("?")
                        );
                    }
                }
            }
        }
        Cmd::Pull => {
            let root = icloud_root(&cli)?;
            let data = data_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let q = sync::pull(&data, &app)?;
            match cli.format {
                Format::Json => output::ok_json(
                    "pulled",
                    serde_json::json!({ "app": app.id, "quarantine": q }),
                ),
                Format::Table => println!("adopted {} (quarantine: {})", app.id, q.display()),
            }
        }
        Cmd::Oplog { limit } => {
            let data = data_dir(&cli)?;
            let ops = crate::oplog::tail(&data, *limit);
            match cli.format {
                Format::Json => println!("{}", serde_json::to_string_pretty(&ops)?),
                Format::Table => {
                    let (h0, h1, h2, h3) = ("SEQ", "TS", "APP", "KIND");
                    println!("{h0:<6} {h1:<10} {h2:<4} {h3}");
                    for o in &ops {
                        println!(
                            "{:<6} {:<10} {:<4} {}",
                            o.seq,
                            o.ts,
                            o.app,
                            serde_json::to_string(&o.kind).unwrap_or_default()
                        );
                    }
                }
            }
        }
        Cmd::Orphans => {
            let data = data_dir(&cli)?;
            let list = crate::oplog::orphans(&data);
            match cli.format {
                Format::Json => println!("{}", serde_json::to_string_pretty(&list)?),
                Format::Table => {
                    for o in &list {
                        println!(
                            "{} {} {}",
                            o.ts,
                            o.app,
                            o.word_query.as_deref().unwrap_or("?")
                        );
                    }
                }
            }
        }
        Cmd::Replay { apply, yes } => {
            if *apply && !yes {
                anyhow::bail!("replay --apply requires --yes");
            }
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let data = data_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let ops = crate::oplog::tail(&data, 100000);
            if !apply {
                let sim = std::env::temp_dir().join(format!(
                    "rwcore-replay-sim-{}-{}.db",
                    std::process::id(),
                    std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .map(|d| d.as_nanos())
                        .unwrap_or(0)
                ));
                store::retry(
                    || {
                        std::fs::copy(&app.backup_path, &sim)
                            .with_context(|| "cannot stage replay sim")
                    },
                    "stage replay sim",
                )?;
                let sim_conn = store::retry(
                    || rusqlite::Connection::open(&sim).with_context(|| "cannot open replay sim"),
                    "open replay sim",
                )?;
                let rep = replay_run(&sim_conn, &app.id, &ops);
                drop(sim_conn);
                std::fs::remove_file(&sim).ok();
                let rep = rep?;
                let mut kinds = std::collections::HashMap::new();
                for o in ops.iter().filter(|o| o.app == app.id) {
                    kinds.insert(o.seq, serde_json::to_string(&o.kind).unwrap_or_default());
                }
                match cli.format {
                    Format::Json => println!(
                        "{}",
                        serde_json::to_string_pretty(
                            &rep.outcomes
                                .iter()
                                .map(|(s, a, r)| serde_json::json!({
                                    "seq": s,
                                    "op": kinds.get(s).cloned().unwrap_or_default(),
                                    "action": format!("{a} {r}").trim(),
                                }))
                                .collect::<Vec<_>>()
                        )?
                    ),
                    Format::Table => {
                        for (s, a, r) in &rep.outcomes {
                            println!(
                                "{s:<6} {:<40} {a} {r}",
                                kinds.get(s).cloned().unwrap_or_default()
                            );
                        }
                    }
                }
                return Ok(());
            }
            let mut applied = 0u64;
            let mut skipped = 0u64;
            let mut outcomes: Vec<(u64, String, String)> = Vec::new();
            let mut orphaned: Vec<(crate::oplog::Op, String)> = Vec::new();
            store::modify(&app, &cache, &data, None, |tx| {
                let rep = replay_run(tx, &app.id, &ops)?;
                outcomes = rep.outcomes;
                orphaned = rep.orphaned;
                Ok(vec![])
            })?;
            for (o, reason) in &orphaned {
                crate::oplog::shelve_orphan(
                    &data,
                    now_epoch()?,
                    &app.id,
                    &format!("replay orphan: {reason}"),
                    Some(o.kind.clone()),
                    None,
                )?;
            }
            for (seq, action, reason) in &outcomes {
                match action.as_str() {
                    "apply-add" | "apply-grade" | "apply-triage" | "apply-enroll"
                    | "apply-learn" | "apply-select" | "apply-category" | "apply-remove"
                    | "apply-reset" | "apply-catadmin" | "apply-postpone" | "apply-goal" => {
                        applied += 1
                    }
                    "orphan" => {}
                    _ => skipped += 1,
                }
                let _ = (seq, reason);
            }
            match cli.format {
                Format::Json => output::ok_json(
                    "replay applied",
                    serde_json::json!({ "applied": applied, "skipped": skipped, "orphaned": orphaned.len() }),
                ),
                Format::Table => println!(
                    "applied={applied} skipped={skipped} orphaned={}",
                    orphaned.len()
                ),
            }
        }
        Cmd::Apply => {
            use std::io::Read as _;
            let mut buf = String::new();
            std::io::stdin()
                .read_to_string(&mut buf)
                .context("cannot read stdin")?;
            let v: serde_json::Value = serde_json::from_str(&buf).context("stdin is not JSON")?;
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let op = v["op"].as_str().unwrap_or("");
            let (ts, date) = body_time(&v)?;
            if let Some(db) = &cli.db {
                let detail = apply_local(db, &v, ts, &date)?;
                let r = Receipt {
                    applied: true,
                    orphaned: false,
                    snapshot: None,
                    detail,
                };
                print_receipt("applied", &r, cli.format);
                return Ok(());
            }
            let r = match op {
                "grade" => {
                    let mode = parse_mode(v["mode"].as_str().unwrap_or(""))?;
                    let ok = parse_result(v["result"].as_str().unwrap_or(""))?;
                    let word = v["word"].as_str().context("missing .word")?;
                    do_grade(&app, &cache, &data, word, mode, ok, (ts, &date))?
                }
                "triage" => {
                    let known = match v["decision"].as_str().unwrap_or("") {
                        "known" => true,
                        "learn" => false,
                        x => anyhow::bail!("bad decision '{x}'; want known|learn"),
                    };
                    let word = v["word"].as_str().context("missing .word")?;
                    do_triage(&app, &cache, &data, word, known, (ts, &date))?
                }
                "answer" => {
                    let mode = parse_mode(v["mode"].as_str().unwrap_or(""))?;
                    let positive = v["positive"].as_bool().context("missing .positive")?;
                    let word = v["word"].as_str().context("missing .word")?;
                    do_answer(&app, &cache, &data, word, mode, positive, (ts, &date))?
                }
                "add" => {
                    let word = v["word"].as_str().context("missing .word")?;
                    if word.trim().is_empty() {
                        anyhow::bail!("missing .word");
                    }
                    let trs = v["tr"]
                        .as_array()
                        .context("missing .tr array")?
                        .iter()
                        .map(|t| t.as_str().unwrap_or("").to_string())
                        .collect::<Vec<_>>();
                    let tr = parse_tr(&trs)?;
                    if tr.is_empty() || tr.iter().any(|(_, t)| t.trim().is_empty()) {
                        anyhow::bail!("bad .tr entries; want LANG=TEXT with non-empty TEXT");
                    }
                    let transcription = v["transcription"].as_str();
                    let enroll = v["enroll"].as_bool().unwrap_or(false);
                    let snap = store::modify(&app, &cache, &data, Some(ts), |tx| {
                        let id = store::add_word(tx, word, transcription, &tr)?;
                        let cat = store::custom_category(tx)?;
                        let mut ops = vec![OpKind::Added {
                            id: id.0,
                            text: word.to_string(),
                            transcription: transcription.map(str::to_string),
                            tr: tr
                                .iter()
                                .map(|(l, t)| (l.code().to_string(), t.clone()))
                                .collect(),
                            ex: vec![],
                            category: cat,
                        }];
                        if enroll {
                            store::enroll_word(tx, id, ts, &date)?;
                            ops.push(OpKind::Enrolled { id: id.0 });
                        }
                        Ok(ops)
                    })?;
                    Receipt {
                        applied: true,
                        orphaned: false,
                        snapshot: Some(snap),
                        detail: serde_json::json!({ "word": word }),
                    }
                }
                "enroll" => {
                    let word = v["word"].as_str().context("missing .word")?;
                    do_triage(&app, &cache, &data, word, false, (ts, &date))?
                }
                "remove" => {
                    let word = v["word"].as_str().context("missing .word")?;
                    do_remove(&app, &cache, &data, word)?
                }
                "reset" => {
                    let word = v["word"].as_str().context("missing .word")?;
                    match store::modify(&app, &cache, &data, Some(ts), |tx| {
                        let w = store::get_word(tx, word)?
                            .with_context(|| format!("no word '{word}'"))?;
                        store::reset_word(tx, w.id)?;
                        Ok(vec![OpKind::Reset { id: w.id.0 }])
                    }) {
                        Ok(snap) => Receipt {
                            applied: true,
                            orphaned: false,
                            snapshot: Some(snap),
                            detail: serde_json::json!({ "word": word }),
                        },
                        Err(e) => {
                            let probe = store::open_ro(&app.backup_path)?;
                            if store::get_word(&probe, word)?.is_none() {
                                let r =
                                    shelve_missing(&data, &app.id, "reset on missing word", word)?;
                                print_receipt("applied", &r, cli.format);
                                return Ok(());
                            }
                            return Err(e);
                        }
                    }
                }
                "postpone" => {
                    let word = v["word"].as_str().context("missing .word")?;
                    match store::modify(&app, &cache, &data, Some(ts), |tx| {
                        let w = store::get_word(tx, word)?
                            .with_context(|| format!("no word '{word}'"))?;
                        store::postpone_word(tx, w.id, ts)?;
                        Ok(vec![OpKind::Postponed { id: w.id.0 }])
                    }) {
                        Ok(snap) => Receipt {
                            applied: true,
                            orphaned: false,
                            snapshot: Some(snap),
                            detail: serde_json::json!({ "word": word }),
                        },
                        Err(e) => {
                            let probe = store::open_ro(&app.backup_path)?;
                            if store::get_word(&probe, word)?.is_none() {
                                let r = shelve_missing(
                                    &data,
                                    &app.id,
                                    "postpone on missing word",
                                    word,
                                )?;
                                print_receipt("applied", &r, cli.format);
                                return Ok(());
                            }
                            return Err(e);
                        }
                    }
                }
                "setting" => {
                    let name = v["name"].as_str().context("missing .name")?;
                    let value = v["value"].as_str().context("missing .value")?;
                    let snap = store::modify(&app, &cache, &data, Some(ts), |tx| {
                        store::set_setting(tx, name, value)?;
                        Ok(vec![OpKind::SettingSet {
                            name: name.to_string(),
                            value: value.to_string(),
                        }])
                    })?;
                    Receipt {
                        applied: true,
                        orphaned: false,
                        snapshot: Some(snap),
                        detail: serde_json::json!({ "name": name, "value": value }),
                    }
                }
                "restore" => {
                    let word = v["word"].as_str().context("missing .word")?;
                    let row: crate::sched::Row =
                        serde_json::from_value(v["row"].clone()).context("bad .row")?;
                    let at = v["at"].as_i64().context("missing .at")?;
                    match store::modify(&app, &cache, &data, Some(ts), |tx| {
                        let w = store::get_word(tx, word)?
                            .with_context(|| format!("no word '{word}'"))?;
                        store::restore_answer(tx, w.id, &row, at)?;
                        Ok(vec![OpKind::Restored {
                            id: w.id.0,
                            row,
                            at,
                        }])
                    }) {
                        Ok(snap) => Receipt {
                            applied: true,
                            orphaned: false,
                            snapshot: Some(snap),
                            detail: serde_json::json!({ "word": word }),
                        },
                        Err(e) => {
                            let probe = store::open_ro(&app.backup_path)?;
                            if store::get_word(&probe, word)?.is_none() {
                                let r = shelve_missing(
                                    &data,
                                    &app.id,
                                    "restore on missing word",
                                    word,
                                )?;
                                print_receipt("applied", &r, cli.format);
                                return Ok(());
                            }
                            return Err(e);
                        }
                    }
                }
                "goal" => {
                    let goal = v["goal"].as_i64().context("missing .goal")?;
                    let snap = store::modify(&app, &cache, &data, Some(ts), |tx| {
                        store::set_goal(tx, &date, goal)?;
                        Ok(vec![OpKind::GoalSet {
                            date: date.clone(),
                            goal,
                        }])
                    })?;
                    Receipt {
                        applied: true,
                        orphaned: false,
                        snapshot: Some(snap),
                        detail: serde_json::json!({ "goal": goal }),
                    }
                }
                "raise_goal" => {
                    let by = v["by"].as_i64().context("missing .by")?;
                    let snap = store::modify(&app, &cache, &data, Some(ts), |tx| {
                        let adjusted = store::raise_goal(tx, &date, by)?;
                        Ok(vec![OpKind::GoalRaised {
                            date: date.clone(),
                            adjusted,
                        }])
                    })?;
                    Receipt {
                        applied: true,
                        orphaned: false,
                        snapshot: Some(snap),
                        detail: serde_json::json!({ "by": by }),
                    }
                }
                _ => anyhow::bail!(
                    "bad .op '{op}'; want grade|triage|answer|add|enroll|remove|reset|postpone|setting|restore|goal|raise_goal"
                ),
            };
            print_receipt("applied", &r, cli.format);
        }
        Cmd::Next {
            session,
            exclude,
            seed,
        } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let session = select::Session::parse(session)?;
            let rules = store::rules(&conn)?;
            let scope = select::Scope::load(&conn)?;
            let now = chrono::Local::now();
            let ts = now.timestamp();
            let today = now.format("%F").to_string();
            let tomorrow = (now.date_naive() + chrono::Days::new(1))
                .format("%F")
                .to_string();
            let day = select::day(&conn, &rules, &scope, ts, &today, &tomorrow)?;
            let seed = seed.unwrap_or_else(|| now.timestamp_nanos_opt().unwrap_or(ts) as u64);
            let mut rng = select::Rng::new(seed);
            let prev = exclude.map(crate::model::WordId);
            let card = select::next(&conn, &rules, &scope, session, ts, &day, prev, &mut rng)?
                .map(|p| select::card(&conn, &rules, &scope, p, &mut rng))
                .transpose()?;
            match cli.format {
                Format::Json => println!(
                    "{}",
                    serde_json::to_string(&serde_json::json!({
                        "card": card,
                        "day": day,
                        "now": ts,
                    }))?
                ),
                Format::Table => match &card {
                    Some(c) => println!("{} side={} source={:?}", c.word.text, c.side, c.source),
                    None => println!("no card; next review {:?}", day.next_review),
                },
            }
        }
        Cmd::Settings => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let s = store::synced_settings(&conn)?;
            match cli.format {
                Format::Json => println!("{}", serde_json::to_string(&s)?),
                Format::Table => {
                    for (k, v) in s.as_object().into_iter().flatten() {
                        println!("{k}: {v}");
                    }
                }
            }
        }
        Cmd::Card {
            word,
            side,
            variants,
        } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = read_db(&cli, &app)?;
            let rules = store::rules(&conn)?;
            let scope = select::Scope::load(&conn)?;
            let now = chrono::Local::now();
            let ts = now.timestamp();
            let today = now.format("%F").to_string();
            let tomorrow = (now.date_naive() + chrono::Days::new(1))
                .format("%F")
                .to_string();
            let day = select::day(&conn, &rules, &scope, ts, &today, &tomorrow)?;
            let side = match *side {
                1 => sched::Side::Rec,
                2 => sched::Side::Rep,
                x => anyhow::bail!("--side must be 1 or 2, got {x}"),
            };
            let id = crate::model::WordId(*word);
            let source = match store::sched_row(&conn, id)?.queue(side) {
                0 => select::Source::New,
                1 => select::Source::Learning,
                _ => select::Source::Review,
            };
            let shown: Vec<crate::model::WordId> =
                variants.iter().copied().map(crate::model::WordId).collect();
            let mut rng = select::Rng::new(now.timestamp_nanos_opt().unwrap_or(ts) as u64);
            let card = select::card_with(
                &conn,
                &rules,
                &scope,
                select::Picked {
                    word: id,
                    side,
                    source,
                },
                &shown,
                &mut rng,
            )?;
            match cli.format {
                Format::Json => println!(
                    "{}",
                    serde_json::to_string(&serde_json::json!({
                        "card": card,
                        "day": day,
                        "now": ts,
                    }))?
                ),
                Format::Table => println!("{} side={}", card.word.text, card.side),
            }
        }
        Cmd::Work { out } => {
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let snap = store::snapshot(&app, &cache)?;
            if let Some(dir) = out.parent() {
                std::fs::create_dir_all(dir)
                    .with_context(|| format!("cannot create {}", dir.display()))?;
            }
            let tmp = out.with_extension("tmp");
            std::fs::copy(&snap, &tmp)
                .with_context(|| format!("cannot copy {} to {}", snap.display(), tmp.display()))?;
            std::fs::rename(&tmp, out)
                .with_context(|| format!("cannot move {} to {}", tmp.display(), out.display()))?;
            match cli.format {
                Format::Json => output::ok_json(
                    "working copy ready",
                    serde_json::json!({ "path": out, "from": snap }),
                ),
                Format::Table => println!("{}", out.display()),
            }
        }
        Cmd::Check {
            typed,
            word,
            side,
            expected,
            lang,
        } => {
            let (expected, lang) = match (word, expected) {
                (Some(id), None) => {
                    let root = icloud_root(&cli)?;
                    let app = resolve_app(&cli, &root)?;
                    let conn = read_db(&cli, &app)?;
                    let (text, _, tr, _) = store::word_payload(&conn, crate::model::WordId(*id))?;
                    match side.as_deref() {
                        Some("rec") => {
                            let scope = select::Scope::load(&conn)?;
                            let t = tr
                                .into_iter()
                                .find(|(l, _)| *l == scope.native)
                                .map(|(_, t)| t)
                                .unwrap_or_default();
                            (t, scope.native.to_lowercase())
                        }
                        Some("rep") => {
                            let lang = matcher::course_lang(&app.id).with_context(|| {
                                format!("no course language for app {}", app.id)
                            })?;
                            (text, lang.to_string())
                        }
                        _ => anyhow::bail!("--word needs --side rec or rep"),
                    }
                }
                (None, Some(e)) => (e.clone(), lang.clone().context("--expected needs --lang")?),
                _ => anyhow::bail!("give --word with --side, or --expected with --lang"),
            };
            let verdict = matcher::check(typed, &expected, &lang)?;
            match cli.format {
                Format::Json => println!(
                    "{}",
                    serde_json::to_string(&serde_json::json!({
                        "verdict": verdict.name(),
                        "accepted": verdict.accepted(),
                        "expected": expected,
                        "lang": lang,
                    }))?
                ),
                Format::Table => println!("{}", verdict.name()),
            }
        }
        Cmd::Snapshot => {
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let apps = match &cli.app {
                Some(sel) => vec![discover::resolve(&root, sel)?],
                None => discover::discover(&root)?,
            };
            let mut snaps = Vec::new();
            let mut errors = Vec::new();
            for app in &apps {
                match store::snapshot(app, &cache) {
                    Ok(snap) => snaps.push(serde_json::json!({ "app": app.id, "snapshot": snap })),
                    Err(e) => {
                        errors
                            .push(serde_json::json!({ "app": app.id, "error": format!("{e:#}") }));
                        if cli.format != Format::Json {
                            eprintln!("{}: failed: {e:#}", app.id);
                        }
                    }
                }
            }
            match cli.format {
                Format::Json => output::ok_json(
                    "snapshot",
                    serde_json::json!({ "snapshots": snaps, "errors": errors }),
                ),
                Format::Table => {
                    for s in &snaps {
                        println!(
                            "{} -> {}",
                            s["app"].as_str().unwrap_or("?"),
                            s["snapshot"].as_str().unwrap_or("?")
                        );
                    }
                }
            }
        }
        Cmd::Install { to, copy } => {
            let dir = to.clone().map(Ok).unwrap_or_else(default_bin_dir)?;
            std::fs::create_dir_all(&dir)
                .with_context(|| format!("cannot create {}", dir.display()))?;
            let dst = dir.join("rwcore");
            let src = std::env::current_exe().context("cannot locate own binary")?;
            if *copy {
                if dst.exists() || dst.is_symlink() {
                    anyhow::bail!(
                        "refusing to copy over {}; uninstall/remove it first",
                        dst.display()
                    );
                }
                std::fs::copy(&src, &dst).context("copy failed")?;
            } else {
                if dst.exists() || dst.is_symlink() {
                    std::fs::remove_file(&dst).ok();
                }
                std::os::unix::fs::symlink(&src, &dst).context("symlink failed")?;
            }
            println!("rwcore exposed at {}", dst.display());
            println!("note: the Go TUI does not need this; it bundles rwcore privately.");
        }
        Cmd::Uninstall { from } => {
            let dir = from.clone().map(Ok).unwrap_or_else(default_bin_dir)?;
            let dst = dir.join("rwcore");
            if dst.is_symlink() {
                std::fs::remove_file(&dst).context("remove failed")?;
                println!("removed {}", dst.display());
            } else if dst.exists() {
                anyhow::bail!(
                    "refusing to delete regular file {}; remove it manually",
                    dst.display()
                );
            } else {
                println!("nothing at {}", dst.display());
            }
        }
    }
    Ok(())
}
