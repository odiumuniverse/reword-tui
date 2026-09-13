use crate::discover::{App, AppMeta};
use crate::model::{CardMode, Category, LogEntry, Stats, Word};
use anyhow::Result;
use serde::Serialize;
#[derive(Debug, Clone, Copy, PartialEq, Eq, clap::ValueEnum)]
pub enum Format {
    Table,
    Json,
}
#[derive(Serialize)]
struct AppJson {
    n: usize,
    id: String,
    container: String,
    backup: String,
    size_bytes: u64,
    mtime_secs: u64,
}
pub fn apps(apps: &[(App, AppMeta)], format: Format) -> Result<()> {
    match format {
        Format::Json => {
            let v: Vec<AppJson> = apps
                .iter()
                .enumerate()
                .map(|(i, (a, m))| AppJson {
                    n: i + 1,
                    id: a.id.clone(),
                    container: a.container.clone(),
                    backup: a.backup_path.to_string_lossy().into_owned(),
                    size_bytes: m.size_bytes,
                    mtime_secs: m.mtime_secs,
                })
                .collect();
            println!("{}", serde_json::to_string_pretty(&v)?);
        }
        Format::Table => {
            let (h0, h1, h2, h3, h4) = ("#", "APP", "SIZE", "MTIME", "BACKUP");
            println!("{h0:<3} {h1:<6} {h2:>10} {h3:<19} {h4}");
            for (i, (a, m)) in apps.iter().enumerate() {
                println!(
                    "{:<3} {:<6} {:>10} {:<19} {}",
                    i + 1,
                    a.id,
                    m.size_bytes,
                    m.mtime_secs,
                    a.backup_path.display()
                );
            }
        }
    }
    Ok(())
}
pub fn stats(app_id: &str, s: &Stats, format: Format) -> Result<()> {
    match format {
        Format::Json => println!("{}", serde_json::to_string_pretty(s)?),
        Format::Table => {
            println!("app: {app_id}");
            println!("words:      {}", s.words);
            println!("categories: {}", s.categories);
            println!("log rows:   {}", s.log_rows);
            println!("pictures:   {}", s.pictures);
            println!("audio:      {}", s.audio);
            println!(
                "native:     {}",
                s.settings.native_language.as_deref().unwrap_or("-")
            );
            println!(
                "daily goal: {}",
                s.settings.daily_goal.as_deref().unwrap_or("-")
            );
            println!(
                "last log:   {}",
                s.last_log_ts
                    .map(|t| t.to_string())
                    .unwrap_or_else(|| "-".to_string())
            );
        }
    }
    Ok(())
}
fn word_line(w: &Word) -> String {
    let tr: Vec<String> = w
        .translations
        .iter()
        .map(|(k, v)| format!("{}={v}", k.code()))
        .collect();
    format!("{} | {} | {}", w.id.0, w.text, tr.join("; "))
}
pub fn words(list: &[Word], format: Format) -> Result<()> {
    match format {
        Format::Json => println!("{}", serde_json::to_string_pretty(list)?),
        Format::Table => {
            for w in list {
                println!("{}", word_line(w));
            }
        }
    }
    Ok(())
}
fn progress_line(label: &str, m: &crate::model::ModeState) -> String {
    format!(
        "{label}: level={} step={} ease={:.2} fails={} last={}",
        m.level,
        m.step,
        m.easiness,
        m.fails,
        m.last_review_ts
            .map(|t| t.to_string())
            .unwrap_or_else(|| "-".to_string())
    )
}
pub fn word(w: &Word, format: Format) -> Result<()> {
    match format {
        Format::Json => println!("{}", serde_json::to_string_pretty(w)?),
        Format::Table => {
            println!("id: {}", w.id.0);
            println!("word: {}", w.text);
            println!(
                "transcription: {}",
                w.transcription.as_deref().unwrap_or("-")
            );
            for (k, v) in &w.translations {
                println!("{}: {v}", k.code());
            }
            for (k, v) in &w.examples {
                println!("example[{}]: {v}", k.code());
            }
            println!("{}", progress_line("recognition", &w.recognition));
            println!("{}", progress_line("reproduction", &w.reproduction));
        }
    }
    Ok(())
}
pub fn categories(list: &[Category], format: Format) -> Result<()> {
    match format {
        Format::Json => println!("{}", serde_json::to_string_pretty(list)?),
        Format::Table => {
            let (a, b, c, d) = ("ID", "CUSTOM", "WORDS", "NAME");
            println!("{a:<16} {b:<6} {c:>6} {d}");
            for c in list {
                println!(
                    "{:<16} {:<6} {:>6} {}",
                    c.id,
                    c.custom,
                    c.words,
                    c.name_en.as_deref().unwrap_or("-")
                );
            }
        }
    }
    Ok(())
}
pub fn log_entries(list: &[LogEntry], format: Format) -> Result<()> {
    match format {
        Format::Json => println!("{}", serde_json::to_string_pretty(list)?),
        Format::Table => {
            let (h0, h1, h2, h3, h4, h5, h6, h7) =
                ("ID", "TS", "WORD", "MODE", "QUEUE", "STEP", "NQ", "FLAGS");
            println!("{h0:<5} {h1:<10} {h2:<6} {h3:<4} {h4:<5} {h5:<4} {h6:<2} {h7}");
            for e in list {
                println!(
                    "{:<5} {:<10} {:<6} {:<4} {:<5} {:<4} {:<2} {}",
                    e.id,
                    e.ts,
                    e.word_id,
                    e.mode.value(),
                    e.queue.value(),
                    e.step,
                    e.next_queue.value(),
                    e.kind.value()
                );
            }
        }
    }
    Ok(())
}
fn modes_label(modes: &[CardMode]) -> String {
    modes
        .iter()
        .map(|m| match m {
            CardMode::Recognition => "rec".to_string(),
            CardMode::Reproduction => "rep".to_string(),
            CardMode::Unknown(v) => format!("unknown({v})"),
        })
        .collect::<Vec<_>>()
        .join("+")
}
fn due_since(w: &Word, modes: &[CardMode]) -> i64 {
    modes
        .iter()
        .filter_map(|m| {
            let s = match m {
                CardMode::Recognition => &w.recognition,
                CardMode::Reproduction => &w.reproduction,
                CardMode::Unknown(_) => return None,
            };
            Some(s.last_review_ts?.saturating_add(s.interval_secs?))
        })
        .min()
        .unwrap_or(0)
}
#[derive(Serialize)]
struct DueJson {
    id: i64,
    word: String,
    modes: Vec<i64>,
    overdue_secs: i64,
}
pub fn due(list: &[(Word, Vec<CardMode>)], now: i64, format: Format) -> Result<()> {
    match format {
        Format::Json => {
            let v: Vec<DueJson> = list
                .iter()
                .map(|(w, m)| DueJson {
                    id: w.id.0,
                    word: w.text.clone(),
                    modes: m.iter().map(|x| x.value()).collect(),
                    overdue_secs: now.saturating_sub(due_since(w, m)),
                })
                .collect();
            println!("{}", serde_json::to_string_pretty(&v)?);
        }
        Format::Table => {
            let (h0, h1, h2, h3) = ("ID", "WORD", "MODES", "OVERDUE");
            println!("{h0:<6} {h1:<24} {h2:<8} {h3}");
            for (w, m) in list {
                println!(
                    "{:<6} {:<24} {:<8} {}s",
                    w.id.0,
                    w.text,
                    modes_label(m),
                    now.saturating_sub(due_since(w, m))
                );
            }
        }
    }
    Ok(())
}
pub fn ok_json(message: &str, extra: serde_json::Value) {
    let v = serde_json::json!({ "ok": true, "message": message, "detail": extra });
    println!("{v}");
}
