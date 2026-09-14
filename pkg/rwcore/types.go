package rwcore

import "encoding/json"

type App struct {
	N         int    `json:"n"`
	ID        string `json:"id"`
	Container string `json:"container"`
	Backup    string `json:"backup"`
	SizeBytes uint64 `json:"size_bytes"`
	MtimeSecs uint64 `json:"mtime_secs"`
}

type Settings struct {
	NativeLanguage *string `json:"native_language"`
	DailyGoal      *string `json:"daily_goal"`
	UILanguage     *string `json:"ui_language"`
	// LearningCardMode is the side the phone opens a word in learning on:
	// recognition, reproduction or recognition_or_reproduction.
	LearningCardMode *string `json:"learning_card_mode"`
}

type Stats struct {
	Words      int64    `json:"words"`
	Categories int64    `json:"categories"`
	LogRows    int64    `json:"log_rows"`
	Pictures   int64    `json:"pictures"`
	Audio      int64    `json:"audio"`
	Settings   Settings `json:"settings"`
	LastLogTs  *int64   `json:"last_log_ts"`
}

type Today struct {
	Today       string   `json:"today"`
	Learned     int64    `json:"learned"`
	Reviewed    int64    `json:"reviewed"`
	Memorizing  int64    `json:"memorizing"`
	Mastered    int64    `json:"mastered"`
	Known       int64    `json:"known"`
	Goal        *int64   `json:"goal"`
	StreakCur   int64    `json:"streak_cur"`
	StreakBest  int64    `json:"streak_best"`
	ActiveDates []string `json:"active_dates"`
	// Week is the calendar week, Monday first: each day's words learned in
	// both directions, as the phone's streak dots count them.
	Week     []WeekDay `json:"week"`
	WeekGoal *int64    `json:"week_goal"`
}

type WeekDay struct {
	Date    string `json:"date"`
	Learned int64  `json:"learned"`
}

type ModeState struct {
	Level        int64   `json:"level"`
	Step         int64   `json:"step"`
	Easiness     float64 `json:"easiness"`
	Fails        int64   `json:"fails"`
	LastReviewTs *int64  `json:"last_review_ts"`
	IntervalSecs *int64  `json:"interval_secs"`
}

type Word struct {
	ID            int64             `json:"id"`
	Text          string            `json:"text"`
	Transcription *string           `json:"transcription"`
	Pos           *int64            `json:"pos"`
	Translations  map[string]string `json:"translations"`
	Examples      map[string]string `json:"examples"`
	Recognition   ModeState         `json:"recognition"`
	Reproduction  ModeState         `json:"reproduction"`
}

// Deal is the next card of a session, dealt the way the phone deals it,
// with the day's counters.
type Deal struct {
	Card *Card `json:"card"`
	Day  Day   `json:"day"`
	Now  int64 `json:"now"`
}

// Check is how the phone's keyboard block grades a typed answer: "correct",
// "partial" (accepted in yellow) or "wrong".
type Check struct {
	Verdict  string `json:"verdict"`
	Accepted bool   `json:"accepted"`
	Expected string `json:"expected"`
	Lang     string `json:"lang"`
}

// Synced is the settings the desktop shares with the phone through the
// backup's SETTINGS, as the phone reads them (its defaults filled in).
type Synced struct {
	NewWords      string `json:"new_words_card_mode"`
	Learning      string `json:"word_learning_card_mode"`
	Review        string `json:"word_review_card_mode"`
	Keyboard      string `json:"enable_words_keyboard_input"`
	Guessing      string `json:"enable_guessing_game"`
	ReviewFrom    string `json:"review_words_from_categories"`
	MasteredDays  int64  `json:"word_review_interval_completely_learned_days"`
	Transcription bool   `json:"show_transcription"`
	DailyGoal     *int64 `json:"daily_goal"`
}

// Card is one card as the phone lays it out: the side it asks (1 shows
// the word, 2 the translation), the queue of that side, which picks what a
// swipe does, the word's status, which names the swipe answers, and the
// blocks it offers.
type Card struct {
	Word     Word   `json:"word"`
	Side     int64  `json:"side"`
	Source   string `json:"source"`
	Queue    int64  `json:"queue"`
	Status   int64  `json:"status"`
	Variants []Word `json:"variants"`
	Keyboard bool   `json:"keyboard"`
	Choose   bool   `json:"choose"`
}

type Day struct {
	Learning     int64  `json:"learning"`
	LearnedToday int64  `json:"learned_today"`
	Goal         *int64 `json:"goal"`
	// BaseGoal is the day's goal before "continue" raised it.
	BaseGoal    *int64 `json:"base_goal"`
	GoalReached bool   `json:"goal_reached"`
	Due         int64  `json:"due"`
	NextReview  *int64 `json:"next_review"`
}

type DueItem struct {
	ID          int64   `json:"id"`
	Word        string  `json:"word"`
	Modes       []int64 `json:"modes"`
	OverdueSecs int64   `json:"overdue_secs"`
}

type Category struct {
	ID       string  `json:"id"`
	Custom   bool    `json:"custom"`
	Selected bool    `json:"selected"`
	NameEn   *string `json:"name_en"`
	Words    int64   `json:"words"`
}

func (c Category) DisplayName() string {
	if c.NameEn != nil && *c.NameEn != "" {
		return *c.NameEn
	}
	return c.ID
}

type CatStat struct {
	Category string `json:"category"`
	Total    int64  `json:"total"`
	Started  int64  `json:"started"`
}

type LogEntry struct {
	ID        int64  `json:"id"`
	Ts        int64  `json:"ts"`
	Date      string `json:"date"`
	WordID    int64  `json:"word_id"`
	Mode      int64  `json:"mode"`
	Queue     int64  `json:"queue"`
	Step      int64  `json:"step"`
	NextQueue int64  `json:"next_queue"`
	Kind      int64  `json:"kind"`
}

type Fingerprint struct {
	SizeBytes uint64 `json:"size_bytes"`
	MtimeNs   uint64 `json:"mtime_ns"`
	Words     int64  `json:"words"`
	LogRows   int64  `json:"log_rows"`
	MaxLogID  int64  `json:"max_log_id"`
}

type StatusRow struct {
	App     string      `json:"app"`
	State   string      `json:"state"`
	Current Fingerprint `json:"current"`
	Backup  string      `json:"backup"`
}

type OpEntry struct {
	V    uint32      `json:"v"`
	Seq  uint64      `json:"seq"`
	Ts   int64       `json:"ts"`
	App  string      `json:"app"`
	Kind OpKind      `json:"kind"`
	Pre  Fingerprint `json:"pre"`
	Post Fingerprint `json:"post"`
}

type OpKind struct {
	Name string
	Rest map[string]any
}

func (k *OpKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	k.Name = name
	k.Rest = map[string]any{}
	return nil
}

func (k OpKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.Name)
}

type OrphanEntry struct {
	Ts        int64          `json:"ts"`
	App       string         `json:"app"`
	Reason    string         `json:"reason"`
	Op        map[string]any `json:"op"`
	WordQuery *string        `json:"word_query"`
}

type ReceiptDetail struct {
	Applied  bool           `json:"applied"`
	Orphaned bool           `json:"orphaned"`
	Snapshot *string        `json:"snapshot"`
	Detail   map[string]any `json:"detail"`
}

type Receipt struct {
	Ok      bool          `json:"ok"`
	Message string        `json:"message"`
	Detail  ReceiptDetail `json:"detail"`
}

type OkWrap struct {
	Ok      bool           `json:"ok"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail"`
}
