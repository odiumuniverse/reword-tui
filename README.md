# reword-tui

Learn ReWord vocabulary from your terminal. `rwcore` (Rust, in `cli/`)
reads and writes ReWord's iCloud `.backup` SQLite files; a Go TUI on top
is next.

## Sync warning

ReWord has no API and no merge: the phone app's `Create backup` /
`Restore` **overwrites the whole file**, and iCloud needs minutes to
propagate it to the Mac. `rwcore` edits the local copy only — after a
session you **must** tap `Restore` on the phone, or progress is lost.

## Build & test

```sh
cd cli
cargo build
cargo test        # 16 tests, no device needed
cargo clippy -- -D warnings
```

## Usage

```sh
rwcore apps                                # discovered backups (en/es/…)
rwcore --app en stats                      # due counts, streak
rwcore --app en due --mode 1               # words due now
rwcore --app en snapshot                   # frozen copy before any write
rwcore --app en review --ok 150 --mode 1   # resolve a review card
rwcore --app en status                     # snapshot vs live: clean/dirty
```

Writes go through a snapshot gate and are logged to a local op-log,
replayable onto a fresh backup (`pull` → `replay`). CI runs
`fmt --check`, `clippy -D warnings` and `cargo test` on every push and PR.
