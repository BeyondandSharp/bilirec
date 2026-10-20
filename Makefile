SHELL := /bin/bash
export PATH := /usr/local/go/bin:/root/.cargo/bin:$(PATH)
export BILIBILI_LOGIN_MODE ?= anonymous

.PHONY: all fmt fmt-go fmt-rust lint lint-go lint-rust test test-go test-rust build build-go build-rust dev e2e image images images-full images-alpine images-alpine-full clean

all: build

fmt: fmt-go fmt-rust

# Only format Go files that are modified/untracked in this worktree. The
# upstream tree is not gofmt-clean under the installed Go version, so running
# `gofmt -w ./...` would create a huge unrelated diff.
fmt-go:
	files=$$(git ls-files -m -o --exclude-standard -- '*.go'); \
	if [ -n "$$files" ]; then gofmt -l -w $$files; else echo "no modified Go files"; fi

fmt-rust:
	cargo fmt --manifest-path rust/Cargo.toml --all

lint: lint-go lint-rust

lint-go:
	go vet ./...

lint-rust:
	cargo clippy --manifest-path rust/Cargo.toml --workspace --all-targets -- -D warnings

test: test-go test-rust

test-go:
	go test -short -count=1 -timeout 20m ./...

test-rust:
	cargo test --manifest-path rust/Cargo.toml --workspace

build: build-go build-rust

# Version embedded for the update check (docs/CONFIG-V2.md §8): release
# images pass ARG VERSION, local builds use `git describe`. Outside a git
# checkout the value is empty, which keeps the update check disabled.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
UPDATE_CHECK_LDFLAGS := -X 'github.com/bilirec/bilirec/pkg/updatecheck.currentVersionInjected=$(VERSION)'

build-go:
	mkdir -p dist
	CGO_ENABLED=0 go build -ldflags "$(UPDATE_CHECK_LDFLAGS)" -o dist/bilirec ./cmd/backend

build-rust:
	cargo build --manifest-path rust/Cargo.toml --workspace --release
	mkdir -p dist
	cp rust/target/release/flvrepair dist/flvrepair

# Upstream bilirec development target.
dev:
	go run ./cmd/backend

e2e:
	bash scripts/e2e.sh

image:
	./build.sh

images:
	./build.sh --all

images-full:
	./build.sh --all --full

images-alpine:
	./build-alpine.sh --all

images-alpine-full:
	./build-alpine.sh --all --full

clean:
	rm -rf dist rust/target
