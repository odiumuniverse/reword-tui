use serde::Serialize;
use std::collections::BTreeMap;
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Lang {
    Eng,
    Rus,
    Deu,
    Por,
    Ita,
    Fra,
    Ukr,
    Other(String),
}
impl Lang {
    pub fn code(&self) -> &str {
        match self {
            Lang::Eng => "ENG",
            Lang::Rus => "RUS",
            Lang::Deu => "DEU",
            Lang::Por => "POR",
            Lang::Ita => "ITA",
            Lang::Fra => "FRA",
            Lang::Ukr => "UKR",
            Lang::Other(s) => s,
        }
    }
    pub fn parse(s: &str) -> Option<Lang> {
        let u = s.to_uppercase();
        match u.as_str() {
            "ENG" => Some(Lang::Eng),
            "RUS" => Some(Lang::Rus),
            "DEU" => Some(Lang::Deu),
            "POR" => Some(Lang::Por),
            "ITA" => Some(Lang::Ita),
            "FRA" => Some(Lang::Fra),
            "UKR" => Some(Lang::Ukr),
            _ if u.len() == 3 && u.chars().all(|c| c.is_ascii_uppercase()) => Some(Lang::Other(u)),
            _ => None,
        }
    }
    pub fn known_codes() -> &'static str {
        "ENG RUS DEU POR ITA FRA UKR"
    }
}
impl Serialize for Lang {
    fn serialize<S: serde::Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_str(self.code())
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CardMode {
    Recognition,
    Reproduction,
    Unknown(i64),
}
impl CardMode {
    pub fn from_i64(v: i64) -> CardMode {
        match v {
            1 => CardMode::Recognition,
            2 => CardMode::Reproduction,
            x => CardMode::Unknown(x),
        }
    }
    pub fn value(self) -> i64 {
        match self {
            CardMode::Recognition => 1,
            CardMode::Reproduction => 2,
            CardMode::Unknown(x) => x,
        }
    }
}
impl Serialize for CardMode {
    fn serialize<S: serde::Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_i64(self.value())
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Queue {
    Graduation,
    Enrollment,
    Review,
    Unknown(i64),
}
impl Queue {
    pub fn from_i64(v: i64) -> Queue {
        match v {
            0 => Queue::Graduation,
            1 => Queue::Enrollment,
            2 => Queue::Review,
            x => Queue::Unknown(x),
        }
    }
    pub fn value(self) -> i64 {
        match self {
            Queue::Graduation => 0,
            Queue::Enrollment => 1,
            Queue::Review => 2,
            Queue::Unknown(x) => x,
        }
    }
}
impl Serialize for Queue {
    fn serialize<S: serde::Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_i64(self.value())
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AttemptKind {
    Graded,
    Viewed,
    Unknown(i64),
}
impl AttemptKind {
    pub fn from_i64(v: i64) -> AttemptKind {
        match v {
            0 => AttemptKind::Graded,
            2 => AttemptKind::Viewed,
            x => AttemptKind::Unknown(x),
        }
    }
    pub fn value(self) -> i64 {
        match self {
            AttemptKind::Graded => 0,
            AttemptKind::Viewed => 2,
            AttemptKind::Unknown(x) => x,
        }
    }
}
impl Serialize for AttemptKind {
    fn serialize<S: serde::Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_i64(self.value())
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize)]
pub struct WordId(pub i64);
impl std::fmt::Display for WordId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.0)
    }
}
#[derive(Debug, Clone, Serialize)]
pub struct ModeState {
    pub level: i64,
    pub step: i64,
    pub easiness: f64,
    pub fails: i64,
    pub last_review_ts: Option<i64>,
    pub interval_secs: Option<i64>,
}
#[derive(Debug, Clone, Serialize)]
pub struct Word {
    pub id: WordId,
    pub text: String,
    pub transcription: Option<String>,
    pub pos: Option<i64>,
    pub translations: BTreeMap<Lang, String>,
    pub examples: BTreeMap<Lang, String>,
    pub recognition: ModeState,
    pub reproduction: ModeState,
}
#[derive(Debug, Clone, Serialize)]
pub struct LogEntry {
    pub id: i64,
    pub ts: i64,
    pub date: String,
    pub word_id: WordId,
    pub mode: CardMode,
    pub queue: Queue,
    pub step: i64,
    pub next_queue: Queue,
    pub kind: AttemptKind,
}
#[derive(Debug, Clone, Serialize)]
pub struct Category {
    pub id: String,
    pub custom: bool,
    pub selected: bool,
    pub name_en: Option<String>,
    pub words: i64,
}
#[derive(Debug, Clone, Serialize)]
pub struct CategoryStat {
    pub category: String,
    pub total: i64,
    pub started: i64,
}
#[derive(Debug, Clone, Default, Serialize)]
pub struct Settings {
    pub native_language: Option<String>,
    pub daily_goal: Option<String>,
    pub ui_language: Option<String>,
}
#[derive(Debug, Clone, Serialize)]
pub struct TodayStats {
    pub today: String,
    pub learned: i64,
    pub reviewed: i64,
    pub memorizing: i64,
    pub mastered: i64,
    pub known: i64,
    pub goal: Option<i64>,
    pub streak_cur: i64,
    pub streak_best: i64,
    pub active_dates: Vec<String>,
}
#[derive(Debug, Clone, Serialize)]
pub struct Stats {
    pub words: i64,
    pub categories: i64,
    pub log_rows: i64,
    pub pictures: i64,
    pub audio: i64,
    pub settings: Settings,
    pub last_log_ts: Option<i64>,
}
