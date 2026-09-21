TUI := reword-tui
RWCORE := rwcore

# A self-signed code signing identity pins the designated requirement to the
# certificate instead of the cdhash, so it is stable across rebuilds. Without it the
# build falls back to ad-hoc. The identifiers matter too: codesign would default both
# Go and Rust binaries to "a.out".
SIGN_IDENTITY ?= odiumuniverse code signing

.PHONY: all build sign test vet clean

all: build

build:
	cargo build --release --manifest-path cli/Cargo.toml
	go build -o $(TUI) ./cmd/reword-tui
	cp cli/target/release/$(RWCORE) ./$(RWCORE)

sign: build
	@for bin in $(TUI) $(RWCORE); do \
	  if codesign --force --sign "$(SIGN_IDENTITY)" --timestamp=none --identifier com.odiumuniverse.$$bin $$bin 2>/dev/null; then \
	    :; \
	  else \
	    echo "warning: cannot sign with '$(SIGN_IDENTITY)'; signing $$bin ad-hoc"; \
	    codesign --force --sign - --identifier com.odiumuniverse.$$bin $$bin; \
	  fi; \
	  codesign --verify --strict $$bin; \
	  echo "signed $$bin: $$(codesign -dvvv $$bin 2>&1 | grep -m1 Authority | cut -d= -f2- || echo ad-hoc)"; \
	done

test:
	cargo test --manifest-path cli/Cargo.toml
	cargo clippy --manifest-path cli/Cargo.toml -- -D warnings
	cargo fmt --manifest-path cli/Cargo.toml -- --check
	RWCORE_BIN=$(CURDIR)/cli/target/debug/$(RWCORE) go test ./...

vet:
	go vet ./...

clean:
	rm -f $(TUI) $(RWCORE)
