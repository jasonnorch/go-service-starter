# go-service-starter

A Go HTTP service starter built on the standard library. Opinionated about the
things that are tedious to get right — configuration, shutdown, error responses,
observability — and deliberately swappable in the places where opinions differ.

```
git clone https://github.com/jasonnorch/go-service-starter yourthing
cd yourthing
make rename MODULE=github.com/you/yourthing
make verify && make run
```

`make verify` passes on a fresh clone with no config file, no environment
variables, and nothing else running. Once the module cache is warm the tests
need no network either — they start no containers and reach nothing outside the
process.

## What you get

| | |
|---|---|
| **Routing** | `net/http` and `ServeMux`. No framework. |
| **Config** | Embedded defaults → optional file → environment, into a typed struct. |
| **Logging** | `log/slog`, passed as a parameter. Text or JSON. |
| **Errors** | One JSON envelope for every failure, including 404 and 405. |
| **Middleware** | Recover, request ID, security headers, body limit, access log. |
| **Health** | `/healthz` and `/readyz`, with pluggable readiness checks. |
| **Shutdown** | Signal, drain in-flight requests, force-close on a second signal. |
| **Tooling** | `make verify`, golangci-lint, GitHub Actions, multi-stage Dockerfile. |

One direct dependency — Viper — which brings twelve indirect ones. Everything
else is the standard library. If that trade does not appeal, `Load()` is
confined to a single file and the typed `Config` struct on the other side of it
does not change.

## Why stdlib and not a framework

Since Go 1.22, `ServeMux` does method matching and path wildcards, which is what
most services used a router for. What a framework still provides is
conveniences — JSON helpers, error handling, middleware — and the cost of taking
them is that `gin.HandlerFunc` composes only with Gin.

`func(http.Handler) http.Handler` is the universal contract. Every middleware in
this repo, in the standard library, and in a decade of third-party packages
composes with every other one, and none of it has to be rewritten when the
router changes. So the conveniences are rebuilt here — roughly 360 lines of
code across five files — and the composability is kept:

```go
// A handler returns an error; the status, the body, the log line and the
// request ID all follow from one place.
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) error {
    user, err := h.store.User(r.Context(), r.PathValue("id"))
    if errors.Is(err, store.ErrNotFound) {
        return respond.NotFound("no such user").Because(err)
    }
    if err != nil {
        return err // becomes a 500; the cause is logged, never sent
    }

    respond.JSON(w, h.logger, http.StatusOK, user)
    return nil
}
```

```go
// Decoding reports what to fix: the position of a syntax error, the name of an
// unknown field, the field and type of a mismatch. 413 for too large, 422 for
// parsed-but-invalid.
req, err := decode.JSON[CreateUser](w, r, 64<<10)
if err != nil {
    return err
}
```

A request type with a `Validate() error` method — either receiver — is checked
automatically, and a failure becomes a 422. **That error's text is published to
the client verbatim**, because a 422 that does not say which field is wrong
tells the caller nothing. So `Validate` describes the request and nothing else:
anything that consults a database or an upstream belongs in the handler, where a
failure is a logged 500 rather than a published 422.

Every failure comes back the same shape:

```json
{"error": {"code": "not_found", "message": "no such user", "request_id": "VR7XOJ7E6HOEMZZ3QNCRJVLFXE"}}
```

## Configuration

Three layers, each overriding the last:

1. **`internal/config/defaults.yaml`** — embedded with `go:embed`, complete and
   commented. The service runs correctly with nothing else.
2. **`./config.yaml`**, or `$CONFIG_FILE` — optional and override-only. Naming a
   file that does not exist is an error; not having one is not.
3. **Environment** — upper-case the key, replace dots with underscores:
   `server.read_timeout` → `SERVER_READ_TIMEOUT`.

`config.example.yaml` is a symlink to the embedded defaults, so the defaults,
the documentation and the example cannot drift apart.

There is no `/etc` search path and no walk up the directory tree. Both are
convenient, and both make a test's result depend on what the machine running it
happens to have installed.

`config.Load()` returns a value — no package global, no `init()`. `Validate()`
reports every problem at once, naming the config key rather than the Go field,
because a validator that stops at the first error turns a misconfigured deploy
into a sequence of restarts.

Secrets use `config.Secret`, which redacts through `fmt`, `encoding/json` and
`slog`. `Reveal()` is the only accessor, so `grep -rn '\.Reveal()'` is a
complete audit of every use.

## Layout

```
cmd/api/                 entry point; os.Exit(run()) so deferred cleanup runs
internal/app/            wiring, the route table, the middleware stack
internal/config/         typed config, embedded defaults, Secret, validation
internal/logging/        the only place a slog.Handler is constructed
internal/httpapi/        server lifecycle, Chain/Group, 404-405 fallback
internal/httpapi/respond/ the error type and every response written
internal/httpapi/decode/ JSON request bodies, with errors that say what to fix
internal/middleware/     recover, request ID, security headers, body limit, log
internal/health/         liveness and readiness with registered checks
internal/requestid/      the correlation ID's context key
```

## Endpoints

| Method | Path | |
|---|---|---|
| GET | `/healthz` | Liveness. Consults nothing — it answers "is this process working?" |
| GET | `/readyz` | Readiness. Runs every registered check; 503 if any fails. |
| GET | `/info` | Name, version, environment, uptime. |

Liveness and readiness are kept apart deliberately. A liveness probe that
consults the database restarts every instance of the service when the database
blips, turning a recovered dependency into a cold start of the whole fleet.

## Make targets

| | |
|---|---|
| `make verify` | The one that answers "did it work?": gofmt, tidy, vet, lint, `go test -race`. |
| `make run` | Build and run. |
| `make test` | Tests with the race detector. |
| `make lint` | golangci-lint over the whole tree. |
| `make rename MODULE=...` | Rewrite the module path everywhere. |
| `make deps` | Upgrade dependencies and tidy. |

## Swapping the opinionated parts

Each is confined to one file, which is what makes "swappable" true rather than
aspirational. `docs/decisions.md` has the reasoning and the instructions.

| To change | Edit |
|---|---|
| The router (chi, gorilla) | `internal/app/app.go` — nothing else knows about it |
| The log backend (zap, ECS, OTel) | `internal/logging/logging.go` — one `slog.Handler` |
| The config loader (koanf, envconfig) | `internal/config/load.go` — the only Viper import |
| The error envelope | `internal/httpapi/respond/respond.go` |

## Not included

Deliberately. Each lands as its own package plus one block in `app.Run`:

- **Postgres** — pgx, migrations, a readiness check
- **Redis** — a `Cache` interface with Redis and in-memory implementations
- **Auth and rate limiting** — API keys behind a pluggable store; a sliding-window limiter

They are absent because everyone's version differs, and a starter that guesses
wrong costs more to remove than it saved.

## License

MIT
