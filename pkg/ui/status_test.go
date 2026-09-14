package ui

import (
	"strings"
	"testing"

	"reword-tui/pkg/rwcore"
)

func TestWordStatusReadsLikeThePhone(t *testing.T) {
	const now = int64(1_000_000)
	due := func(ts, iv int64) rwcore.ModeState {
		return rwcore.ModeState{Level: 2, LastReviewTs: &ts, IntervalSecs: &iv}
	}
	level := func(q int64) rwcore.ModeState { return rwcore.ModeState{Level: q} }
	both := func(s rwcore.ModeState) rwcore.Word { return rwcore.Word{Recognition: s, Reproduction: s} }
	split := rwcore.Word{Recognition: due(now, 3*86400+5), Reproduction: due(now, 7200)}
	cases := []struct {
		w    rwcore.Word
		mode string
		want string
	}{
		{rwcore.Word{}, "reproduction", "New"},
		{both(level(3)), "reproduction", "Already known"},
		{both(level(4)), "reproduction", "Mastered"},
		{rwcore.Word{Recognition: level(1), Reproduction: level(2)}, "reproduction", "Learning"},
		{split, "reproduction", "Review in 2 hours"},
		{split, "recognition", "Review in 3 days"},
		{split, "recognition_or_reproduction", "Review in 2 hours"},
		{split, "sideways", "Review in 3 days"},
		{both(due(now-100, 50)), "reproduction", "Review now"},
		{both(due(now, 30)), "reproduction", "Review in 1 minute"},
		{both(due(now, 3600)), "reproduction", "Review in 1 hour"},
		{both(level(2)), "reproduction", "New"},
	}
	for _, c := range cases {
		if got := wordStatus(c.w, c.mode, now); got != c.want {
			t.Errorf("%s: got %q, want %q", c.mode, got, c.want)
		}
	}
}

func TestShowUpInRoundsLikeThePhone(t *testing.T) {
	for secs, want := range map[int64]string{
		90000: "1 day",
		7300:  "2 hours",
		61:    "2 minutes",
		60:    "1 minute",
		1:     "1 minute",
		0:     "0 seconds",
	} {
		if got := showUpIn(secs); got != "Words for review will show up in "+want {
			t.Errorf("%ds: got %q", secs, got)
		}
	}
}

func TestGoalReachedScreenContinues(t *testing.T) {
	goalModel := func() Model {
		m := testModel(t)
		m.width, m.height = 100, 30
		m.screen = sSession
		m.sess.mode = modeLearn
		m.sess.started = true
		goal, base, next := int64(45), int64(30), int64(4000)
		m.sess.day = rwcore.Day{LearnedToday: 45, Goal: &goal, BaseGoal: &base, GoalReached: true, NextReview: &next}
		m.sess.now = 400
		return m
	}
	m := goalModel()
	out := sgrRe.ReplaceAllString(m.View(), "")
	for _, s := range []string{"Nice job!", "Today you've learned 45 new words", "Words for review will show up in 1 hour", "[c] continue"} {
		if !strings.Contains(out, s) {
			t.Fatalf("the goal screen lacks %q:\n%s", s, out)
		}
	}
	m = press(t, m, "c")
	if m.ov != oGoal || m.numFor != "raise" || m.goalInput != "15" {
		t.Fatalf("continue starts at half the base goal: ov=%v for=%q in=%q", m.ov, m.numFor, m.goalInput)
	}
	for _, k := range []string{"backspace", "backspace", "0", "enter"} {
		m = press(t, m, k)
	}
	if m.ov != oGoal || m.err == "" {
		t.Fatal("the raised goal must stay above what is learned")
	}
	for _, k := range []string{"backspace", "1", "0", "enter"} {
		m = press(t, m, k)
	}
	if it := m.q.Items[len(m.q.Items)-1]; m.ov != oNone || it.Op != "raise_goal" || it.By != 10 || !m.sess.dealing {
		t.Fatalf("continue queues the raise and deals on: %+v", it)
	}
	if m = press(t, goalModel(), "r"); m.sess.mode != modeReview || !m.sess.dealing {
		t.Fatal("r on the goal screen goes to review")
	}
}
