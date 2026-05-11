.PHONY: build install setup uninstall run test lint clean fmt vet daemon tui

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
else
CODESIGN =
endif

build: $(BIN_DIR)/ccmux $(BIN_DIR)/ccmuxd

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

tui run:
	go run ./cmd/ccmux

daemon:
	go run ./cmd/ccmuxd

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint: fmt vet
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed; skipping"

clean:
	rm -rf $(BIN_DIR) dist
