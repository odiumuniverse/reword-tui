# reword-tui

[![CI](https://github.com/odiumuniverse/reword-tui/actions/workflows/ci.yml/badge.svg)](https://github.com/odiumuniverse/reword-tui/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27-blue)](go.mod)
[![Rust](https://img.shields.io/badge/Rust-stable-orange)](cli/Cargo.toml)
[![License: MIT](https://img.shields.io/badge/License-MIT-green)](LICENSE)

Spaced-repetition terminal client for your ReWord vocabulary. Go (Bubble Tea) TUI
over `rwcore`, a small Rust backend that reads and writes ReWord's iCloud
`.backup` SQLite files.

> **Companion, not a standalone app.** It works with words you already learn in
> the ReWord phone app — install that first, add words there, and make at least
> one backup. With an empty vocabulary there is nothing to repeat.

## Sync warning

ReWord has no API and no merge: the phone app's `Create backup` / `Restore`
**overwrites the whole file**, and iCloud needs minutes to propagate it to the
Mac. This tool edits the local copy only — after a session you **must** tap
`Restore` on the phone, or progress is lost. Every write goes through a
snapshot gate and is logged to a local op-log (`pull` → `replay`).

## Run

```sh
make build            # ./reword-tui + private ./rwcore next to it
./reword-tui                              # app picker (or straight to Learn)
./reword-tui --app es                     # skip picker
./reword-tui --rwcore PATH --icloud-root DIR --data-dir DIR --queue FILE
make test             # rust tests + clippy + fmt + go tests (incl. rwcore E2E)
```

Keys: `enter` open · `c` categories · `2` vocabulary · `3` menu · `s` sync ·
`?` help · `q` quit (reminds you about `Restore`).

## Layout

- `cmd/reword-tui` — entry point, flags, queue-path defaults
- `cli/` (`rwcore`) — Rust backend: reads, snapshot-gated writes, op-log
- `pkg/ui` — Bubble Tea screens (Learn, session, vocabulary, word, stats, sync)
- `pkg/rwcore` — Go client shelling out to `rwcore --format json`
- `pkg/queue` — durable intent queue (`queue.jsonl`)
