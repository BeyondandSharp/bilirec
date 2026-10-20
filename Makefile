SHELL := /bin/bash
export PATH := /usr/local/go/bin:/root/.cargo/bin:$(PATH)
export BILIBILI_LOGIN_MODE ?= anonymous

.PHONY: all fmt fmt-go fmt-rust lint lint-go lint-rust test test-go test-rust build build-go build-rust dev e2e image images clean

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

build-go:
	mkdir -p dist
	CGO_ENABLED=0 go build -o dist/bilirec ./cmd/backend

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

clean:
	rm -rf dist rust/target
