# Rewritten by ./setup.sh, which anchors on this line's exact shape. Keep it a
# plain literal assignment on one line.
APP_NAME := go-service-starter
BUILD_DIR := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build clean deps lint verify test run

all: verify build

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(APP_NAME) ./cmd/api/

run: build
	./$(BUILD_DIR)/$(APP_NAME)

clean:
	rm -rf $(BUILD_DIR)

deps:
	go get -u ./...
	go mod tidy

test:
	go test -race -count=1 ./...

# golangci-lint defaults to reporting at most 50 issues per linter and 3 per
# identical message, silently. Turning both off matters more than it sounds: a
# capped count reads as a backlog you can finish and then cannot.
LINT_FLAGS := --timeout 10m --max-issues-per-linter=0 --max-same-issues=0

# Config lives in .golangci.yml, copied from the fleet repo
# (~/development/agentic/lint/go/.golangci.yml). It is copied rather than
# symlinked so CI and anyone cloning the repo gets it too; re-copy with
# bin/lint-apply.sh in that repo rather than editing this one in place.
lint:
	golangci-lint run $(LINT_FLAGS) ./...

# The one command that answers "did it work?". Every repo in the fleet has a
# `verify` target with the same three properties, whatever the language, so
# nobody — human or agent — has to work out the incantation for this repo:
#
#   1. Non-destructive. No containers, no volumes, no network mutation, and no
#      writes to tracked files. Safe on a cold clone and safe in CI.
#   2. Exits non-zero on any failure.
#   3. Runs everything before reporting, so one run shows every problem rather
#      than the first one.
#
# Deliberately does NOT run `go mod tidy`. A target that rewrites go.mod while
# claiming to verify can turn a passing run into a dirty tree, which is a rude
# surprise in CI and a confusing one locally. `go mod tidy -diff` reports the
# drift without causing it.
#
# Lints the whole tree rather than only new findings: this repo starts with no
# backlog, and the point of a starter is that it is clean from the first commit.
#
# It also runs `./setup.sh --check`, so a reformatted line that the rename
# anchors on fails here rather than silently, once, in somebody's fork.
verify:
	@failed=0; \
	printf '\033[2m→ setup.sh --check\033[0m\n'; \
	./setup.sh --check || failed=1; \
	printf '\033[2m→ gofmt\033[0m\n'; \
	unformatted="$$(gofmt -l . || true)"; \
	if [ -n "$$unformatted" ]; then \
		printf '\033[31m✗ needs gofmt:\033[0m\n%s\n' "$$unformatted"; failed=1; \
	fi; \
	printf '\033[2m→ go mod tidy -diff\033[0m\n'; \
	go mod tidy -diff || failed=1; \
	printf '\033[2m→ go vet\033[0m\n'; \
	go vet ./... || failed=1; \
	printf '\033[2m→ golangci-lint\033[0m\n'; \
	golangci-lint run $(LINT_FLAGS) ./... || failed=1; \
	printf '\033[2m→ go test -race\033[0m\n'; \
	go test -race -count=1 ./... || failed=1; \
	echo ""; \
	if [ $$failed -eq 0 ]; then \
		printf "\033[32m✓ verify passed\033[0m\n"; \
	else \
		printf "\033[31m✗ verify failed\033[0m\n"; \
		exit 1; \
	fi
