.PHONY: build install setup uninstall run test lint clean fmt vet daemon tui check-go

BIN_DIR    := bin
INSTALL_DIR := $(HOME)/.local/bin
LDFLAGS    := -s -w -X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
UNAME_S    := $(shell uname -s)

# On macOS, ad-hoc sign each binary so spctl/GateKeeper accepts it when
# launched from a terminal. Without this the OS adds com.apple.provenance
# to the unsigned binary and SIGKILLs it silently on exec, which manifests
# as `ccmux` exiting with no output. Launchd-managed ccmuxd is exempt, so
# this only affects direct TUI/CLI invocations.
ifeq ($(UNAME_S),Darwin)
CODESIGN = codesign --force --sign - $@ 2>/dev/null || true
GO_INSTALL_HINT = brew install go
else ifeq ($(UNAME_S),Linux)
CODESIGN =
GO_INSTALL_HINT = sudo apt install golang-go  # or your distro's equivalent / https://go.dev/doc/install
else
CODESIGN =
GO_INSTALL_HINT = https://go.dev/doc/install
endif

# check-go: friendly "install Go first" message instead of the cryptic
# "/bin/sh: go: command not found" make spits out by default. Invoked
# from every build/test/run target — clean and uninstall don't need it.
check-go:
	@command -v go >/dev/null 2>&1 || { \
		printf "\n\033[1;31m✗\033[0m \`go\` not found on PATH.\n\n"; \
		echo   "ccmux is built from source — Go 1.22+ is required."; \
		echo   "Install it with:"; \
		echo   "  $(GO_INSTALL_HINT)"; \
		echo   ""; \
		echo   "Then re-run \`make setup\` (or \`make build\`)."; \
		echo   ""; \
		exit 1; \
	}

build: check-go $(BIN_DIR)/ccmux $(BIN_DIR)/ccmuxd

$(BIN_DIR)/ccmux: $(shell find cmd/ccmux internal -type f -name '*.go' 2>/dev/null) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ccmux ./cmd/ccmux
	@$(CODESIGN)

$(BIN_DIR)/ccmuxd: $(shell find cmd/ccmuxd internal -type f -name '*.go' 2>/dev/null) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ccmuxd ./cmd/ccmuxd
	@$(CODESIGN)

install: build
	@mkdir -p $(INSTALL_DIR)
	cp $(BIN_DIR)/ccmux  $(INSTALL_DIR)/ccmux
	cp $(BIN_DIR)/ccmuxd $(INSTALL_DIR)/ccmuxd
	@echo "Installed to $(INSTALL_DIR). Make sure it's on your PATH."

# `make setup` is the one-shot for new users from a fresh clone:
# build → install to PATH → run the interactive setup wizard.
# Existing users can re-run it; the wizard is idempotent and skips
# any step whose underlying state is already good.
setup: install
	@echo
	@echo "Running ccmux setup wizard…"
	@$(INSTALL_DIR)/ccmux setup

# `make uninstall` is a thin wrapper around the real uninstaller.
# Always call `ccmux uninstall` first if you can — it handles the
# daemon, state files, and tmux chrome that this target ignores.
uninstall:
	@if command -v $(INSTALL_DIR)/ccmux >/dev/null 2>&1; then \
		$(INSTALL_DIR)/ccmux uninstall --yes || true; \
	else \
		echo "ccmux is not on PATH — removing binaries only"; \
		rm -f $(INSTALL_DIR)/ccmux $(INSTALL_DIR)/ccmuxd; \
	fi

tui run: check-go
	go run ./cmd/ccmux

daemon: check-go
	go run ./cmd/ccmuxd

test: check-go
	go test ./...

fmt: check-go
	gofmt -w .

vet: check-go
	go vet ./...

lint: fmt vet
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed; skipping"

clean:
	rm -rf $(BIN_DIR) dist
