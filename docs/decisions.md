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
the rest of the program sees. `internal/config/load.go` is the only file that
imports Viper.

**Why.** Layering embedded defaults, an optional file, and environment overrides
is genuinely fiddly, and Viper does it. What it should not do is reach the rest
of the program: `config.Config().GetString("server.read_timeout")` at a call
site has no compiler check, no rename safety, and no way to discover what keys
exist except by grepping.

**What it costs.** One direct dependency that brings twelve indirect ones — by
some distance the largest thing in `go.sum`, in a repo that is otherwise
standard library. That trade is worth naming, and it is the most likely thing in
here for a fork to disagree with.

**To swap it.** `Load()` in `internal/config/load.go` is the entire surface.
Replacing Viper with koanf, `envconfig`, or hand-rolled YAML plus `os.Getenv` is
a rewrite of that one function; the `Config` struct, `Validate`, `Secret`, and
every call site stay exactly as they are. The tests in `load_test.go` describe
the behavior the replacement has to reproduce, so they double as its
specification.

---

## setup.sh instead of make rename

**The decision.** Renaming a fork is `./setup.sh github.com/you/yourthing`, a
shell script at the repo root, rather than a `make rename` target. It rewrites
the module path and the five places the bare service name is baked in, then
runs `go build ./...`. It never prompts, deletes nothing, and is a no-op when
the module already matches.

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

**Why a failed build is not rolled back.** Everything that can be checked
before writing is — every anchor, and that Go is installed — so a failure there
leaves the tree untouched. The `go build` at the end is different: it is the
proof, and it can only run on the renamed tree. When it fails, the compile
error names files in that tree, which is where someone has to read it, so the
rename is kept and the message says how to undo it. The undo is git: the script
only rewrites tracked files (`git grep`, not `grep -r`, which is also what keeps
an untracked note from being edited), so `git restore .` is complete. Staging
the rename in a copy or backing up every file would put a small transaction
manager in a POSIX shell script that every fork inherits, to duplicate what git
already does.

**Why it does not reset git history.** Offering `rm -rf .git && git init`
behind a prompt was considered and rejected. A prompt is a hang for anything
non-interactive, and an agent that does answer it answers it blindly; the
alternative — a flag — is a destructive default one typo away from deleting the
history of the wrong repo. The saving is one `git init`. Because the script
neither prompts nor destroys, nobody has to read it before running it, which is
worth more than the step it would save.

**To swap it.** It is one self-contained file with no dependency on the rest of
the repo. A fork that would rather have `make rename` back can lift the two
substitution passes straight out of it.

---

## Load-bearing details that look removable

### The embedded defaults must list every key

`defaults.yaml` lists every key even where the zero value would do, and
`load.go` reads it before anything else. That read is what registers the keys
with Viper — and `AutomaticEnv` only reaches keys Viper already knows about when
`Unmarshal` runs. Remove a key from the file and the service still starts, still
uses the zero value, and silently ignores the environment variable for it.

`TestLoadEnvOverridesDefault` exists for this and nothing else.

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
cannot reach them, and clear every `SERVICE_*`, `SERVER_*`, `LOG_*` and
`CONFIG_FILE` variable inherited from the shell or the CI image. The first
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
