# Decisions

Why each opinion in this repo is what it is, what it costs, and how to change it
if you disagree. This file exists because a starter kit's real product is its
decisions — the code is just what they compile to — and because anything that
looks removable here may be load-bearing.

Read the relevant section before deleting something that seems redundant.

---

## net/http instead of Gin, chi, or echo

**The decision.** Routing is `http.ServeMux`. There is no framework.

**Why.** Since Go 1.22, `ServeMux` matches on method (`GET /users/{id}`) and
supports path wildcards, which is what most services actually used a router for.
What a framework still provides is conveniences: JSON helpers, error handling,
middleware, parameter binding.

The cost of taking those conveniences is the middleware type.
`gin.HandlerFunc` composes only with Gin. `func(http.Handler) http.Handler` is
the contract the standard library uses, that every third-party middleware
written in the last decade uses, and that this repo's middleware uses — so all
of it composes, and none of it has to be rewritten when the routing changes.

So the conveniences are rebuilt here instead — `respond`, `decode`, `Chain`,
`Group`, `Fallback`, about 330 lines of code — and the composability is kept.

**What it costs.** More code in this repo than a framework would need. Route
groups are 40 lines rather than a method call. There is no automatic parameter
binding or validation-by-struct-tag; `decode.JSON[T]` plus a `Validate()` method
is the replacement, and it is more explicit and less magic in both directions.

**To swap it.** `internal/app/app.go` is the only file that knows what the
router is; everything else takes an `http.Handler`. Replacing `http.NewServeMux`
with `chi.NewRouter` is a change to `newRouter` and nothing else — the
middleware, the responder, the decoder and the tests are unaffected, because
none of them import the router. `httpapi.Fallback` becomes unnecessary, since
chi has its own `NotFound`/`MethodNotAllowed` hooks.

---

## log/slog instead of zap

**The decision.** `*slog.Logger`, constructed in `internal/logging` and passed
as a parameter. There is no package-level logger and no `Default()`.

**Why slog.** It is in the standard library, its `Handler` interface is the
common denominator every logging backend now implements, and its structured API
is good enough that the performance argument for zap only matters at a volume
most services never reach. Choosing it costs nothing and removes a dependency.

**Why not a package-level logger.** This one is the load-bearing half. A logger
reached through a global cannot be replaced in a test without replacing it for
every other test running concurrently — which is what produces the
`NewThingWithLogger()` constructor variants that appear throughout codebases
that started with a global. Every one of those exists only so a test can inject
a logger that a parameter would have accepted in the first place.

**What it costs.** A `*slog.Logger` parameter on constructors that would
otherwise not need one. That is the visible form of a dependency that was always
there.

**To swap it.** `internal/logging/logging.go` is the only file that constructs a
handler. A zap-backed handler (`zapslog.NewHandler`), an ECS handler, or an
OpenTelemetry handler all satisfy `slog.Handler`, so the swap is that file and
nothing else. Call sites keep working, because they only ever see
`*slog.Logger`.

---

## Viper, behind a typed struct

**The decision.** Viper loads the configuration; a typed `Config` struct is what
the rest of the program sees. `internal/config/load.go` is the only non-test
file that imports Viper.

**Why.** Layering embedded defaults, an optional file, and environment overrides
is genuinely fiddly, and Viper does it. What it should not do is reach the rest
of the program: `config.Config().GetString("server.read_timeout")` at a call
site has no compiler check, no rename safety, and no way to discover what keys
exist except by grepping.

**What it costs.** One direct dependency that brings twelve indirect ones — the
largest single contributor to `go.sum`, for a job the standard library could
almost do. That trade is worth naming, and it is the most likely thing in here
for a fork to disagree with.

**To swap it.** `Load()` in `internal/config/load.go` is the entire surface.
Replacing Viper with koanf, `envconfig`, or hand-rolled YAML plus `os.Getenv` is
a rewrite of that one function; the `Config` struct, `Validate`, `Secret`, and
every call site stay exactly as they are. The tests in `load_test.go` describe
the behavior the replacement has to reproduce, so they double as its
specification — except `TestDefaultsListEveryKey`, which reads the file with
Viper because the rule it enforces is Viper's. Keep it if the replacement also
registers keys by reading defaults; delete it if not.

---

## setup.sh instead of make rename

**The decision.** Renaming a fork is `./setup.sh github.com/you/yourthing`, a
shell script at the repo root, rather than a `make rename` target. It rewrites
the module path and the five places the bare service name is baked in, removes
any modules named with `--without`, then runs `go build` and `go vet`. It never
prompts, deletes only the modules it is told to, and is a no-op when the tree
already matches.

**Why a root script.** The reader is a stranger, and a stranger's first move is
`ls`. `setup.sh` is visible there; `make rename` is only discoverable by
opening the Makefile or reading far enough into the README. The split is also
honest about lifetime: `setup.sh` runs exactly once in a repo's life, and
`make` is what gets run every day afterwards. There is no Go-ecosystem
convention to defer to here — `gonew` exists but is experimental and largely
unadopted, and the generator model that Python, Rust, and .NET settled on
(cookiecutter, `cargo-generate`, `dotnet new`) needs a tool installed before
the template is usable at all. A clone plus one script needs nothing.

**Why it does the name and not just the module path.** `make rename` built its
file list by grepping for the *module path*, so `Makefile`, `defaults.yaml`,
and the CI workflow — which contain only the bare name — were never opened. A
fork renamed that way still logged `service=go-service-starter` on every line
and reported it from `/info`. The rename looked complete and was not.

**Why the name substitutions are anchored.** Because a second tree-wide pass on
the bare name is not a smaller version of the first one, it is a different and
much worse operation. For a fork at `github.com/you/api` the bare name is
`api`, which is a substring of `httpapi`, of `cmd/api`, and of the word "API"
in half the prose in this repo. So each name substitution names one line in one
file — `^APP_NAME := `, the two-space `name:` key, `^# ` on the first line of
each doc, `-t <name>:ci` — and each is an error, not a silent no-op, when it
matches nothing. A substitution that quietly stops matching is the exact bug
this script was written to fix.

**Why it is checked in `make verify`.** Those anchors are pinned to the shape
of lines in other files, which makes them exactly the kind of thing a tidy-up
commit breaks without noticing. Nothing on `main` would fail: the damage would
surface once, later, in a fork, to somebody with no idea what the script was
supposed to match. `./setup.sh --check` runs the same patterns in a mode that
writes nothing, so that failure happens here instead.

**Why a failure after the first write is not rolled back.** Everything that
can be checked before writing is — every anchor, that Go is installed, and that
no tracked file has uncommitted changes — so a failure there leaves the tree
untouched. What follows can only be checked on the renamed tree: each write
fails loudly (`perl -pi` only warns when it cannot write a file and exits 0,
so it runs with `inplace` warnings made fatal), a recheck confirms every anchor
now carries the new name, and `go build` is the proof. When any of those fails,
the error names files in that tree, which is where someone has to read it, so
the rename is kept and the message says how to undo it. The undo is git: the
script only rewrites tracked files (`git grep`, not `grep -r`, which is also
what keeps an untracked note from being edited), and it refuses to start on
uncommitted tracked changes, so `git restore .` removes the rename and nothing
else. Without that refusal the hint would discard someone's work along with it.
Staging the rename in a copy or backing up every file would put a small
transaction manager in a POSIX shell script that every fork inherits, to
duplicate what git already does.

**Why it does not reset git history.** Offering `rm -rf .git && git init`
behind a prompt was considered and rejected. A prompt is a hang for anything
non-interactive, and an agent that does answer it answers it blindly; the
alternative — a flag — is a destructive default one typo away from deleting the
history of the wrong repo. The saving is one `git init`. Because the script
neither prompts nor destroys anything it was not asked to, nobody has to read it
before running it, which is worth more than the step it would save.

**To swap it.** It is one self-contained file with no dependency on the rest of
the repo. A fork that would rather have `make rename` back can lift the two
substitution passes straight out of it.

---

## Modules, and removing them with --without

**The decision.** The dependency-backed features are chosen for the fork rather
than left out. Each is a module: a package, plus the lines elsewhere that exist
only for it, marked with a `module:<name>` comment. `./setup.sh --without
<name>` removes one, at setup or any time after.

**Why include them and let forks remove them, rather than the reverse.**
Generating a module into a fork on request means its code is not in the
template's tree, so it is not compiled or tested on `main`, and it rots where
nobody is looking. Removal keeps every module in the tree where CI can see it.
Deleting code is also mechanical in a way generating it is not: a removed
module leaves nothing to configure, and it can happen whenever a fork decides,
not only on day one.

**Why markers, not a list of line numbers or patterns.** The marker sits on the
line it governs, so moving, reformatting or editing that line carries the
marker with it. There are two forms: a marker on a line removes that line, and
a begin/end pair removes a block. A marker can name several modules, joined in
one of two ways:

- **`a,b` goes when either module does.** That is how the Postgres-backed half
  of auth disappears with Postgres as well as with auth.
- **`a+b` goes only once every named module is gone,** counting modules
  removed on earlier runs. It is for things the modules share: `compose.yaml`'s
  header and `make up` must outlive either of Postgres and Redis, and go with
  the last of them.

A file that removal leaves empty is deleted, along with its directory if that
empties too. That is how a file marked from its first line to its last, like
`pgstore` or the `api_keys` migration, goes without appearing in any module's
list of paths. The comment character is whatever the file uses (`//`, `#`,
`<!-- -->`, SQL's `--`), so Go, YAML, the Makefile, CI, SQL and Markdown are
all marked the same way. HTML comments are invisible when Markdown renders.
Plain-text code blocks show the marker, which at least tells the reader the
line is removable. In a Makefile, never put a line marker on a variable
assignment: make keeps the space before a trailing comment as part of the
value.

**Why it is tested on every pull request.** A removal is only as good as its
markers, and a missed one — an unmarked import, a test fixture still naming a
deleted field — is invisible on `main`, where every module is present. It
surfaces in a fork, to someone who did not write it. `make verify-without`
removes each module in a scratch copy, then all of them together, and runs
what the fork's own `make verify` would. `./setup.sh --check` catches the other
half cheaply: a module whose files are present but which nothing marks, which
means its wiring was added without markers and would be left dangling.

**Why it deletes with rm, not git rm.** The undo for every setup.sh failure is
`git restore .`, which restores from the index. `git rm` would stage the
deletion, and the undo would silently fail to bring the files back. Only
tracked files are deleted, and the script refuses outright if a module's
directory holds untracked ones. Deleting those would be unrecoverable, and
leaving them would orphan code that still imports the removed dependency.

**What it costs.** Marker comments in files that would otherwise not need them,
and a discipline: everything outside a module's package that mentions it has to
be marked, or `verify-without` fails. The marker program is about eighty lines
of perl inside `setup.sh`.

**To swap it.** A fork that will never remove anything can ignore the markers.
They are comments. Deleting the `--without` half of `setup.sh` and the
`verify-without` target removes the machinery, and `./setup.sh --check` stops
checking markers along with it.

---

## /livez and /readyz

**The decision.** Liveness is served at `/livez` and readiness at `/readyz`.
There is no `/health`, and no `/healthz`.

**Why the trailing z.** It is a convention from Google's internal diagnostic
pages (`/statusz`, `/varz`), which Kubernetes made public. It keeps probe paths
out of the application's namespace. A service may well have a real `/health` or
`/status` resource, such as a health-records API or a job-status endpoint, but
nobody names a business route `/livez`. The suffix also marks the path as
infrastructure, meant for an orchestrator rather than a client. A fork that
"tidies" these to `/health` gives up both.

**Why /livez rather than /healthz.** `/healthz` is the older name, and
Kubernetes has deprecated it for its own API server in favor of `/livez`,
leaving `/livez` and `/readyz` as a matched pair. `/healthz` also says less: it
reads as "is everything healthy?", and that question is readiness's. Liveness
consults nothing (see "Liveness consults nothing" in AGENTS.md), and a name
that invites someone to add a dependency check to it is how an outage in one
dependency becomes a restart of every instance. `/livez` names what the probe
is for.

**To swap it.** The paths appear in three places: the routes in `newRouter`
(`internal/app/app.go`), the quiet-path list given to `RequestLogger` beside
them, and the Dockerfile `HEALTHCHECK`. Change all three together. The logger
list matters most, because a probe path missing from it is logged on every
poll, which is tens of thousands of lines a day. Anything outside the repo
that probes the old path, such as Kubernetes manifests or a load balancer
health check, must change in the same deploy, or the next restart
fails its probes.

---

<!-- module:postgres begin -->
## Postgres via pgx and goose

**The decision.** The service has a Postgres pool, opened in `app.Run` from
`internal/postgres`, with schema migrations embedded in the binary and applied
at startup by goose. goose is imported nowhere else. pgx is configured nowhere
else, but code that queries takes the `*pgxpool.Pool` directly (see below).

**Why include a database at all.** An earlier version left Postgres out on the
grounds that "everyone's version differs". For a Go HTTP service that is mostly
not true. pgx is the settled choice of driver, and most services that fork a
starter like this need a database. Leaving it out meant every fork rebuilt the
part where the patterns matter most, without guidance:

- when the pool closes during shutdown
- a readiness check that honors its deadline
- migrations that run before the server serves
- a password that never reaches a log

**Why pgx directly, not database/sql.** pgx's native interface is faster, and
it exposes Postgres types that `database/sql` flattens away. `database/sql`
buys driver portability, which no one needs from a service that has already
chosen Postgres. goose does need `database/sql`, so `Migrate` borrows the pool
through `stdlib.OpenDBFromPool` for the length of one migration run.

**Why goose.** It is the migration tool Go developers are most likely to know
already. It reads plain SQL files, which makes migrations reviewable as SQL.
It runs from an embedded filesystem through its Go API, so a deploy cannot ship
code without the schema it expects. It also has a session locker built on a
Postgres advisory lock. The package-level migration registry is turned off, so
every migration the service runs is a file in `internal/postgres/migrations`.

**Why there is no repository interface.** Handlers and stores take
`*pgxpool.Pool` and write SQL. A `UserRepository` interface in front of every
table is the abstraction every caller pays for and almost no service uses:
swapping databases rewrites the queries anyway, and tests that need real SQL
behavior need a real database, which the integration tests have.

**Why startup fails without a database.** A service that starts with no
database reports ready, takes traffic, and fails every request that needs it.
So `Open` pings within `postgres.connect_timeout`, and `app.Run` returns the
error. For the same reason, `Validate` rejects the development default URL in
production. A deploy that forgot `POSTGRES_URL` fails with a configuration
error that names the key, rather than a connection error to localhost that
looks like an outage.

**Why the pool closes on a defer, not appCtx.** `appCancel` runs before the
server drains, which is right for background workers and wrong for a pool. The
requests still in flight need their connections until `Serve` returns.

**Why migrations take a lock.** Instances of a deployment start together, and
each runs `Migrate`. Without the advisory lock they all see a new migration as
pending, all apply it, and all but one fail startup on "relation already
exists". `TestMigrateSerializesConcurrentInstances` reproduces exactly that
with the lock removed. It has to start from an already-migrated database: on a
fresh one, the instances instead contend on goose creating its version table,
which goose retries its way out of, lock or no lock. A test starting there
passes with the lock removed. Waiting instances poll every 5 seconds, which is
the cost of the lock at startup.

**What it costs.** Two direct dependencies (pgx and goose), which bring seven
indirect ones. `make run` now needs a database, so there is a `compose.yaml`
and a `make up` step, and the quickstart grew a line. Integration tests need
Docker locally and a service container in CI.

**To swap it.** A different migration tool means rewriting `Migrate` and
renaming the migration files. A different driver means rewriting `Open` and
the code that takes the pool. A different database means `internal/postgres`,
the block in `app.Run`, the `postgres` section of the config,
`compose.yaml`, and every store that queries the pool (today,
`internal/auth/pgstore`). To drop Postgres entirely,
`./setup.sh --without postgres`.

---
<!-- module:postgres end -->

<!-- module:cache begin -->
## A cache behind a small interface

**The decision.** `internal/cache` defines a three-method `Cache` interface,
plus `Ping` and `Close` for the wiring, with two implementations: Redis, and
an in-process `Memory`. `cache.backend` picks one at startup. go-redis is
imported nowhere else.

**Why an interface here when Postgres has none.** The two cases differ in the
way that matters. Caches really are swapped: Redis, Memcached, a managed
service, or nothing but a map in tests. Their shared surface is small enough to
state completely: get, set with an expiry, delete. A database's surface is SQL,
and an interface over it is either a leaky copy of SQL or a lowest common
denominator nobody wants. Memory also proves the seam holds, because it is a
second implementation that already exists, rather than an interface written for
one type in anticipation of another. The interface lives in the package rather
than at a consumer because `Open` has to return one of two types. A consumer
that needs less should still declare its own narrower interface.

**Why one contract test for both.** A cache test run against Memory is only
worth something if Memory behaves like Redis. `runContract` is the single
statement of that behavior. Memory runs it in `make test` and Redis runs it in
`make test-integration`, so a difference fails in one place instead of passing
in tests and surprising production. The contract is why Memory copies values
on Get (Redis always returns fresh bytes), and why both implementations refuse
a non-positive ttl: go-redis reads 0 as "never expire" and -1 as "keep the old
expiry", and neither is what a caller who passed one by mistake meant.

**Why the Redis URL's parse error is rewritten.** go-redis passes `net/url`'s
error through, and `*url.Error` quotes the whole URL, password included. A
malformed `CACHE_REDIS_URL` would have written the password into the log of
every failed start. `openRedis` keeps the reason and drops the URL, and
`TestOpenRedisDoesNotLeakThePasswordOnAParseError` covers each way a URL fails
to parse. The test found this: three of its five cases leaked before the fix.

**Why Memory is not the default.** Every instance has its own map, so two
instances behind a load balancer disagree about what is cached, and nothing
reports it. That is fine for tests and for a service that runs exactly one
instance, and quietly wrong for anything else. So `redis` is the default, and
choosing `memory` is a decision someone makes on purpose.

**What it costs.** One direct dependency (go-redis) and two small indirect
ones (a hash and an atomics package). A Redis container in `compose.yaml`, which `make run`
now needs, and a service container in CI.

**To swap it.** A different backend is one new type satisfying `Cache`, plus a
case in `Open`. Run `runContract` against it to prove it behaves. To drop
caching entirely, `./setup.sh --without cache`.

---
<!-- module:cache end -->

<!-- module:auth begin -->
## API keys, from config and the database

**The decision.** Requests authenticate with `Authorization: Bearer <key>`.
`auth.Require` hashes the key and looks the hash up in a `KeyStore`, then puts
an `auth.Principal` on the context. Keys come from `auth.keys` in config, and
also, when the postgres module is kept, from an `api_keys` table. `/v1/whoami`
is the example protected route. `/livez`, `/readyz` and `/info` stay public.

**Why API keys.** They are the simplest scheme that is correct for what a
service like this usually serves: other services and scripts. There are no
sessions, no token refresh, and no identity provider to run. JWT or OIDC is
the right answer for end users, and that swap is designed in (see below).

**Why only hashes are stored.** A key is 32 random bytes, so its SHA-256 is as
good as a password hash without the cost of one: there is no dictionary to try
and nothing to salt. A leaked config file or database dump reveals no usable
key. `cmd/apikey` prints a key once, with the line to store, and the key
cannot be recovered afterwards. Keys start with `sk_`, so one that leaks into
a log or a commit is recognizable to a human and to secret scanners.

**Why config and the database, and config first.** The database is where keys
are issued and revoked at runtime. Config is where the first key comes from,
since an empty table admits nobody, and where an operator's break-glass key
lives if the database is down. `auth.Stores` tries config first. A store that
errors fails the lookup rather than falling through: a database that cannot
answer must not make a revoked key look merely unknown to the next store.
`auth.keys` is one `name=sha256hex,...` string rather than a YAML list, so the
whole set fits in a single `AUTH_KEYS` environment variable.

**Why every bad key gets the same 401.** Missing, malformed, unknown and
revoked keys produce an identical body and a `WWW-Authenticate` header. A
caller probing keys learns nothing from which failure it got. A store failure
is a 500, because telling a legitimate client its key is bad when the database
is down sends them looking for the wrong problem.

**How it fits the module system.** `pgstore`, its test and its migration are
each marked `module:postgres,auth` from first line to last, so removing either
module empties them, and emptied files are deleted. With postgres removed,
`app.Run` is left with the config store alone. That is the "database if you
have one, config if you don't" behavior, produced by deletion rather than by a
runtime branch.

**What it costs.** No new dependency. A `/v1` route group that forks will
replace with their own routes.

**To swap it.** Everything after `Require` reads only `auth.Principal`. JWT or
OIDC is a new middleware that validates the token and sets the same
`Principal`, mounted where `Require` is. No handler changes. To drop auth
entirely, `./setup.sh --without auth`.

---
<!-- module:auth end -->

## Load-bearing details that look removable

### The embedded defaults must list every key

`defaults.yaml` lists every key even where the zero value would do, and
`load.go` reads it before anything else. That read is what registers the keys
with Viper — and `AutomaticEnv` only reaches keys Viper already knows about when
`Unmarshal` runs. Remove a key from the file and the service still starts, still
uses the zero value, and silently ignores the environment variable for it.

`TestDefaultsListEveryKey` guards the file itself: it walks `config.Config` and
fails on any field missing from `defaults.yaml`, or any key in the file that
matches no field. `TestLoadEnvOverridesDefault` guards the mechanism, failing if
the defaults stop being read before `AutomaticEnv` runs.

### config.example.yaml is a symlink

It points at `internal/config/defaults.yaml`. Replacing it with a copy is what
lets the example drift from the actual defaults, which is the state every
project's example config eventually reaches.

### There is no /etc search path

The reference implementation this kit was extracted from searched
`/etc/<org>/`, and its config tests read whatever the developer's machine
happened to have installed there. A changed default read back as its old value
locally and failed only in CI. Convenience of that kind is how a test stops
testing the code.

### os.Exit(run()) in main

`os.Exit` skips deferred functions. Writing `os.Exit(1)` inline at each failure
works right up until the first deferred cleanup is added, at which point every
existing exit path silently stops running it. The split makes that impossible
rather than something to remember.

### sigCh is buffered for two

`signal.Notify` drops signals rather than blocking on a full channel. The
further signal is what forces a hung shutdown to finish, so an unbuffered
channel discards exactly the signal that matters most.

### Tests isolate the environment, not just the filesystem

`internal/config`'s tests move the working directory so a stray `config.yaml`
cannot reach them, and clear `CONFIG_FILE` and every variable under a config
section's prefix (`configEnvPrefixes` in `load_test.go`, one entry per
top-level section) inherited from the shell or the CI image. The first
version did only the former, and `SERVER_PORT=31337 go test ./internal/config/`
failed on the developer's machine while passing everywhere else — the same
"a test reads the machine it runs on" failure this package was written to avoid,
reintroduced in its own test suite.

The same instinct applies to timing and ports. A test that asserts a 50ms
deadline was honored by allowing a flat second is asserting almost nothing, and
one that dials a released ephemeral port and requires nothing to answer is
testing the host's port table rather than the code.

### Recover is inside RequestLogger, not outside

Outside, a panic unwinds past the logger before the response exists, and the
resulting 500 is never logged. Inside, the panic becomes a response first and
the access log records it like anything else.

### The global middleware wraps the mux, not each route

Applied per-route, requests that match no route bypass the whole stack: no
request ID, no security headers, no log line. That is the shape of a
path-scanning attempt, and it is exactly what should not be invisible.
`RequestLogger`'s quiet list handles probe noise instead.

### respond.JSON marshals into a buffer first

The status line cannot be taken back. Encoding straight to the `ResponseWriter`
means a value that fails to marshal has already sent `200` and a partial body,
leaving the client parsing truncated JSON with no indication anything went
wrong.

### The recorder implements Unwrap, Flush and FlushError

`http.ResponseController` reaches `Hijack` and the per-request deadlines through
`Unwrap`. Without it, wrapping the `ResponseWriter` for the access log silently
breaks WebSocket upgrades — the classic cost of access-log middleware.

Flushing needs more than `Unwrap`, and this took two attempts to get right.
Unwrapping alone lets `ResponseController` reach past the recorder and flush the
writer underneath, which commits the response and its status without the
recorder ever knowing. Everything downstream then believes nothing has been
sent: the access log reports a status the client may not have received, and
`Recover` appends an error envelope onto a body already in flight. So
`FlushError` intercepts and marks the response started at 200 — which is exactly
what net/http does on a flush before any write.

`Flush` exists alongside it for the older and still more common idiom,
`if f, ok := w.(http.Flusher); ok`. Without it that assertion fails against the
wrapper and the branch is skipped: the handler streams nothing, reports no
error, and looks correct.

### Recover wraps the writer when nothing else has

Whether the response has already begun is the one thing `Recover` must know, so
it cannot be delegated to whether some other middleware happened to wrap the
writer first. With `RequestLogger` installed outside, its recorder is reused;
used alone — which this kit invites, since everything in it composes — `Recover`
wraps the writer itself.

Without that, a handler that writes part of a body and then panics produces
`{"partial":true{"error":{"code":"internal_error",…}}` — an unparseable
response, from the middleware whose entire job is to prevent one.

### Serve watches the context as well as the signal

A caller holding a cancelable context expects cancelling it to stop the thing it
was passed to. A `Serve` that watched only signals would accept a context and
quietly ignore it, which is worse than never taking one: every caller has reason
to believe cancellation works. It is also how a test stops the server without
raising a signal at the whole process.

The force-close count is taken from what the operator has actually done, not
from what started the shutdown. A signal-initiated shutdown needs one more
signal to force; a context-initiated one needs two, because the first has not
been pressed yet. Counting it as the second press would abandon the drain on a
single Ctrl-C — or on the routine SIGTERM an orchestrator sends during a
rollout. Either way the rule an operator sees is the same: one press asks, two
presses insist.

### The readiness deadline is enforced, not offered

`run` does not wait for every check goroutine. Waiting makes the timeout
advisory: a check that ignores its context — a driver with its own timeout, a
library that forgot one — holds `/readyz` open for as long as it likes and then
reports success, answering 200 long after it should have answered at all. An
orchestrator polling every two seconds does not care what the answer would
eventually have been.

Anything still running at the deadline is reported failed and left to finish.
The results channel is buffered per check, so a late send never blocks and no
goroutine is stranded.

Each check also runs under its own recovery. A check panics in a goroutine of
its own, outside the request's, where no HTTP middleware can reach it — so
without that, a dependency that panics on a bad connection ends the process
instead of turning readiness red. A nil check is the same hazard and is caught
earlier, at `Register`.

### Validate's message is published to the client

`decode.JSON` puts the validator's error text straight into the response, which
makes it a public API surface. That is deliberate: a 422 that does not say which
field is wrong tells the caller nothing, and a fixed generic message would make
the `Validator` interface pointless.

The constraint that comes with it is documented on `Validator` itself —
`Validate` describes the request and nothing else. Anything consulting a
database, an upstream, or process state belongs in the handler, where failure is
a logged 500 rather than a published 422.

`decode.JSON` also rejects a `null` body outright. It is valid JSON, four
characters long, and decodes into a pointer `T` as nil — which then satisfies
`Validator` as a typed nil and panics inside a pointer-receiver `Validate`, or
reaches the handler to be dereferenced there. Either way one client crashes a
handler at will.

### Group prefixes only the path

ServeMux patterns are `[METHOD ][HOST]/[PATH]`. Prefixing anything but the path
produces a pattern that registers successfully and then matches nothing:
`GET example.com/users` under a `/v1` group would become
`GET /v1/example.com/users`, a legal route no request can reach.

### Readiness reports which check failed, never why

A readiness endpoint is routinely reachable from further away than its authors
assume, and one that prints `dial tcp 10.0.1.7:5432: connection refused` is a
free map of the infrastructure behind it. Whoever is debugging a failing probe
has the logs; whoever is scanning for a way in does not.

### /info does not serve the whole config

`config.Config` carries `json` tags precisely so a *protected* admin endpoint
can marshal it directly, with `Secret` fields redacting themselves. `/info` is
unauthenticated, so it reports name, version, environment and uptime only.
Publishing a service's full configuration is not a default anyone should inherit
by forking.

---

## Known limitations

- **No hot config reload.** Viper's `WatchConfig` exists; wiring it means
  deciding what a mid-flight change to a timeout means for in-flight requests,
  and that answer is service-specific.
- **No `-config` flag.** `$CONFIG_FILE` covers the same ground with less
  surface. Add one if your deployment expects it.
- **`RequestLogger` logs `r.RemoteAddr`.** Behind a proxy that is the proxy.
  Trusting `X-Forwarded-For` instead requires knowing the proxy is the only way
  in — anyone can send that header — so the decision is left to whoever deploys
  this.
- **`Fallback` enumerates methods.** There is no way to ask a `ServeMux` what it
  has registered, so `probedMethods` is a fixed list. A route using a method
  outside it gets a 404 instead of a 405.
