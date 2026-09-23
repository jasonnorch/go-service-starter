# Rewritten by ./setup.sh, which anchors on this line's exact shape. Keep it a
# plain literal assignment on one line.
APP_NAME := go-service-starter
BUILD_DIR := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build clean deps lint verify verify-without test run
.PHONY: test-integration up down # module:postgres+cache

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

# module:postgres+cache begin
# The containers in compose.yaml. --wait blocks until their healthchecks pass,
# so whatever runs next never races a server that is still starting.
up:
	docker compose up -d --wait

down:
	docker compose down

# Tests behind the `integration` build tag, against the containers `make up`
# starts. Kept out of `make test` and `make verify` so both stay hermetic. The
# URLs default to compose's servers and can be overridden from the shell; they
# are exported for this target only, so `make run` never inherits them.
#
# The markers sit on their own lines: make keeps the space before a trailing
# `# comment` as part of a variable's value, which corrupts a URL.
# module:postgres begin
test-integration: export POSTGRES_URL ?= postgres://app:app@localhost:5432/app?sslmode=disable
# module:postgres end
# module:cache begin
test-integration: export CACHE_REDIS_URL ?= redis://localhost:6379/0
# module:cache end
test-integration: up
	go test -race -count=1 -tags integration ./...
# module:postgres+cache end

# golangci-lint defaults to reporting at most 50 issues per linter and 3 per
# identical message, silently. Turning both off matters more than it sounds: a
# capped count reads as a backlog you can finish and then cannot.
#
# --build-tags integration lints the integration tests too. They only run
# against a database, so without it they would go unchecked until they broke.
LINT_FLAGS := --timeout 10m --max-issues-per-linter=0 --max-same-issues=0 --build-tags integration

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
	go vet -tags integration ./... || failed=1; \
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

# Removes each module with `./setup.sh --without`, one at a time and then all
# together, in a scratch copy of the tree, and checks that what remains passes
# what the fork's own `make verify` would: gofmt, setup.sh --check, vet, lint
# and tests. This is what makes --without trustworthy — every removal a fork
# can make is exercised here first, rather than in the fork.
#
# It copies the working tree, uncommitted changes included, so it tests what is
# on disk. It is separate from verify only because it is slower; CI runs it on
# every pull request. Asking setup.sh which modules are present, rather than
# listing them here, keeps it correct in a fork that has already removed some.
verify-without:
	@modules="$$(./setup.sh --modules)"; \
	if [ -z "$$modules" ]; then echo "no modules to remove"; exit 0; fi; \
	runs="$$modules"; \
	if [ "$$(printf '%s\n' "$$modules" | wc -l)" -gt 1 ]; then \
		runs="$$runs $$(printf '%s' "$$modules" | tr '\n' ',')"; \
	fi; \
	failed=0; \
	for m in $$runs; do \
		printf '\033[2m→ without %s\033[0m\n' "$$m"; \
		dir="$$(mktemp -d)"; \
		cp -R . "$$dir/repo"; \
		( cd "$$dir/repo" && \
		  git add -A && \
		  git -c user.name=verify -c user.email=verify@localhost commit -q --allow-empty -m 'verify-without snapshot' && \
		  ./setup.sh --without "$$m" >/dev/null && \
		  unformatted="$$(gofmt -l .)" && \
		  { [ -z "$$unformatted" ] || { printf 'needs gofmt:\n%s\n' "$$unformatted"; false; }; } && \
		  ./setup.sh --check >/dev/null && \
		  golangci-lint run $(LINT_FLAGS) ./... >/dev/null && \
		  go test -race -count=1 ./... >/dev/null \
		) || { printf '\033[31m✗ without %s\033[0m\n' "$$m"; failed=1; }; \
		rm -rf "$$dir"; \
	done; \
	if [ $$failed -eq 0 ]; then \
		printf "\033[32m✓ every removal verified\033[0m\n"; \
	else \
		exit 1; \
	fi
