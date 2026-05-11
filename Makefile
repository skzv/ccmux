.PHONY: build install run test lint clean fmt vet daemon tui

BIN_DIR    := bin
INSTALL_DIR := $(HOME)/.local/bin
LDFLAGS    := -s -w -X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build: $(BIN_DIR)/ccmux $(BIN_DIR)/ccmuxd

$(BIN_DIR)/ccmux: $(shell find cmd/ccmux internal -type f -name '*.go' 2>/dev/null) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ccmux ./cmd/ccmux

$(BIN_DIR)/ccmuxd: $(shell find cmd/ccmuxd internal -type f -name '*.go' 2>/dev/null) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/ccmuxd ./cmd/ccmuxd

install: build
	@mkdir -p $(INSTALL_DIR)
	cp $(BIN_DIR)/ccmux  $(INSTALL_DIR)/ccmux
	cp $(BIN_DIR)/ccmuxd $(INSTALL_DIR)/ccmuxd
	@echo "Installed to $(INSTALL_DIR). Make sure it's on your PATH."

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
