use crate::model::{CardMode, Word};
pub fn due_modes(w: &Word, now: i64) -> Vec<CardMode> {
    let mut out = Vec::new();
    if w.recognition.level == 2
        && due_side(
            w.recognition.last_review_ts,
            w.recognition.interval_secs,
            now,
        )
    {
        out.push(CardMode::Recognition);
    }
    if w.reproduction.level == 2
        && due_side(
            w.reproduction.last_review_ts,
            w.reproduction.interval_secs,
            now,
        )
    {
        out.push(CardMode::Reproduction);
    }
    out
}
fn due_side(t: Option<i64>, i: Option<i64>, now: i64) -> bool {
    match (t, i) {
        (Some(t), Some(i)) => t.saturating_add(i) <= now.saturating_add(30),
        _ => false,
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
            level: 2,
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
            pos: None,
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
        assert!(due_modes(&word(Some(1), Some(10), None, None), -20).is_empty());
        assert!(due_modes(&word(Some(1), Some(10), None, None), -21).is_empty());
        assert_eq!(due_modes(&word(Some(1), Some(10), None, None), 11).len(), 1);
        assert_eq!(
            due_modes(&word(Some(1), Some(10), Some(1), Some(10)), 11).len(),
            2
        );
        assert_eq!(due_modes(&word(Some(1), Some(10), None, None), 0).len(), 1);
        assert_eq!(
            due_modes(&word(Some(1), Some(10), None, None), -19).len(),
            1
        );
        assert_eq!(
            due_modes(&word(Some(i64::MAX - 1), Some(100), None, None), 0).len(),
            0
        );
    }
}
