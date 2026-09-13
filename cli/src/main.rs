mod discover;
mod interval;
mod model;
mod oplog;
mod output;
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
fn do_grade(
    app: &discover::App,
    cache: &std::path::Path,
    data: &std::path::Path,
    word_query: &str,
    mode: crate::model::CardMode,
    ok: bool,
) -> Result<Receipt> {
    let probe = store::open_ro(&app.backup_path)?;
    if store::get_word(&probe, word_query)?.is_none() {
        let (ts, _) = now_local();
        crate::oplog::shelve_orphan(
            data,
            ts,
            &app.id,
            "grade on missing word",
            None,
            Some(word_query.to_string()),
        )?;
        return Ok(Receipt {
            applied: false,
            orphaned: true,
            snapshot: None,
            detail: serde_json::json!({ "word": word_query }),
        });
    }
    drop(probe);
    let snap = store::modify(app, cache, data, true, |tx| {
        let w =
            store::get_word(tx, word_query)?.with_context(|| format!("no word '{word_query}'"))?;
        let (ts, date) = now_local();
        let (pre_e, pre_f) = store::grade_review(tx, w.id, mode, ok, ts, &date)?;
        Ok(vec![OpKind::Graded {
            id: w.id.0,
            mode: mode.value(),
            ok,
            pre_e,
            pre_f,
        }])
    })?;
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
) -> Result<Receipt> {
    let probe = store::open_ro(&app.backup_path)?;
    if store::get_word(&probe, word_query)?.is_none() {
        let (ts, _) = now_local();
        crate::oplog::shelve_orphan(
            data,
            ts,
            &app.id,
            "triage on missing word",
            None,
            Some(word_query.to_string()),
        )?;
        return Ok(Receipt {
            applied: false,
            orphaned: true,
            snapshot: None,
            detail: serde_json::json!({ "word": word_query }),
        });
    }
    drop(probe);
    let snap = store::modify(app, cache, data, true, |tx| {
        let w =
            store::get_word(tx, word_query)?.with_context(|| format!("no word '{word_query}'"))?;
        let (ts, date) = now_local();
        let op = if known {
            store::triage_known(tx, w.id, ts, &date)?;
            OpKind::Triaged {
                id: w.id.0,
                decision: "known".to_string(),
            }
        } else {
            store::enroll_word(tx, w.id, ts, &date)?;
            OpKind::Enrolled { id: w.id.0 }
        };
        Ok(vec![op])
    })?;
    Ok(Receipt {
        applied: true,
        orphaned: false,
        snapshot: Some(snap),
        detail: serde_json::json!({ "word": word_query, "known": known }),
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
            let conn = store::open_ro(&app.backup_path)?;
            output::stats(&app.id, &store::stats(&conn)?, cli.format)?;
        }
        Cmd::Words {
            search,
            category,
            limit,
        } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = store::open_ro(&app.backup_path)?;
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
            let conn = store::open_ro(&app.backup_path)?;
            match store::get_word(&conn, query)? {
                Some(w) => output::word(&w, cli.format)?,
                None => anyhow::bail!("no word '{query}'"),
            }
        }
        Cmd::Log { word, limit } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = store::open_ro(&app.backup_path)?;
            let w = store::get_word(&conn, word)?.with_context(|| format!("no word '{word}'"))?;
            let entries = store::log_entries(&conn, w.id, *limit)?;
            output::log_entries(&entries, cli.format)?;
        }
        Cmd::Due { limit } => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = store::open_ro(&app.backup_path)?;
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
            let ok = match result.to_lowercase().as_str() {
                "ok" | "remembered" | "correct" | "got-it" | "gotit" => true,
                "fail" | "forgotten" | "missed" | "missed-it" | "missedit" => false,
                _ => anyhow::bail!("bad --result '{result}'; want ok|fail"),
            };
            let mode = parse_mode(mode)?;
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let data = data_dir(&cli)?;
            let r = do_grade(&app, &cache, &data, word, mode, ok)?;
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
            let r = do_triage(&app, &cache, &data, word, known)?;
            print_receipt("triaged", &r, cli.format);
        }
        Cmd::Categories => {
            let root = icloud_root(&cli)?;
            let app = resolve_app(&cli, &root)?;
            let conn = store::open_ro(&app.backup_path)?;
            output::categories(&store::categories(&conn)?, cli.format)?;
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
            let snap = store::modify(&app, &cache, &data, true, |conn| {
                let id = store::add_word(conn, word, transcription.as_deref(), &tr)?;
                let mut ops = vec![OpKind::Added {
                    id: id.0,
                    text: (*word).to_string(),
                    transcription: transcription.clone(),
                    tr: tr
                        .iter()
                        .map(|(l, t)| (l.code().to_string(), t.clone()))
                        .collect(),
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
            let conn = store::open_ro(&app.backup_path)?;
            let mut plan: Vec<(u64, String, crate::oplog::ReplayAction)> = Vec::new();
            for o in ops.iter().filter(|o| o.app == app.id) {
                let a = crate::oplog::replay_decision(&conn, &app.id, o)?;
                plan.push((o.seq, serde_json::to_string(&o.kind).unwrap_or_default(), a));
            }
            drop(conn);
            if !apply {
                match cli.format {
                    Format::Json => println!(
                        "{}",
                        serde_json::to_string_pretty(
                            &plan
                                .iter()
                                .map(|(s, k, a)| serde_json::json!({
                                    "seq": s, "op": k, "action": format!("{a:?}")
                                }))
                                .collect::<Vec<_>>()
                        )?
                    ),
                    Format::Table => {
                        for (s, k, a) in &plan {
                            println!("{s:<6} {k:<40} {a:?}");
                        }
                    }
                }
                return Ok(());
            }
            let mut applied = 0u64;
            let mut skipped = 0u64;
            let mut orphaned: Vec<(crate::oplog::Op, String)> = Vec::new();
            let mut outcomes: Vec<(u64, String, String)> = Vec::new();
            store::modify(&app, &cache, &data, false, |tx| {
                outcomes.clear();
                for o in ops.iter().filter(|o| o.app == app.id) {
                    let date = chrono::DateTime::from_timestamp(o.ts, 0)
                        .context("bad op ts")?
                        .with_timezone(&chrono::Local)
                        .format("%F")
                        .to_string();
                    match crate::oplog::replay_decision(tx, &app.id, o)? {
                        crate::oplog::ReplayAction::ApplyAdd {
                            id,
                            text,
                            transcription,
                            tr,
                        } => {
                            store::readd_word(tx, id, &text, transcription.as_deref(), &tr)?;
                            outcomes.push((o.seq, "apply-add".to_string(), String::new()));
                        }
                        crate::oplog::ReplayAction::ApplyGrade { id, mode } => {
                            let ok =
                                matches!(o.kind, crate::oplog::OpKind::Graded { ok: true, .. });
                            store::grade_review(tx, id, mode, ok, o.ts, &date)?;
                            outcomes.push((o.seq, "apply-grade".to_string(), String::new()));
                        }
                        crate::oplog::ReplayAction::ApplyTriage { id, known } => {
                            if known {
                                store::triage_known(tx, id, o.ts, &date)?;
                            } else {
                                store::enroll_word(tx, id, o.ts, &date)?;
                            }
                            outcomes.push((o.seq, "apply-triage".to_string(), String::new()));
                        }
                        crate::oplog::ReplayAction::ApplyEnroll { id } => {
                            store::enroll_word(tx, id, o.ts, &date)?;
                            outcomes.push((o.seq, "apply-enroll".to_string(), String::new()));
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
                    "apply-add" | "apply-grade" | "apply-triage" | "apply-enroll" => applied += 1,
                    "orphan" => {}
                    _ => skipped += 1,
                }
                let _ = (seq, reason);
            }
            orphaned.len();
            match cli.format {
                Format::Json => output::ok_json(
                    "replay applied",
                    serde_json::json!({ "applied": applied, "skipped": skipped, "orphaned": orphaned }),
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
            let r = match op {
                "grade" => {
                    let mode = parse_mode(v["mode"].as_str().unwrap_or(""))?;
                    let ok = match v["result"].as_str().unwrap_or("") {
                        "ok" | "remembered" | "correct" => true,
                        "fail" | "forgotten" | "missed" => false,
                        x => anyhow::bail!("bad result '{x}'; want ok|fail"),
                    };
                    let word = v["word"].as_str().context("missing .word")?;
                    do_grade(&app, &cache, &data, word, mode, ok)?
                }
                "triage" => {
                    let known = match v["decision"].as_str().unwrap_or("") {
                        "known" => true,
                        "learn" => false,
                        x => anyhow::bail!("bad decision '{x}'; want known|learn"),
                    };
                    let word = v["word"].as_str().context("missing .word")?;
                    do_triage(&app, &cache, &data, word, known)?
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
                    let snap = store::modify(&app, &cache, &data, true, |tx| {
                        let id = store::add_word(tx, word, transcription, &tr)?;
                        let mut ops = vec![OpKind::Added {
                            id: id.0,
                            text: word.to_string(),
                            transcription: transcription.map(str::to_string),
                            tr: tr
                                .iter()
                                .map(|(l, t)| (l.code().to_string(), t.clone()))
                                .collect(),
                        }];
                        if enroll {
                            let (ts, date) = now_local();
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
                    do_triage(&app, &cache, &data, word, false)?
                }
                _ => anyhow::bail!("bad .op '{op}'; want grade|triage|add|enroll"),
            };
            print_receipt("applied", &r, cli.format);
        }
        Cmd::Snapshot => {
            let root = icloud_root(&cli)?;
            let cache = cache_dir(&cli)?;
            let apps = match &cli.app {
                Some(sel) => vec![discover::resolve(&root, sel)?],
                None => discover::discover(&root)?,
            };
            let mut snaps = Vec::new();
            for app in &apps {
                match store::snapshot(app, &cache) {
                    Ok(snap) => snaps.push(serde_json::json!({ "app": app.id, "snapshot": snap })),
                    Err(e) => {
                        if cli.format == Format::Json {
                            output::ok_json(
                                "snapshot partial",
                                serde_json::json!({ "done": snaps, "error": format!("{e:#}") }),
                            );
                        } else {
                            eprintln!("{}: failed: {e:#}", app.id);
                        }
                    }
                }
            }
            match cli.format {
                Format::Json => {
                    output::ok_json("snapshot", serde_json::json!({ "snapshots": snaps }))
                }
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
