use crate::model::{CardMode, Word};
pub const CROSS_SIDE_INTERVAL_SECS: i64 = 1166400;
pub fn due_modes(w: &Word, now: i64) -> Vec<CardMode> {
    let mut out = Vec::new();
    if due_side(
        w.recognition.last_review_ts,
        w.recognition.interval_secs,
        now,
    ) {
        out.push(CardMode::Recognition);
    }
    if due_side(
        w.reproduction.last_review_ts,
        w.reproduction.interval_secs,
        now,
    ) {
        out.push(CardMode::Reproduction);
    }
    out
}
fn due_side(t: Option<i64>, i: Option<i64>, now: i64) -> bool {
    match (t, i) {
        (Some(t), Some(i)) => t.saturating_add(i) <= now,
        _ => false,
    }
}
pub fn ladder_interval(level: i64, easiness: f64, mode: CardMode) -> i64 {
    if level <= 1 {
        return match mode {
            CardMode::Recognition => 2700,
            _ => 1800,
        };
    }
    match level {
        2 => 10800,
        3 if easiness < 2.5 => 129600,
        3 => 86400,
        4 => 432000,
        5 => 3782000,
        _ => 5184000,
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{ModeState, WordId};
    use std::collections::BTreeMap;
    fn word(
        rec_t: Option<i64>,
        rec_i: Option<i64>,
        rep_t: Option<i64>,
        rep_i: Option<i64>,
    ) -> Word {
        let mode = |t, i| ModeState {
            level: 0,
            step: 0,
            easiness: 2.5,
            fails: 0,
            last_review_ts: t,
            interval_secs: i,
        };
        Word {
            id: WordId(1),
            text: "x".to_string(),
            transcription: None,
            translations: BTreeMap::new(),
            examples: BTreeMap::new(),
            recognition: mode(rec_t, rec_i),
            reproduction: mode(rep_t, rep_i),
        }
    }
    #[test]
    fn due_needs_both_ts_and_interval() {
        let now = 1_000_000;
        assert!(due_modes(&word(None, None, None, None), now).is_empty());
        assert!(due_modes(&word(Some(1), None, None, None), now).is_empty());
        assert!(due_modes(&word(None, Some(1), None, None), now).is_empty());
        assert_eq!(
            due_modes(&word(Some(1), Some(10), None, None), 11),
            vec![CardMode::Recognition]
        );
        assert!(due_modes(&word(Some(1), Some(10), None, None), 10).is_empty());
        assert!(due_modes(&word(Some(1), Some(10), None, None), 11).len() == 1);
        assert_eq!(
            due_modes(&word(Some(1), Some(10), Some(1), Some(10)), 11).len(),
            2
        );
        assert_eq!(
            due_modes(&word(Some(i64::MAX - 1), Some(100), None, None), 0).len(),
            0
        );
    }
    #[test]
    fn ladder_anchors() {
        assert_eq!(ladder_interval(1, 2.5, CardMode::Recognition), 2700);
        assert_eq!(ladder_interval(1, 2.5, CardMode::Reproduction), 1800);
        assert_eq!(ladder_interval(0, 2.5, CardMode::Recognition), 2700);
        assert_eq!(ladder_interval(2, 2.75, CardMode::Recognition), 10800);
        assert_eq!(ladder_interval(2, 2.75, CardMode::Reproduction), 10800);
        assert_eq!(ladder_interval(3, 3.0, CardMode::Recognition), 86400);
        assert_eq!(ladder_interval(3, 2.25, CardMode::Reproduction), 129600);
        assert_eq!(ladder_interval(4, 3.25, CardMode::Recognition), 432000);
        assert_eq!(ladder_interval(5, 3.5, CardMode::Reproduction), 3782000);
        assert_eq!(ladder_interval(9, 3.0, CardMode::Recognition), 5184000);
    }
}
