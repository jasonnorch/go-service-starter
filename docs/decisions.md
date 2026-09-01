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

`signal.Notify` drops signals rather than blocking on a full channel. The second
signal is what forces a hung shutdown to finish, so an unbuffered channel
discards exactly the signal that matters most.

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

### The recorder implements Unwrap

`http.ResponseController` reaches `Flush`, `Hijack` and the per-request
deadlines through `Unwrap`. Without it, wrapping the `ResponseWriter` for the
access log silently breaks streaming responses and WebSocket upgrades — the
classic cost of access-log middleware.

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
