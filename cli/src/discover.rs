use anyhow::{Context, Result};
use std::path::{Path, PathBuf};
use std::time::SystemTime;
pub const POAS_PREFIX: &str = "iCloud~ru~poas~";
#[derive(Debug, Clone)]
pub struct App {
    pub id: String,
    pub container: String,
    pub backup_path: PathBuf,
    pub backup_name: String,
}
#[derive(Debug, Clone)]
pub struct AppMeta {
    pub size_bytes: u64,
    pub mtime_secs: u64,
}
pub fn default_icloud_root() -> Result<PathBuf> {
    let home = dirs::home_dir().context("cannot determine home dir")?;
    Ok(home.join("Library/Mobile Documents"))
}
pub fn default_cache_dir() -> Result<PathBuf> {
    let home = dirs::home_dir().context("cannot determine home dir")?;
    Ok(home.join(".cache/reword-cli"))
}
pub fn default_data_dir() -> Result<PathBuf> {
    let base = dirs::data_dir().context("cannot determine data dir")?;
    Ok(base.join("reword"))
}
fn is_reword_db(path: &Path) -> bool {
    let Ok(conn) =
        rusqlite::Connection::open_with_flags(path, rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY)
    else {
        return false;
    };
    let n: i64 = conn
        .query_row(
            "SELECT COUNT(*) FROM sqlite_master
             WHERE type='table' AND name IN ('WORD','CATEGORY','LOG','SETTINGS')",
            [],
            |r| r.get(0),
        )
        .unwrap_or(0);
    n == 4
}
pub fn discover(root: &Path) -> Result<Vec<App>> {
    let mut apps = Vec::new();
    let rd = std::fs::read_dir(root)
        .with_context(|| format!("cannot read iCloud root {}", root.display()))?;
    for entry in rd {
        let entry = entry.with_context(|| format!("cannot read entry of {}", root.display()))?;
        if !entry.file_type().map(|t| t.is_dir()).unwrap_or(false) {
            continue;
        }
        let name = entry.file_name().to_string_lossy().into_owned();
        let docs = entry.path().join("Documents");
        let Ok(backups) = std::fs::read_dir(&docs) else {
            continue;
        };
        for b in backups.flatten() {
            let p = b.path();
            let Ok(meta) = std::fs::symlink_metadata(&p) else {
                continue;
            };
            if !meta.file_type().is_file() || meta.file_type().is_symlink() {
                continue;
            }
            if !p.extension().is_some_and(|x| x == "backup") {
                continue;
            }
            if !is_reword_db(&p) {
                continue;
            }
            apps.push(App {
                id: short_id(&name, &p),
                container: name.clone(),
                backup_name: p
                    .file_name()
                    .map(|x| x.to_string_lossy().into_owned())
                    .unwrap_or_default(),
                backup_path: p,
            });
        }
    }
    apps.sort_by(|a, b| a.id.cmp(&b.id));
    Ok(apps)
}
pub fn resolve(root: &Path, sel: &str) -> Result<App> {
    let apps = discover(root)?;
    if apps.is_empty() {
        anyhow::bail!("no ReWord apps found under {}", root.display());
    }
    if let Ok(n) = sel.parse::<usize>() {
        return apps
            .into_iter()
            .nth(n.wrapping_sub(1))
            .with_context(|| format!("no app number {n}; run `apps` to list"));
    }
    let found: Vec<App> = apps.into_iter().filter(|a| a.id == sel).collect();
    match found.len() {
        0 => {
            let mut msg = format!("unknown app '{sel}'; available:\n");
            for (i, a) in discover(root).unwrap_or_default().iter().enumerate() {
                msg.push_str(&format!("  {}: {} ({})\n", i + 1, a.id, a.container));
            }
            anyhow::bail!("{msg}");
        }
        1 => Ok(found.into_iter().next().unwrap()),
        _ => {
            let mut msg = format!("ambiguous app '{sel}'; candidates:\n");
            for a in &found {
                msg.push_str(&format!(
                    "  {} ({})\n",
                    a.backup_path.display(),
                    a.container
                ));
            }
            msg.push_str("use the 1-based number from `apps` instead");
            anyhow::bail!("{msg}");
        }
    }
}
pub fn file_meta(path: &Path) -> Result<AppMeta> {
    let md = std::fs::metadata(path).with_context(|| format!("cannot stat {}", path.display()))?;
    let mtime_secs = md
        .modified()
        .unwrap_or(SystemTime::UNIX_EPOCH)
        .duration_since(SystemTime::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    Ok(AppMeta {
        size_bytes: md.len(),
        mtime_secs,
    })
}
fn short_id(container: &str, backup: &Path) -> String {
    let suffix = container.strip_prefix(POAS_PREFIX).unwrap_or(container);
    match suffix {
        "englishwords" => "en".to_string(),
        "englishwords~esen" => "es".to_string(),
        s if s.starts_with("englishwords~") => s.trim_start_matches("englishwords~").to_string(),
        _ => backup
            .file_stem()
            .map(|x| {
                x.to_string_lossy()
                    .trim_start_matches("reword_")
                    .to_string()
            })
            .filter(|x| !x.is_empty())
            .unwrap_or_else(|| container.to_string()),
    }
}
