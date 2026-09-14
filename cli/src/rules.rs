//! Settings that steer the phone's learning algorithms, read from the
//! backup's SETTINGS table with ReWord 4.3.4's parsing and defaults
//! (om8 getters, d85/we6/t23/o08 enums).
use anyhow::{Context, Result};
use rusqlite::Connection;
use std::collections::HashMap;

/// Which side a card shows (d85): recognition asks for the translation,
/// reproduction for the word.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SideMode {
    Recognition,
    Reproduction,
    /// Both sides are learned apart, and their due times are kept apart.
    RecognitionOrReproduction,
    /// A card picks its side at random and one answer counts for both.
    Random,
}

impl SideMode {
    /// d85.a: an unknown value falls back to recognition.
    pub fn parse(s: &str) -> Self {
        if s.eq_ignore_ascii_case("recognition") {
            Self::Recognition
        } else if s.eq_ignore_ascii_case("reproduction") {
            Self::Reproduction
        } else if s.eq_ignore_ascii_case("recognition_or_reproduction") {
            Self::RecognitionOrReproduction
        } else if s.eq_ignore_ascii_case("random") {
            Self::Random
        } else {
            Self::Recognition
        }
    }

    /// d85.b: an answer on the recognition side moves reproduction too.
    pub fn rec_carries(self) -> bool {
        matches!(self, Self::Recognition | Self::Random)
    }

    /// d85.c: an answer on the reproduction side moves recognition too.
    pub fn rep_carries(self) -> bool {
        matches!(self, Self::Reproduction | Self::Random)
    }

    /// d85.d: one answer stands for the whole word; only
    /// recognition_or_reproduction keeps the two sides apart.
    pub fn joint(self) -> bool {
        self != Self::RecognitionOrReproduction
    }

    /// Sides a card may ask (a42.m/y): recognition unless the mode is
    /// reproduction, reproduction unless it is recognition.
    pub fn allows(self) -> (bool, bool) {
        (self != Self::Reproduction, self != Self::Recognition)
    }
}

/// Side of a card for a new word (we6).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum NewMode {
    Recognition,
    Reproduction,
    Random,
}

impl NewMode {
    /// om8.p: anything but reproduction or random reads as recognition.
    pub fn parse(s: &str) -> Self {
        if s.eq_ignore_ascii_case("reproduction") {
            Self::Reproduction
        } else if s.eq_ignore_ascii_case("random") {
            Self::Random
        } else {
            Self::Recognition
        }
    }
}

/// Where the keyboard and the choose-from-4 blocks appear (t23).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Blocks {
    Disabled,
    /// Only on reproduction cards, which ask for the foreign word.
    Foreign,
    Both,
}

impl Blocks {
    /// om8.n: an unknown value reads as both.
    pub fn parse(s: &str) -> Self {
        if s.eq_ignore_ascii_case("disabled") {
            Self::Disabled
        } else if s.eq_ignore_ascii_case("foreign") {
            Self::Foreign
        } else {
            Self::Both
        }
    }
}

/// Which categories feed the review (o08).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReviewFrom {
    Selected,
    All,
}

impl ReviewFrom {
    /// om8.v: an unknown value reads as selected.
    pub fn parse(s: &str) -> Self {
        if s.eq_ignore_ascii_case("all") {
            Self::All
        } else {
            Self::Selected
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Rules {
    pub new_words: NewMode,
    pub learning: SideMode,
    pub review: SideMode,
    pub keyboard: Blocks,
    pub guessing: Blocks,
    pub review_from: ReviewFrom,
    /// word_review_interval_completely_learned_days in seconds: the
    /// longest interval, and the gap after which a clean review retires
    /// the word as completely learned.
    pub cap_secs: i64,
    pub daily_goal: Option<i64>,
}

impl Default for Rules {
    fn default() -> Self {
        Self::from_map(&HashMap::new())
    }
}

impl Rules {
    pub fn from_map(m: &HashMap<String, String>) -> Self {
        let get = |k: &str, d: &'static str| m.get(k).map_or(d, String::as_str);
        // s19.c parses the stored text with Long.parseLong; a value it
        // cannot read would crash the phone, so fall back to the default.
        let days = m
            .get("word_review_interval_completely_learned_days")
            .and_then(|v| v.parse::<i64>().ok())
            .unwrap_or(60);
        // om8.m: only an all-digits value is a goal.
        let daily_goal = m
            .get("daily_goal")
            .filter(|v| !v.is_empty() && v.bytes().all(|b| b.is_ascii_digit()))
            .and_then(|v| v.parse().ok());
        Self {
            new_words: NewMode::parse(get("new_words_card_mode", "recognition")),
            learning: SideMode::parse(get("word_learning_card_mode", "reproduction")),
            review: SideMode::parse(get("word_review_card_mode", "reproduction")),
            keyboard: Blocks::parse(get("enable_words_keyboard_input", "foreign")),
            guessing: Blocks::parse(get("enable_guessing_game", "foreign")),
            review_from: ReviewFrom::parse(get("review_words_from_categories", "selected")),
            cap_secs: days.wrapping_mul(86400),
            daily_goal,
        }
    }

    /// A stored NULL reads as the default, like the phone's s2.e.
    pub fn load(conn: &Connection) -> Result<Self> {
        let mut st = conn.prepare("SELECT NAME, VALUE FROM SETTINGS WHERE VALUE IS NOT NULL")?;
        let map = st
            .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?
            .collect::<rusqlite::Result<HashMap<_, _>>>()
            .context("decode SETTINGS row")?;
        Ok(Self::from_map(&map))
    }
}

impl SideMode {
    /// The value the phone stores for the mode.
    pub fn name(self) -> &'static str {
        match self {
            Self::Recognition => "recognition",
            Self::Reproduction => "reproduction",
            Self::RecognitionOrReproduction => "recognition_or_reproduction",
            Self::Random => "random",
        }
    }
}

impl NewMode {
    pub fn name(self) -> &'static str {
        match self {
            Self::Recognition => "recognition",
            Self::Reproduction => "reproduction",
            Self::Random => "random",
        }
    }
}

impl Blocks {
    pub fn name(self) -> &'static str {
        match self {
            Self::Disabled => "disabled",
            Self::Foreign => "foreign",
            Self::Both => "both",
        }
    }
}

impl ReviewFrom {
    pub fn name(self) -> &'static str {
        match self {
            Self::Selected => "selected",
            Self::All => "all",
        }
    }
}

/// Checks a setting the desktop writes to the backup: only the learning
/// ones, with the values the phone's settings screen offers
/// (SettingsActivity; the mastered interval is its 1–999 day picker).
/// The device's own settings (night mode, swipes, notifications…) stay the
/// phone's.
pub fn check_setting(name: &str, value: &str) -> Result<()> {
    let ok = match name {
        "new_words_card_mode" => matches!(value, "recognition" | "reproduction" | "random"),
        "word_learning_card_mode" | "word_review_card_mode" => {
            matches!(value, "recognition" | "reproduction" | "recognition_or_reproduction" | "random")
        }
        "enable_words_keyboard_input" | "enable_guessing_game" => matches!(value, "disabled" | "foreign" | "both"),
        "review_words_from_categories" => matches!(value, "selected" | "all"),
        "word_review_interval_completely_learned_days" => {
            !value.is_empty()
                && value.bytes().all(|b| b.is_ascii_digit())
                && value.parse::<i64>().is_ok_and(|d| (1..=999).contains(&d))
        }
        "show_transcription" => matches!(value, "0" | "1"),
        _ => anyhow::bail!("setting '{name}' is not one the desktop changes"),
    };
    if !ok {
        anyhow::bail!("bad value '{value}' for {name}");
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rules(pairs: &[(&str, &str)]) -> Rules {
        Rules::from_map(
            &pairs
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect(),
        )
    }

    #[test]
    fn defaults_follow_the_phone() {
        let r = Rules::default();
        assert_eq!(r.new_words, NewMode::Recognition);
        assert_eq!(r.learning, SideMode::Reproduction);
        assert_eq!(r.review, SideMode::Reproduction);
        assert_eq!((r.keyboard, r.guessing), (Blocks::Foreign, Blocks::Foreign));
        assert_eq!(r.review_from, ReviewFrom::Selected);
        assert_eq!(r.cap_secs, 60 * 86400);
        assert_eq!(r.daily_goal, None);
    }

    #[test]
    fn unknown_values_fall_back_like_the_phone() {
        let r = rules(&[
            ("word_learning_card_mode", "sideways"),
            ("new_words_card_mode", "sideways"),
            ("enable_words_keyboard_input", "sideways"),
            ("review_words_from_categories", "sideways"),
            ("word_review_interval_completely_learned_days", "sixty"),
            ("daily_goal", "none"),
        ]);
        assert_eq!(r.learning, SideMode::Recognition);
        assert_eq!(r.new_words, NewMode::Recognition);
        assert_eq!(r.keyboard, Blocks::Both);
        assert_eq!(r.review_from, ReviewFrom::Selected);
        assert_eq!(r.cap_secs, 60 * 86400);
        assert_eq!(r.daily_goal, None);
    }

    #[test]
    fn reads_the_users_backup_settings() {
        let r = rules(&[
            ("word_review_card_mode", "recognition_or_reproduction"),
            ("word_learning_card_mode", "REPRODUCTION"),
            ("enable_guessing_game", "both"),
            ("word_review_interval_completely_learned_days", "30"),
            ("daily_goal", "30"),
        ]);
        assert_eq!(r.review, SideMode::RecognitionOrReproduction);
        assert_eq!(r.learning, SideMode::Reproduction);
        assert_eq!(r.guessing, Blocks::Both);
        assert_eq!(r.cap_secs, 30 * 86400);
        assert_eq!(r.daily_goal, Some(30));
    }

    #[test]
    fn names_read_back() {
        for v in ["recognition", "reproduction", "recognition_or_reproduction", "random"] {
            assert_eq!(SideMode::parse(v).name(), v);
        }
        for v in ["recognition", "reproduction", "random"] {
            assert_eq!(NewMode::parse(v).name(), v);
        }
        for v in ["disabled", "foreign", "both"] {
            assert_eq!(Blocks::parse(v).name(), v);
        }
        for v in ["selected", "all"] {
            assert_eq!(ReviewFrom::parse(v).name(), v);
        }
    }

    #[test]
    fn only_learning_settings_are_written() {
        assert!(check_setting("enable_guessing_game", "both").is_ok());
        assert!(check_setting("word_review_card_mode", "recognition_or_reproduction").is_ok());
        assert!(check_setting("word_review_interval_completely_learned_days", "999").is_ok());
        assert!(check_setting("word_review_interval_completely_learned_days", "1000").is_err());
        assert!(check_setting("word_review_interval_completely_learned_days", "0").is_err());
        assert!(check_setting("word_review_interval_completely_learned_days", "+5").is_err());
        assert!(check_setting("new_words_card_mode", "recognition_or_reproduction").is_err());
        assert!(check_setting("show_transcription", "yes").is_err());
        assert!(check_setting("inverted_swipes", "1").is_err(), "the phone keeps it per device");
    }

    #[test]
    fn side_mode_flags_match_d85() {
        use SideMode::*;
        let flags = |m: SideMode| (m.rec_carries(), m.rep_carries(), m.joint(), m.allows());
        assert_eq!(flags(Recognition), (true, false, true, (true, false)));
        assert_eq!(flags(Reproduction), (false, true, true, (false, true)));
        assert_eq!(flags(RecognitionOrReproduction), (false, false, false, (true, true)));
        assert_eq!(flags(Random), (true, true, true, (true, true)));
    }
}
