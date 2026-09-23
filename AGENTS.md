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
internal/postgres/         pgx pool, embedded goose migrations   # module:postgres
internal/cache/            Cache interface; Redis and in-memory  # module:cache
internal/requestid/        the correlation ID's context key
docs/decisions.md          why each opinion is what it is, and how to swap it
config.example.yaml        symlink to internal/config/defaults.yaml — never a copy
compose.yaml               local Postgres and Redis for `make run` # module:postgres+cache
```

## Commands

- `make verify` — **the one that answers "did it work?"**: gofmt, `go mod tidy
  -diff`, `go vet`, golangci-lint over the whole tree, `go test -race`.
  Non-destructive, runs everything before reporting. Run it before presenting
  work as done, and report the real output.
<!-- module:postgres+cache begin -->
- `make up` / `make down` — start and stop the compose.yaml containers.
  `make run` needs them, since startup fails without its dependencies.
- `make test-integration` — the `integration`-tagged tests against those
  containers (runs `make up` first). `make verify` does not run them and needs
  no containers, but it does compile and vet them.
<!-- module:postgres+cache end -->
- `make run` — build and run; `make test` — tests with the race detector
- `make lint` — golangci-lint alone
- `./setup.sh github.com/you/yourthing [--without <module>,...]` — rename the
  template to a fork's own module and drop modules it does not want. The first
  thing a fork runs; see below. `./setup.sh --check` verifies its substitutions
  and module markers and is part of `make verify`.
- `make verify-without` — removes each module in a scratch copy and runs the
  fork's checks on what is left. Run it after touching anything marked
  `module:`, or adding a module. CI runs it on every PR.
- `make deps` — `go get -u ./...` and tidy, only when asked to upgrade

## Forking this template

`./setup.sh github.com/you/yourthing` runs **before** `make verify`, not after.
Verifying first only proves the starter compiles, which it already did. The
script rewrites the module path through every import, plus the five places the
bare service name is baked in — `Makefile`, `internal/config/defaults.yaml`,
the `README.md` and `AGENTS.md` titles, and the CI image tag — then runs
`go build ./...` so it cannot report success on a tree that no longer builds.

`--without <module>,...` removes modules: their package, and every line
elsewhere marked `module:<name>` (see "Modules" below). It works at setup or
any time after, on a tree that is already renamed.

Four properties it guarantees, so there is no need to hedge around it:

- **It never prompts.** There is no interactive path, so it cannot hang an
  unattended run waiting on a TTY that isn't there.
- **It is idempotent.** Run against a module that already matches, it prints
  `already <module>`; naming a module already removed prints `already without
  <name>`; both exit 0. When it is unclear whether a repo has been set up,
  running it is the cheapest way to find out.
- **It checks before it writes.** Every substitution and marker is matched, Go
  is confirmed installed, and the tracked tree must have no uncommitted
  changes before any file is touched, so those failures leave the tree as it
  was. Once writing starts, every write is rechecked and the result is built
  and vetted; a failure there keeps the changes and the error says so, and
  `git restore .` undoes them, because only tracked files are ever written.
  Either way, read the error and stop rather than working around it.
- **It deletes only what `--without` names.** Only the tracked files of the
  named modules, and it refuses if one of their directories holds untracked
  files. No history reset, no self-deletion, no touching the git remote.
  Repointing `origin` is left to the person doing the fork, and the script
  only reminds them.

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
  key cannot be set by an environment variable. `TestDefaultsListEveryKey`
  fails when a `Config` field is missing from the file; without it, the
  env override for that key breaks silently.

## Safety boundaries

- No config search path outside the working directory. No `/etc`, no walking up
  the tree: both make a test's result depend on the machine running it, which is
  a bug that reproduces on one laptop and nowhere else.
- Never log a secret. Secrets are `config.Secret`, and `Reveal()` is the only
  accessor precisely so `grep -rn '\.Reveal()'` audits every use.
- Readiness responses report which check failed, never why. The reason goes to
  the log.
- Adding a dependency is a decision, not a step. Each direct dependency is
  confined to one package, and that confinement is what makes it swappable.
  Say so in the PR, and name the package it is confined to.

## Modules

A module is a dependency-backed feature a fork can remove with
`./setup.sh --without <name>`: a package, plus every line elsewhere that exists
only for it, marked with a comment. A line containing `module:<name>` goes
with the module; so does everything from `module:<name> begin` to
`module:<name> end`. A marker naming several modules, comma-separated, goes
when any of them does. The comment may be `//`, `#`, or `<!-- -->`.

A module lands as its own package plus one block in `app.Run`, which opens it,
defers its close, and registers its own readiness check with `health.Checker`.
Nothing in `internal/health` learns what the dependency is.
`internal/postgres` is the worked example. <!-- module:postgres -->

- **Mark everything outside the package that mentions it.** Wiring, config
  struct and defaults, `Validate`, test fixtures, Makefile targets, CI jobs,
  README rows. `./setup.sh --check` fails if a module's files are present but
  nothing is marked, and `make verify-without` fails if a missed line leaves
  the fork broken. Run it before opening the PR.
- **Register it in `setup.sh`**: a name in `KNOWN_MODULES` and its paths in
  `module_paths`.
- **Shared by several modules: join with `+`.** `module:postgres+cache` stays
  until every named module is gone, as `compose.yaml`'s header and `make up`
  do. `module:postgres,auth` goes with either.
- **Never put a line marker on a Makefile variable assignment.** make keeps the
  space before a trailing `# comment` in the value, and the corrupted URL fails
  far from the cause. Use a begin/end pair on lines of their own.
- **Close with a defer in `app.Run`, not on `appCtx`.** `appCancel` fires
  before the server drains, so a pool closed there disappears under requests
  still in flight.
- **Fail at startup when the dependency is unreachable.** A service that starts
  without it reports ready and fails every request that needs it.
- **Its tests that need the real thing go behind `//go:build integration`** and
  fail, not skip, when their URL is unset. `make verify` stays hermetic.
