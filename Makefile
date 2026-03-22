BINARY  := capabot
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
PKGS    := ./cmd/... ./internal/...

.PHONY: all build build-linux build-arm test test-short test-cover lint fmt run dev migrate web web-install web-dev clean help

## all: build the binary (default)
all: build

## build: compile the capabot binary
build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) ./cmd/capabot

## build-linux: cross-compile for Linux amd64
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 ./cmd/capabot

## build-arm: cross-compile for Linux arm64
build-arm:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-linux-arm64 ./cmd/capabot

## test: run all Go tests
test:
	go test $(PKGS) -count=1 -timeout 120s

## test-short: run tests, skipping slow integration tests
test-short:
	go test $(PKGS) -count=1 -short -timeout 60s

## test-v: run tests with verbose output
test-v:
	go test $(PKGS) -count=1 -v -timeout 120s

## test-cover: run tests with HTML coverage report
test-cover:
	go test $(PKGS) -count=1 -coverprofile=coverage.out -timeout 120s
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report written to coverage.html"

## lint: run go vet
lint:
	go vet $(PKGS)

## fmt: format all Go source files
fmt:
	gofmt -w -s .

## run: build and run capabot serve
run: build
	./$(BINARY) serve

## dev: build and run in skill hot-reload mode
dev: build
	./$(BINARY) dev

## migrate: run database migrations
migrate: build
	./$(BINARY) migrate

## web: build the React web UI (output: web/dist)
web:
	cd web && npm run build

## web-install: install web UI npm dependencies
web-install:
	cd web && npm install

## web-dev: start Vite dev server with HMR
web-dev:
	cd web && npm run dev

## clean: remove build artifacts
clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64
	rm -f coverage.out coverage.html
	rm -rf web/dist

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/^## /  /'
