# go-service-starter — agent guide

A Go HTTP service starter built on `net/http`. Opinionated about configuration,
shutdown, error responses and observability; deliberately swappable where
opinions differ, by confining each replaceable dependency to one file rather
than hiding it behind an abstraction everyone pays for.

This repo is a **template**. People fork it and rename the module. That changes
what "good" means here: an unused helper is not dead code waiting to be deleted,
it is part of the surface being offered — but a default that is wrong for most
forks is worse than no default at all, because it is inherited silently.

**Module:** `github.com/jasonnorch/go-service-starter`

## Where things are

```text
setup.sh                   renames the template to a fork's module; run once
cmd/api/main.go            entry point; os.Exit(run()) so deferred cleanup runs
internal/app/app.go        wiring, the route table, the middleware stack
internal/config/           typed Config, embedded defaults.yaml, Secret, Validate
internal/logging/          the only place a slog.Handler is constructed
internal/httpapi/          server lifecycle, Chain/Group, 404-405 fallback
internal/httpapi/respond/  the Error type and every response written
internal/httpapi/decode/   JSON request bodies, with errors that say what to fix
internal/middleware/       recover, request ID, security headers, body limit, log
internal/health/           liveness and readiness with registered checks
internal/requestid/        the correlation ID's context key
docs/decisions.md          why each opinion is what it is, and how to swap it
config.example.yaml        symlink to internal/config/defaults.yaml — never a copy
```

## Commands

- `make verify` — **the one that answers "did it work?"**: gofmt, `go mod tidy
  -diff`, `go vet`, golangci-lint over the whole tree, `go test -race`.
  Non-destructive, runs everything before reporting. Run it before presenting
  work as done, and report the real output.
- `make run` — build and run; `make test` — tests with the race detector
- `make lint` — golangci-lint alone
- `./setup.sh github.com/you/yourthing` — rename the template to a fork's own
  module. The first thing a fork runs; see below. `./setup.sh --check` verifies
  its substitutions still match and is part of `make verify`.
- `make deps` — `go get -u ./...` and tidy, only when asked to upgrade

## Forking this template

`./setup.sh github.com/you/yourthing` runs **before** `make verify`, not after.
Verifying first only proves the starter compiles, which it already did. The
script rewrites the module path through every import, plus the five places the
bare service name is baked in — `Makefile`, `internal/config/defaults.yaml`,
the `README.md` and `AGENTS.md` titles, and the CI image tag — then runs
`go build ./...` so it cannot report success on a tree that no longer builds.

Four properties it guarantees, so there is no need to hedge around it:

- **It never prompts.** There is no interactive path, so it cannot hang an
  unattended run waiting on a TTY that isn't there.
- **It is idempotent.** Run against a module that already matches, it prints
  `already <module>` and exits 0. When it is unclear whether a repo has been
  set up, running it is the cheapest way to find out.
- **It checks before it writes.** Every substitution is matched, and Go is
  confirmed installed, before any file is touched, so those failures leave the
  tree as it was. Once writing starts, every write is rechecked and the result
  is built; a failure there keeps the rename and the error says so, and
  `git restore .` undoes it, because only tracked files are ever written.
  Either way, read the error and stop rather than working around it.
- **It deletes nothing.** No history reset, no self-deletion, no touching the
  git remote. Repointing `origin` is left to the person doing the fork, and the
  script only reminds them.

**Never rename by hand, and never `sed` the bare service name across the
tree.** The name is a substring of real identifiers here. Fork this to
`github.com/you/api` and a tree-wide swap of `api` rewrites every
`internal/httpapi` import to `internal/httpnewname` while the directory keeps
its old name, and quietly edits the word wherever it appears in comments and
log messages. That is the failure `setup.sh` exists to prevent: each of its
name substitutions is anchored to one line in one file, and each one is an
error rather than a silent no-op when it matches nothing.

## Conventions

- **Every change goes through a PR.** Feature branch → PR into `main`, no
  exceptions for one-line fixes or config. Open it and stop; merging is Jason's.
  The PR is where review happens, and CodeRabbit only reviews PRs — anything
  pushed straight to `main` is a change nobody looked at.
- **Every PR merges as a merge commit. Never squash, never rebase.** Both
  replace the branch's commits with new ones, so SHAs referenced in comments and
  changelogs stop existing. For the one-line-per-PR view, use
  `git log --first-parent main`.
- No `Co-Authored-By`, and no harness footers or session links in commits or PR
  bodies. Fleet attribution rules apply: `Assisted-by:` naming the model, and
  that is the whole record.
- `gofmt` is not optional, and `go mod tidy` before committing.
- Keep `README.md` in sync when a change touches endpoints, config keys, make
  targets, or the layout. It is the repo's front page and the reason anyone
  forks it.
- Table-driven tests. Test the behavior that would hurt if it broke, not every
  function — and say in the test's name or a comment what the failure would be.
- **Check that a new test fails without its fix.** Break the code, watch it go
  red, put it back. Tests written for a bug you have just fixed pass for the
  wrong reason more often than seems possible — during this repo's own review,
  one passed because a different guard caught the same case, and another
  asserted on a word that appeared in an unrelated error message.
- **A test must not depend on the machine it runs on.** Not the environment, not
  a released port, not a flat wall-clock bound standing in for a configured
  timeout. All three have already produced a green suite that proved nothing
  here.

## Rules that are load-bearing

Each of these is a decision with a cost behind it. `docs/decisions.md` has the
reasoning; this is the short version, so nothing here is "simplified" by
accident.

- **No package-level logger, config, or `init()`.** All three make a package
  untestable in isolation and are what force `*WithLogger()` constructor
  variants. Pass values as parameters.
- **`app.Run` is the only holder of `*config.Config`.** Every other package
  takes the one sub-struct it needs, so its configuration surface is visible in
  its constructor signature.
- **Middleware returns `func(http.Handler) http.Handler`**, never a named type
  from this repo. That is the portability claim, and it is also what keeps
  `internal/middleware` free of an import of the router.
- **`respond.Fail` and `asError` are the only places a failure becomes a
  status.** Map a new domain error by adding a case to `asError`, not with an
  `errors.Is` ladder in a handler.
- **A client-safe `Message` and a server-side `cause` are different things.**
  Never put an internal error's text in a response.
- **`Recover` is mandatory and belongs inside `RequestLogger`.** Without it, a
  panicking handler drops the connection rather than returning 500. Outside the
  logger, the resulting 500 is never logged. It does not depend on that
  ordering to work, though — it wraps the writer itself when nothing else has.
- **Anything that wraps a `ResponseWriter` must forward every capability it
  hides.** `Unwrap` for `ResponseController`, and `Flush`/`FlushError` for
  both it and the `w.(http.Flusher)` assertion. A wrapper that swallows a
  capability makes streaming silently do nothing.
- **Honor `context.Context`.** A function that takes one and ignores it is worse
  than one that never took it, because every caller has reason to believe
  cancellation works. The same goes for deadlines that are offered to callees
  but not enforced on them.
- **A `Validate` error message is published to the client verbatim.** It may
  describe the request and nothing else — no database, upstream, or process
  state. See the contract on `decode.Validator`.
- **Liveness consults nothing.** A liveness probe that checks a dependency
  restarts every instance when that dependency blips.
- **`config.example.yaml` is a symlink.** Replacing it with a copy is what lets
  the example drift from the defaults.
- **The embedded `defaults.yaml` must list every key**, even when the zero value
  would do. Reading it is what registers the key with Viper, and an unregistered
  key cannot be set by an environment variable. There is a test for this;
  removing a key from the file breaks env overrides silently.

## Safety boundaries

- No config search path outside the working directory. No `/etc`, no walking up
  the tree: both make a test's result depend on the machine running it, which is
  a bug that reproduces on one laptop and nowhere else.
- Never log a secret. Secrets are `config.Secret`, and `Reveal()` is the only
  accessor precisely so `grep -rn '\.Reveal()'` audits every use.
- Readiness responses report which check failed, never why. The reason goes to
  the log.
- Adding a dependency is a decision, not a step. This repo has one direct
  dependency and the count is part of what it is offering — say so in the PR.

## Adding a dependency-backed feature

The deferred work (Postgres, Redis, auth, rate limiting) each lands as its own
package plus one guarded block in `app.Run`, and registers its own readiness
check with `health.Checker`. Nothing in `internal/health` learns what a Postgres
pool is.
