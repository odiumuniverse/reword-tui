TUI := reword-tui
RWCORE := rwcore

.PHONY: all build test vet clean

all: build

build:
	cargo build --release --manifest-path cli/Cargo.toml
	go build -o $(TUI) .
	cp cli/target/release/$(RWCORE) ./$(RWCORE)

test:
	cargo test --manifest-path cli/Cargo.toml
	cargo clippy --manifest-path cli/Cargo.toml -- -D warnings
	cargo fmt --manifest-path cli/Cargo.toml -- --check
	RWCORE_BIN=$(CURDIR)/cli/target/debug/$(RWCORE) go test ./...

vet:
	go vet ./...

clean:
	rm -f $(TUI) $(RWCORE)
