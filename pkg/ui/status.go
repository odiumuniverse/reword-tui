package ui

import (
	"fmt"
	"math"

	"reword-tui/pkg/rwcore"
)

// goalScreen is true when the session has nothing to learn because the
// day's goal is reached: the phone's goal reached screen.
func (m Model) goalScreen() bool {
	d := m.sess.day
	return m.sess.cur == nil && !m.sess.dealing && m.sess.mode != modeReview && d.GoalReached && d.Goal != nil
}

// raiseStart is where the phone's "add more new words" dialog starts (zz1):
// half the day's goal, plus what was learned past the raised goal.
func (d day) raiseStart() int64 {
	base, adj := d.base(), d.adjusted()
	return max(base/2, base/2+(d.LearnedToday-adj))
}

// raiseMin keeps the raised goal above the words already learned.
func (d day) raiseMin() int64 {
	return max(1, d.LearnedToday-d.adjusted()+1)
}

// day is rwcore's day with the phone's goal defaults (5 when unset).
type day rwcore.Day

func (d day) base() int64 {
	if d.BaseGoal != nil {
		return *d.BaseGoal
	}
	return 5
}

func (d day) adjusted() int64 {
	if d.Goal != nil {
		return *d.Goal
	}
	return 5
}

// plural spells "%d unit" or "%d units".
func plural(n int64, one, many string) string {
	if n == 1 {
		return fmt.Sprintf(one, n)
	}
	return fmt.Sprintf(many, n)
}

// wordStatus is a word's status line in a category, as the phone reads it
// (v88.d): new, learning, already known, mastered, or when its review is
// due. The due time follows the learning mode setting, as on the phone:
// one side for recognition or reproduction, the sooner of the two else.
func wordStatus(w rwcore.Word, learningMode string, now int64) string {
	q1, q2 := w.Recognition.Level, w.Reproduction.Level
	switch {
	case q1 == 0 && q2 == 0:
		return "New"
	case q1 == 3 && q2 == 3:
		return "Already known"
	case q1 == 4 && q2 == 4:
		return "Mastered"
	case q1 == 1 || q2 == 1:
		return "Learning"
	}
	due := func(s rwcore.ModeState) (int64, bool) {
		if s.LastReviewTs == nil || s.IntervalSecs == nil {
			return 0, false
		}
		return *s.LastReviewTs + *s.IntervalSecs, true
	}
	var at int64
	var ok bool
	switch learningMode {
	case "reproduction":
		at, ok = due(w.Reproduction)
	case "recognition_or_reproduction", "random":
		r, rok := due(w.Recognition)
		p, pok := due(w.Reproduction)
		switch {
		case rok && pok:
			at, ok = min(r, p), true
		case rok:
			at, ok = r, true
		default:
			at, ok = p, pok
		}
	default:
		// d85.a reads anything else as recognition.
		at, ok = due(w.Recognition)
	}
	if !ok {
		return "New"
	}
	left := max(0, at-now)
	switch {
	case left == 0:
		return "Review now"
	case left >= 86400:
		return plural(left/86400, "Review in %d day", "Review in %d days")
	case left >= 3600:
		return plural(left/3600, "Review in %d hour", "Review in %d hours")
	case left >= 60:
		return plural(left/60, "Review in %d minute", "Review in %d minutes")
	}
	return "Review in 1 minute"
}

// showUpIn says when the next review comes, as the phone's empty review
// screen does (wy5.d): whole days, whole hours, minutes rounded up, seconds.
func showUpIn(secs int64) string {
	secs = max(secs, 0)
	switch {
	case secs >= 86400:
		return plural(secs/86400, "Words for review will show up in %d day", "Words for review will show up in %d days")
	case secs >= 3600:
		return plural(secs/3600, "Words for review will show up in %d hour", "Words for review will show up in %d hours")
	}
	if mins := int64(math.Ceil(float64(secs) / 60)); mins > 0 {
		return plural(mins, "Words for review will show up in %d minute", "Words for review will show up in %d minutes")
	}
	return plural(secs, "Words for review will show up in %d second", "Words for review will show up in %d seconds")
}
