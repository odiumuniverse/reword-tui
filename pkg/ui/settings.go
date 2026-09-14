package ui

import (
	"fmt"
	"slices"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"reword-tui/pkg/queue"
	"reword-tui/pkg/rwcore"
)

// settingRow is one line of the settings screen. A synced row names its
// SETTINGS key and the values the phone's settings screen offers; a row of
// this computer names the pref it flips.
type settingRow struct {
	key    string // SETTINGS name
	local  string // "goal", "reveal" or "inverted"
	label  string
	values []string // choices to cycle; nil for the day count
}

const masteredKey = "word_review_interval_completely_learned_days"

var (
	sideModes  = []string{"recognition", "reproduction", "recognition_or_reproduction", "random"}
	blockModes = []string{"disabled", "foreign", "both"}

	settingRows = []settingRow{
		{key: "new_words_card_mode", label: "New words first", values: []string{"recognition", "reproduction", "random"}},
		{key: "word_learning_card_mode", label: "Learning mode", values: sideModes},
		{key: "word_review_card_mode", label: "Review mode", values: sideModes},
		{key: "enable_words_keyboard_input", label: "Keyboard input", values: blockModes},
		{key: "enable_guessing_game", label: "Choose the correct word", values: blockModes},
		{key: "review_words_from_categories", label: "Review words from", values: []string{"selected", "all"}},
		{key: masteredKey, label: "Mastered after an interval of"},
		{key: "show_transcription", label: "Show transcription", values: []string{"1", "0"}},
		{local: "goal", label: "Daily goal"},
		{local: "reveal", label: "Show translation at once"},
		{local: "inverted", label: "Inverted swipes"},
	}
)

// sharedRows is how many rows at the top live in the backup and so are
// shared with the phone; the rest belong to this computer.
const sharedRows = 9

func syncedValue(s rwcore.Synced, key string) string {
	switch key {
	case "new_words_card_mode":
		return s.NewWords
	case "word_learning_card_mode":
		return s.Learning
	case "word_review_card_mode":
		return s.Review
	case "enable_words_keyboard_input":
		return s.Keyboard
	case "enable_guessing_game":
		return s.Guessing
	case "review_words_from_categories":
		return s.ReviewFrom
	case masteredKey:
		return strconv.FormatInt(s.MasteredDays, 10)
	case "show_transcription":
		if s.Transcription {
			return "1"
		}
		return "0"
	}
	return ""
}

func setSyncedValue(s *rwcore.Synced, key, v string) {
	switch key {
	case "new_words_card_mode":
		s.NewWords = v
	case "word_learning_card_mode":
		s.Learning = v
	case "word_review_card_mode":
		s.Review = v
	case "enable_words_keyboard_input":
		s.Keyboard = v
	case "enable_guessing_game":
		s.Guessing = v
	case "review_words_from_categories":
		s.ReviewFrom = v
	case masteredKey:
		s.MasteredDays, _ = strconv.ParseInt(v, 10, 64)
	case "show_transcription":
		s.Transcription = v == "1"
	}
}

// valueLabel spells a stored value the way the phone's settings read.
func valueLabel(key, v string) string {
	switch key {
	case masteredKey:
		return v + " days"
	case "show_transcription":
		return onoff(v == "1")
	}
	switch v {
	case "recognition":
		return "word → translation"
	case "reproduction":
		return "translation → word"
	case "recognition_or_reproduction":
		return "both directions"
	case "random":
		return "one random direction"
	case "disabled":
		return "off"
	case "foreign":
		return "foreign words"
	case "both":
		return "words and translations"
	case "selected":
		return "chosen categories"
	case "all":
		return "all categories"
	}
	return v
}

func (m Model) settingValue(r settingRow) string {
	switch r.local {
	case "goal":
		goal := "Not set"
		if m.today != nil && m.today.Goal != nil {
			goal = fmt.Sprint(*m.today.Goal)
		} else if m.stats != nil && m.stats.Settings.DailyGoal != nil {
			goal = *m.stats.Settings.DailyGoal
		}
		return goal + dim.Render("  [g] adjust")
	case "reveal":
		return onoff(m.prefs.RevealAtOnce)
	case "inverted":
		return onoff(m.prefs.InvertedSwipes)
	}
	if m.synced == nil {
		return "…"
	}
	return valueLabel(r.key, syncedValue(*m.synced, r.key))
}

// changeSetting moves the row under the cursor to its next value; the day
// count and the daily goal ask for a number instead.
func (m Model) changeSetting() (tea.Model, tea.Cmd) {
	r := settingRows[m.setIdx]
	switch r.local {
	case "goal":
		m.goalTitle = "How many new words do you want to learn per day?"
		m.goalInput = ""
		m.ov = oGoal
		return m, nil
	case "reveal":
		m.prefs.RevealAtOnce = !m.prefs.RevealAtOnce
	case "inverted":
		m.prefs.InvertedSwipes = !m.prefs.InvertedSwipes
	}
	if r.local != "" {
		if err := m.prefs.Save(); err != nil {
			m.err = err.Error()
		}
		return m, nil
	}
	if m.synced == nil {
		return m, nil
	}
	if r.values == nil {
		m.numFor = "mastered"
		m.goalTitle = "Mastered after an interval of how many days? (1–999)"
		m.goalInput = strconv.FormatInt(m.synced.MasteredDays, 10)
		m.ov = oGoal
		return m, nil
	}
	i := slices.Index(r.values, syncedValue(*m.synced, r.key))
	return m.setSynced(r.key, r.values[(i+1)%len(r.values)])
}

// setSynced writes a shared setting like any other change: into the queue
// for iCloud and at once into the working copy, so the next card follows it.
func (m Model) setSynced(key, value string) (tea.Model, tea.Cmd) {
	m.enqueue(queue.Intent{Op: "setting", Name: key, Value: value})
	s := rwcore.Synced{}
	if m.synced != nil {
		s = *m.synced
	}
	setSyncedValue(&s, key, value)
	m.synced = &s
	m.prefs.ShowTranscription = s.Transcription
	if m.cli.DB == "" {
		// Without a working copy the backup still holds the old value.
		return m, nil
	}
	return m, m.loadSynced()
}
