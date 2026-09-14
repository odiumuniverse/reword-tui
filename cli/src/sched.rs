//! The phone's scheduler, ported from ReWord 4.3.4 (p68 and the
//! WordPresenter answer callables s32, t32, r32, l32, p32, k32) so a desktop
//! session moves a word exactly like the app: same steps, easiness,
//! intervals, side sync and LOG rows.
//!
//! The app writes every answer from the point of view of the card's own
//! side; the other side follows only when the card mode carries it. The
//! port keeps that shape: each rule is written for "own = recognition"
//! and a reproduction card runs it on the flipped row.
use crate::rules::{Rules, SideMode};

/// Card side (lla): recognition shows the word and asks for the
/// translation, reproduction the other way round.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Side {
    Rec,
    Rep,
}

impl Side {
    pub fn mode(self) -> i64 {
        match self {
            Self::Rec => 1,
            Self::Rep => 2,
        }
    }

    fn other(self) -> Self {
        match self {
            Self::Rec => Self::Rep,
            Self::Rep => Self::Rec,
        }
    }

    /// d85.b/c: whether an answer on this side moves the other one too.
    fn carries(self, mode: SideMode) -> bool {
        match self {
            Self::Rec => mode.rec_carries(),
            Self::Rep => mode.rep_carries(),
        }
    }
}

/// WORD's scheduling columns (p68.a): queue, last review, interval,
/// step, easiness and fails for each side.
#[derive(Debug, Clone, Copy, PartialEq, serde::Serialize, serde::Deserialize)]
pub struct Row {
    pub q_rec: i64,
    pub q_rep: i64,
    pub t_rec: Option<i64>,
    pub t_rep: Option<i64>,
    pub i_rec: Option<i64>,
    pub i_rep: Option<i64>,
    pub s_rec: i64,
    pub s_rep: i64,
    pub e_rec: f32,
    pub e_rep: f32,
    pub f_rec: i64,
    pub f_rep: i64,
}

impl Row {
    fn flip(self) -> Self {
        Self {
            q_rec: self.q_rep,
            q_rep: self.q_rec,
            t_rec: self.t_rep,
            t_rep: self.t_rec,
            i_rec: self.i_rep,
            i_rep: self.i_rec,
            s_rec: self.s_rep,
            s_rep: self.s_rec,
            e_rec: self.e_rep,
            e_rep: self.e_rec,
            f_rec: self.f_rep,
            f_rep: self.f_rec,
        }
    }

    pub fn queue(&self, side: Side) -> i64 {
        match side {
            Side::Rec => self.q_rec,
            Side::Rep => self.q_rep,
        }
    }

    pub fn step(&self, side: Side) -> i64 {
        match side {
            Side::Rec => self.s_rec,
            Side::Rep => self.s_rep,
        }
    }
}

/// Runs a rule written for a recognition card on `side`.
fn on_side<T>(side: Side, row: Row, rule: impl FnOnce(Row) -> T, back: impl FnOnce(T) -> T) -> T {
    match side {
        Side::Rec => rule(row),
        Side::Rep => back(rule(row.flip())),
    }
}

fn clamp_ease(e: f32) -> f32 {
    1.25f32.max(4.25f32.min(e))
}

/// p68.b: the interval for `step`. The first four steps are fixed; later
/// ones stretch the time actually elapsed by the easiness. From step 4 on
/// the interval stops at `cap`, and snaps to it when within 8%.
pub fn ladder(step: i64, elapsed: i64, cap: i64, ease: f32) -> i64 {
    let base = match step {
        1 => 1800,
        2 => 10800,
        3 => 86400,
        4 => 432000,
        // Java promotes long * float to float, then truncates.
        _ => ((elapsed as f32) * ease) as i64,
    };
    if step < 4 {
        return base;
    }
    let min = cap.min(base);
    if ((cap - min).abs() as f64) / (cap as f64) <= f64::from(0.08f32) {
        cap
    } else {
        min
    }
}

/// p68.c: the least gap kept between the two sides' due times in
/// recognition_or_reproduction mode.
pub fn gap(step: i64) -> i64 {
    match step {
        1 => 900,
        2 => 5400,
        3 => 43200,
        4 => 86400,
        _ => 172800,
    }
}

/// s32 "Start learning": both sides enter learning, due in 30 s.
pub fn start_learning(now: i64) -> Row {
    Row {
        q_rec: 1,
        q_rep: 1,
        t_rec: Some(now),
        t_rep: Some(now),
        i_rec: Some(30),
        i_rep: Some(30),
        s_rec: 1,
        s_rep: 1,
        e_rec: 2.5,
        e_rep: 2.5,
        f_rec: 0,
        f_rep: 0,
    }
}

/// t32 "I already know": both sides park as known.
pub fn already_known(now: i64) -> Row {
    Row {
        q_rec: 3,
        q_rep: 3,
        t_rec: Some(now),
        t_rep: Some(now),
        i_rec: None,
        i_rep: None,
        s_rec: 0,
        s_rep: 0,
        e_rec: 2.5,
        e_rep: 2.5,
        f_rec: 0,
        f_rep: 0,
    }
}

/// r32 "Keep showing": the card's side restarts learning, due in 30 s.
pub fn keep_showing(now: i64, side: Side, learning: SideMode, row: Row) -> Row {
    let carry = side.carries(learning);
    on_side(
        side,
        row,
        |r| Row {
            q_rec: 1,
            t_rec: Some(now),
            i_rec: Some(30),
            s_rec: 1,
            q_rep: if carry { 1 } else { r.q_rep },
            t_rep: if carry { Some(now) } else { r.t_rep },
            i_rep: if carry { Some(30) } else { r.i_rep },
            s_rep: if carry { 1 } else { r.s_rep },
            e_rec: 2.5,
            e_rep: 2.5,
            f_rec: 0,
            f_rep: 0,
        },
        Row::flip,
    )
}

/// p68.a "I have memorized": the card's side graduates to review at step
/// 1; the other side follows when the learning mode carries it.
pub fn graduate(now: i64, side: Side, learning: SideMode, cap: i64, row: Row) -> Row {
    let carry = side.carries(learning);
    on_side(
        side,
        row,
        |r| {
            let i = ladder(1, 0, cap, r.e_rec);
            Row {
                q_rec: 2,
                t_rec: Some(now),
                i_rec: Some(i),
                s_rec: 1,
                q_rep: if carry { 2 } else { r.q_rep },
                t_rep: if carry { Some(now) } else { r.t_rep },
                i_rep: if carry { Some(i) } else { r.i_rep },
                s_rep: if carry { 1 } else { r.s_rep },
                e_rec: 2.5,
                e_rep: 2.5,
                f_rec: 0,
                f_rep: 0,
            }
        },
        Row::flip,
    )
}

/// p68.d: after a graduation in recognition_or_reproduction mode, keeps
/// the two sides from falling due together.
pub fn respace_graduated(now: i64, side: Side, review: SideMode, row: Row) -> Row {
    if review.joint() {
        return row;
    }
    on_side(
        side,
        row,
        |r| {
            let (Some(t_own), Some(t_oth), Some(mut io), Some(mut ix)) = (r.t_rec, r.t_rep, r.i_rec, r.i_rep)
            else {
                return r;
            };
            if r.q_rec != 2 || r.q_rep != 2 {
                return r;
            }
            let (c_own, c_oth) = (gap(r.s_rec), gap(r.s_rep));
            if t_own == t_oth && io == ix {
                ix += c_oth;
            }
            let floor = now + c_oth;
            if t_oth + ix < floor {
                ix += floor - (t_oth + ix);
            }
            if ((t_own + io) - (t_oth + ix)).abs() < c_own {
                io = (t_oth + ix + c_own) - t_own;
            }
            Row {
                i_rec: Some(io),
                i_rep: Some(ix),
                ..r
            }
        },
        Row::flip,
    )
}

/// p32 "Got it" on a due review card: the step climbs, easiness rises
/// after a clean cycle, and a clean review past the cap retires the side.
/// None when the card's side is not due, which leaves the word untouched.
pub fn review_ok(now: i64, side: Side, review: SideMode, cap: i64, row: Row) -> Option<Row> {
    let carry = side.carries(review);
    let next = on_side(
        side,
        row,
        |r| {
            let (Some(t), Some(i)) = (r.t_rec, r.i_rec) else {
                return None;
            };
            if t >= now || t + i - 60 > now {
                return None;
            }
            let elapsed = now - t;
            let mut ease = r.e_rec;
            if r.f_rec == 0 {
                ease = clamp_ease(ease + 0.25);
            }
            let (q, interval) = if r.f_rec == 0 {
                if elapsed >= cap - 60 {
                    (4, None)
                } else {
                    (2, Some(ladder(r.s_rec + 1, elapsed, cap, ease)))
                }
            } else if r.s_rec > 4 {
                (2, Some(ladder(4, 0, cap, ease)))
            } else {
                (2, Some(ladder(r.s_rec + 1, elapsed, cap, ease)))
            };
            let step = if q == 4 { 0 } else { r.s_rec + 1 };
            Some(Row {
                q_rec: q,
                t_rec: Some(now),
                i_rec: interval,
                s_rec: step,
                e_rec: ease,
                f_rec: 0,
                q_rep: if carry { q } else { r.q_rep },
                t_rep: if carry { Some(now) } else { r.t_rep },
                i_rep: if carry { interval } else { r.i_rep },
                s_rep: if carry { step } else { r.s_rep },
                e_rep: if carry { ease } else { r.e_rep },
                f_rep: if carry { 0 } else { r.f_rep },
            })
        },
        |o| o.map(Row::flip),
    )?;
    Some(respace_ok(now, side, review, next))
}

/// The spacing p32 runs after "Got it": the other side is pushed past its
/// own gap, then the card's side moves if the two still fall together.
fn respace_ok(now: i64, side: Side, review: SideMode, row: Row) -> Row {
    if review.joint() {
        return row;
    }
    on_side(
        side,
        row,
        |r| {
            let (Some(t_own), Some(t_oth)) = (r.t_rec, r.t_rep) else {
                return r;
            };
            if !matches!(r.q_rec, 2 | 4) || !matches!(r.q_rep, 2 | 4) {
                return r;
            }
            let (c_own, c_oth) = (gap(r.s_rec), gap(r.s_rep));
            let (mut io, mut ix) = (r.i_rec, r.i_rep);
            if let Some(x) = ix {
                let floor = now + c_oth;
                if x + t_oth < floor {
                    ix = Some(x + (floor - (x + t_oth)));
                }
            }
            if let (Some(o), Some(x)) = (io, ix)
                && ((o + t_own) - (x + t_oth)).abs() < c_own
            {
                io = Some((x + t_oth + c_own) - t_own);
            }
            Row {
                i_rec: io,
                i_rep: ix,
                ..r
            }
        },
        Row::flip,
    )
}

/// k32 "Missed it" on a due review card: the side comes back in a minute,
/// easiness drops once per cycle and the fail count grows; the step stays.
/// None when the card's side is not due.
pub fn review_fail(now: i64, side: Side, review: SideMode, row: Row) -> Option<Row> {
    let carry = side.carries(review);
    let next = on_side(
        side,
        row,
        |r| {
            let (Some(t), Some(i)) = (r.t_rec, r.i_rec) else {
                return None;
            };
            if t >= now || t + i > now {
                return None;
            }
            let fails = r.f_rec + 1;
            let mut ease = r.e_rec;
            if r.f_rec == 0 {
                ease = clamp_ease(ease - 0.5);
            }
            let interval = Some((now + 60) - t);
            Some(Row {
                q_rec: 2,
                i_rec: interval,
                e_rec: ease,
                f_rec: fails,
                q_rep: if carry { 2 } else { r.q_rep },
                t_rep: if carry { r.t_rec } else { r.t_rep },
                i_rep: if carry { interval } else { r.i_rep },
                s_rep: if carry { r.s_rec } else { r.s_rep },
                e_rep: if carry { ease } else { r.e_rep },
                f_rep: if carry { fails } else { r.f_rep },
                ..r
            })
        },
        |o| o.map(Row::flip),
    )?;
    Some(respace_fail(now, side, review, next))
}

/// The spacing k32 runs after "Missed it": here the other side gives way.
fn respace_fail(now: i64, side: Side, review: SideMode, row: Row) -> Row {
    if review.joint() {
        return row;
    }
    on_side(
        side,
        row,
        |r| {
            let (Some(t_own), Some(t_oth), Some(io), Some(mut ix)) = (r.t_rec, r.t_rep, r.i_rec, r.i_rep)
            else {
                return r;
            };
            if r.q_rec != 2 || r.q_rep != 2 {
                return r;
            }
            let c_oth = gap(r.s_rep);
            let floor = now + c_oth;
            if t_oth + ix < floor {
                ix += floor - (t_oth + ix);
            }
            if ((t_own + io) - (t_oth + ix)).abs() < c_oth {
                ix = (t_own + io + c_oth) - t_oth;
            }
            Row { i_rep: Some(ix), ..r }
        },
        Row::flip,
    )
}

/// What a swipe does, by the queue of the card's side (WordPresenter.m).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Action {
    AlreadyKnown,
    StartLearning,
    Memorized,
    KeepShowing,
    ReviewOk,
    ReviewFail,
}

impl Action {
    /// `positive` is the left answer before inverted_swipes. zna.a reads
    /// any queue outside 0..4 as new.
    pub fn of(side_queue: i64, positive: bool) -> Self {
        match (side_queue, positive) {
            (1, true) => Self::Memorized,
            (1, false) => Self::KeepShowing,
            (2..=4, true) => Self::ReviewOk,
            (2..=4, false) => Self::ReviewFail,
            (_, true) => Self::AlreadyKnown,
            (_, false) => Self::StartLearning,
        }
    }

    pub fn name(self) -> &'static str {
        match self {
            Self::AlreadyKnown => "already_known",
            Self::StartLearning => "start_learning",
            Self::Memorized => "memorized",
            Self::KeepShowing => "keep_showing",
            Self::ReviewOk => "review_ok",
            Self::ReviewFail => "review_fail",
        }
    }
}

/// A LOG row the answer writes (re5): mode, queue and step before, queue
/// after, flags (2 marks the copy for the side that followed).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct LogRow {
    pub mode: i64,
    pub queue: i64,
    pub step: i64,
    pub nqueue: i64,
    pub flags: i64,
}

/// The result of one answer: the new row (None leaves WORD untouched),
/// LOG rows to append, and whether the word's live LOG is dropped.
#[derive(Debug, Clone, PartialEq)]
pub struct Outcome {
    pub row: Option<Row>,
    pub log: Vec<LogRow>,
    pub clear_log: bool,
}

/// Applies one answer the way WordPresenter.m does.
pub fn answer(now: i64, rules: &Rules, action: Action, side: Side, row: Row) -> Outcome {
    let logged = |next: &Row, joint: bool| {
        let first = LogRow {
            mode: side.mode(),
            queue: row.queue(side),
            step: row.step(side),
            nqueue: next.queue(side),
            flags: 0,
        };
        let mut log = vec![first];
        if joint {
            log.push(LogRow {
                mode: side.other().mode(),
                flags: 2,
                ..first
            });
        }
        log
    };
    match action {
        Action::StartLearning => Outcome {
            row: Some(start_learning(now)),
            log: vec![],
            clear_log: false,
        },
        Action::AlreadyKnown => Outcome {
            row: Some(already_known(now)),
            log: [Side::Rec, Side::Rep]
                .into_iter()
                .map(|s| LogRow {
                    mode: s.mode(),
                    queue: row.queue(s),
                    step: row.step(s),
                    nqueue: 3,
                    flags: 0,
                })
                .collect(),
            clear_log: false,
        },
        Action::KeepShowing => {
            let next = keep_showing(now, side, rules.learning, row);
            Outcome {
                clear_log: next.q_rec != row.q_rec || next.q_rep != row.q_rep,
                row: Some(next),
                log: vec![],
            }
        }
        Action::Memorized => {
            let next = graduate(now, side, rules.learning, rules.cap_secs, row);
            let next = respace_graduated(now, side, rules.review, next);
            Outcome {
                log: logged(&next, rules.learning.joint()),
                row: Some(next),
                clear_log: false,
            }
        }
        Action::ReviewOk => match review_ok(now, side, rules.review, rules.cap_secs, row) {
            Some(next) => Outcome {
                log: logged(&next, rules.review.joint()),
                row: Some(next),
                clear_log: false,
            },
            None => Outcome {
                row: None,
                log: vec![],
                clear_log: false,
            },
        },
        Action::ReviewFail => Outcome {
            row: review_fail(now, side, rules.review, row),
            log: vec![],
            clear_log: false,
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const NOW: i64 = 1_789_246_418;

    fn rules(learning: SideMode, review: SideMode) -> Rules {
        Rules {
            learning,
            review,
            ..Rules::default()
        }
    }

    fn learning_row() -> Row {
        Row {
            t_rec: Some(NOW - 100),
            t_rep: Some(NOW - 100),
            ..start_learning(NOW - 100)
        }
    }

    fn review_row() -> Row {
        Row {
            q_rec: 2,
            q_rep: 2,
            t_rec: Some(NOW - 100),
            t_rep: Some(NOW - 2000),
            i_rec: Some(1_000_000),
            i_rep: Some(1800),
            s_rec: 4,
            s_rep: 1,
            e_rec: 2.5,
            e_rep: 2.5,
            f_rec: 0,
            f_rep: 0,
        }
    }

    fn log(mode: i64, queue: i64, step: i64, nqueue: i64, flags: i64) -> LogRow {
        LogRow {
            mode,
            queue,
            step,
            nqueue,
            flags,
        }
    }

    /// Golden vectors printed by the app's own p68 (copied verbatim from the
    /// 4.3.4 decompile into a Java harness): the port must agree on every one.
    mod golden {
        use super::*;

        #[derive(serde::Deserialize)]
        struct VRow {
            q_rec: i64,
            q_rep: i64,
            t_rec: Option<i64>,
            t_rep: Option<i64>,
            i_rec: Option<i64>,
            i_rep: Option<i64>,
            s_rec: i64,
            s_rep: i64,
            e_rec: u32,
            e_rep: u32,
            f_rec: i64,
            f_rep: i64,
        }

        impl From<&VRow> for Row {
            fn from(v: &VRow) -> Self {
                Row {
                    q_rec: v.q_rec,
                    q_rep: v.q_rep,
                    t_rec: v.t_rec,
                    t_rep: v.t_rep,
                    i_rec: v.i_rec,
                    i_rep: v.i_rep,
                    s_rec: v.s_rec,
                    s_rep: v.s_rep,
                    e_rec: f32::from_bits(v.e_rec),
                    e_rep: f32::from_bits(v.e_rep),
                    f_rec: v.f_rec,
                    f_rep: v.f_rep,
                }
            }
        }

        #[derive(serde::Deserialize)]
        #[serde(tag = "fn", rename_all = "snake_case")]
        enum Vector {
            Ladder {
                step: i64,
                prev: i64,
                cap: i64,
                ef: u32,
                out: i64,
            },
            Gap {
                step: i64,
                out: i64,
            },
            Graduate {
                now: i64,
                side: i64,
                mode: String,
                cap: i64,
                #[serde(rename = "in")]
                input: VRow,
                out: VRow,
            },
            Respace {
                now: i64,
                side: i64,
                mode: String,
                #[serde(rename = "in")]
                input: VRow,
                out: VRow,
            },
        }

        fn side_of(v: i64) -> Side {
            if v == 1 { Side::Rec } else { Side::Rep }
        }

        #[test]
        fn matches_the_apps_own_p68() {
            let mut seen = [0; 4];
            for (n, line) in include_str!("testdata/p68_vectors.jsonl").lines().enumerate() {
                match serde_json::from_str::<Vector>(line).unwrap() {
                    Vector::Ladder {
                        step,
                        prev,
                        cap,
                        ef,
                        out,
                    } => {
                        assert_eq!(ladder(step, prev, cap, f32::from_bits(ef)), out, "line {n}");
                        seen[0] += 1;
                    }
                    Vector::Gap { step, out } => {
                        assert_eq!(gap(step), out, "line {n}");
                        seen[1] += 1;
                    }
                    Vector::Graduate {
                        now,
                        side,
                        mode,
                        cap,
                        input,
                        out,
                    } => {
                        let got = graduate(now, side_of(side), SideMode::parse(&mode), cap, Row::from(&input));
                        assert_eq!(got, Row::from(&out), "line {n}");
                        seen[2] += 1;
                    }
                    Vector::Respace {
                        now,
                        side,
                        mode,
                        input,
                        out,
                    } => {
                        let got = respace_graduated(now, side_of(side), SideMode::parse(&mode), Row::from(&input));
                        assert_eq!(got, Row::from(&out), "line {n}");
                        seen[3] += 1;
                    }
                }
            }
            assert!(seen.iter().all(|&c| c > 0), "every p68 function must be covered: {seen:?}");
        }
    }

    #[test]
    fn ladder_anchors() {
        let cap = 60 * 86400;
        assert_eq!(
            [1, 2, 3, 4].map(|s| ladder(s, 0, cap, 2.5)),
            [1800, 10800, 86400, 432000]
        );
        assert_eq!(ladder(5, 1_000_000, cap, 2.5), 2_500_000);
        // Within 8% of the cap snaps to it; farther stays put.
        assert_eq!(ladder(5, 1_400_000, cap, 3.5), cap);
        assert_eq!(ladder(5, 1_300_000, cap, 3.5), 4_550_000);
        assert_eq!(ladder(6, 9_000_000, cap, 3.0), cap);
    }

    #[test]
    fn memorized_on_reproduction_matches_the_users_backup() {
        // The es backup: learning cards show the translation, review keeps
        // the sides apart. Every graduated word there reads rec 2700 / rep
        // 1800 at step 1, with a rep LOG row and a flagged rec copy.
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        let out = answer(NOW, &r, Action::Memorized, Side::Rep, learning_row());
        let row = out.row.unwrap();
        assert_eq!((row.q_rec, row.q_rep, row.s_rec, row.s_rep), (2, 2, 1, 1));
        assert_eq!((row.t_rec, row.t_rep), (Some(NOW), Some(NOW)));
        assert_eq!((row.i_rec, row.i_rep), (Some(2700), Some(1800)));
        assert_eq!((row.e_rec, row.e_rep, row.f_rec, row.f_rep), (2.5, 2.5, 0, 0));
        assert_eq!(out.log, vec![log(2, 1, 1, 2, 0), log(1, 1, 1, 2, 2)]);
    }

    #[test]
    fn memorized_apart_leaves_the_other_side_learning() {
        let r = rules(SideMode::RecognitionOrReproduction, SideMode::RecognitionOrReproduction);
        let out = answer(NOW, &r, Action::Memorized, Side::Rec, learning_row());
        let row = out.row.unwrap();
        assert_eq!((row.q_rec, row.q_rep), (2, 1));
        assert_eq!((row.i_rec, row.i_rep), (Some(1800), Some(30)));
        assert_eq!(out.log, vec![log(1, 1, 1, 2, 0)]);
    }

    #[test]
    fn review_ok_climbs_the_ladder() {
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        let out = answer(NOW, &r, Action::ReviewOk, Side::Rep, review_row());
        let row = out.row.unwrap();
        assert_eq!((row.q_rep, row.s_rep, row.i_rep, row.t_rep), (2, 2, Some(10800), Some(NOW)));
        assert_eq!((row.e_rep, row.f_rep), (2.75, 0));
        assert_eq!((row.i_rec, row.t_rec, row.s_rec), (Some(1_000_000), Some(NOW - 100), 4));
        assert_eq!(out.log, vec![log(2, 2, 1, 2, 0)]);
    }

    #[test]
    fn review_ok_after_a_miss_keeps_ease_and_resets_fails() {
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        let start = Row {
            s_rep: 5,
            f_rep: 1,
            e_rep: 2.0,
            ..review_row()
        };
        let row = review_ok(NOW, Side::Rep, r.review, r.cap_secs, start).unwrap();
        assert_eq!((row.i_rep, row.s_rep, row.e_rep, row.f_rep), (Some(432000), 6, 2.0, 0));
    }

    #[test]
    fn clean_review_past_the_cap_retires_the_side() {
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        let start = Row {
            t_rep: Some(NOW - r.cap_secs),
            i_rep: Some(r.cap_secs),
            s_rep: 6,
            ..review_row()
        };
        let out = answer(NOW, &r, Action::ReviewOk, Side::Rep, start);
        let row = out.row.unwrap();
        assert_eq!((row.q_rep, row.i_rep, row.s_rep), (4, None, 0));
        assert_eq!(out.log, vec![log(2, 2, 6, 4, 0)]);
    }

    #[test]
    fn review_answers_need_the_card_due() {
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        // "Got it" allows the last minute early, "Missed it" does not.
        let early = Row {
            t_rep: Some(NOW - 1800 + 60),
            ..review_row()
        };
        assert!(answer(NOW, &r, Action::ReviewOk, Side::Rep, early).row.is_some());
        assert!(answer(NOW, &r, Action::ReviewFail, Side::Rep, early).row.is_none());
        let too_early = Row {
            t_rep: Some(NOW - 1800 + 61),
            ..review_row()
        };
        let out = answer(NOW, &r, Action::ReviewOk, Side::Rep, too_early);
        assert_eq!((out.row, out.log.len()), (None, 0));
        let same_second = Row {
            t_rep: Some(NOW),
            i_rep: Some(0),
            ..review_row()
        };
        assert!(answer(NOW, &r, Action::ReviewFail, Side::Rep, same_second).row.is_none());
    }

    #[test]
    fn review_fail_returns_in_a_minute() {
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        let out = answer(NOW, &r, Action::ReviewFail, Side::Rep, review_row());
        assert!(out.log.is_empty());
        let row = out.row.unwrap();
        assert_eq!((row.q_rep, row.t_rep, row.i_rep), (2, Some(NOW - 2000), Some(2060)));
        assert_eq!((row.s_rep, row.e_rep, row.f_rep), (1, 2.0, 1));
        // A second miss in the same cycle leaves the easiness alone.
        let again = Row {
            t_rep: Some(NOW - 3000),
            i_rep: Some(100),
            ..row
        };
        let row = review_fail(NOW, Side::Rep, r.review, again).unwrap();
        assert_eq!((row.e_rep, row.f_rep), (2.0, 2));
    }

    #[test]
    fn joint_review_moves_both_sides_and_logs_a_copy() {
        let r = rules(SideMode::Recognition, SideMode::Recognition);
        let start = Row {
            t_rec: Some(NOW - 2000),
            i_rec: Some(1800),
            s_rec: 1,
            ..review_row()
        };
        let out = answer(NOW, &r, Action::ReviewOk, Side::Rec, start);
        let row = out.row.unwrap();
        assert_eq!((row.i_rec, row.i_rep), (Some(10800), Some(10800)));
        assert_eq!((row.s_rec, row.s_rep, row.t_rep), (2, 2, Some(NOW)));
        assert_eq!(out.log, vec![log(1, 2, 1, 2, 0), log(2, 2, 1, 2, 2)]);
    }

    #[test]
    fn fail_spacing_pushes_the_other_side_away() {
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        let start = Row {
            t_rec: Some(NOW - 1000),
            i_rec: Some(500),
            s_rec: 3,
            t_rep: Some(NOW - 1000),
            i_rep: Some(1100),
            s_rep: 2,
            ..review_row()
        };
        let row = review_fail(NOW, Side::Rec, r.review, start).unwrap();
        // Rec is due in a minute; rep gives way to a full 5400 s gap after it.
        assert_eq!(row.i_rec, Some(1060));
        assert_eq!(row.i_rep, Some(6460));
    }

    #[test]
    fn keep_showing_restarts_learning() {
        let r = rules(SideMode::Reproduction, SideMode::RecognitionOrReproduction);
        let start = Row {
            t_rec: Some(NOW - 5000),
            s_rec: 1,
            ..learning_row()
        };
        let out = answer(NOW, &r, Action::KeepShowing, Side::Rep, start);
        let row = out.row.unwrap();
        assert_eq!((row.q_rec, row.q_rep, row.t_rec, row.t_rep), (1, 1, Some(NOW), Some(NOW)));
        assert_eq!((row.i_rec, row.i_rep, out.clear_log), (Some(30), Some(30), false));
        // Pulling a reviewed side back into learning drops the live history.
        let r = rules(SideMode::Random, SideMode::RecognitionOrReproduction);
        let half = Row {
            q_rec: 2,
            ..learning_row()
        };
        let out = answer(NOW, &r, Action::KeepShowing, Side::Rep, half);
        assert_eq!((out.row.unwrap().q_rec, out.clear_log), (1, true));
    }

    #[test]
    fn new_word_answers() {
        let r = Rules::default();
        let out = answer(NOW, &r, Action::StartLearning, Side::Rec, Row { ..already_known(0) });
        assert_eq!(out.row, Some(start_learning(NOW)));
        assert!(out.log.is_empty());
        let fresh = Row {
            q_rec: 0,
            q_rep: 0,
            t_rec: None,
            t_rep: None,
            i_rec: None,
            i_rep: None,
            s_rec: 0,
            s_rep: 0,
            e_rec: 2.5,
            e_rep: 2.5,
            f_rec: 0,
            f_rep: 0,
        };
        let out = answer(NOW, &r, Action::AlreadyKnown, Side::Rep, fresh);
        let row = out.row.unwrap();
        assert_eq!((row.q_rec, row.q_rep, row.i_rec, row.t_rep), (3, 3, None, Some(NOW)));
        assert_eq!(out.log, vec![log(1, 0, 0, 3, 0), log(2, 0, 0, 3, 0)]);
    }

    #[test]
    fn action_follows_the_side_queue() {
        use Action::*;
        let table = [0, 1, 2, 3, 4, 5, -1].map(|q| (Action::of(q, true), Action::of(q, false)));
        assert_eq!(
            table,
            [
                (AlreadyKnown, StartLearning),
                (Memorized, KeepShowing),
                (ReviewOk, ReviewFail),
                (ReviewOk, ReviewFail),
                (ReviewOk, ReviewFail),
                (AlreadyKnown, StartLearning),
                (AlreadyKnown, StartLearning),
            ]
        );
    }
}
