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

## Go TUI

```sh
make build            # builds ./reword-tui + private ./rwcore next to it
./reword-tui                              # app picker (or straight to Learn)
./reword-tui --app es                     # skip picker
./reword-tui --rwcore PATH --icloud-root DIR --data-dir DIR --queue FILE
make test             # rust tests + clippy + fmt + go tests (incl. rwcore E2E)
```

The TUI never opens SQLite: every read is `rwcore --format json`,
every mutation is a queued intent written once via `snapshot` → `apply`.
`q` anywhere reminds you to tap `Restore` on the phone.
