# Build stage. Pinned to the same Go version as go.mod, so a mismatch is a
# deliberate edit rather than something the base image decides for you.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Dependencies are copied and downloaded before the source, so the module cache
# layer survives every change that does not touch go.mod.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
# CGO_ENABLED=0 produces a static binary, which is what lets the final stage be
# alpine — or scratch — without the glibc that a cgo build would need at runtime.
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /app/service ./cmd/api/

# Runtime stage.
FROM alpine:3.21

# ca-certificates is not optional the moment the service makes an outbound HTTPS
# call, and its absence fails at runtime as an opaque x509 error rather than at
# build time.
RUN apk add --no-cache ca-certificates

# An unprivileged user, created in the image rather than assumed to exist. A
# container running as root shares that root with the host kernel, and the only
# thing standing between the two is that nothing has escaped yet.
RUN adduser -D -u 10001 appuser

COPY --from=build /app/service /usr/local/bin/service

USER appuser
EXPOSE 8080

# For docker run and Compose; an orchestrator uses /healthz and /readyz directly
# and ignores this. wget is busybox's, already present.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8080/healthz || exit 1

ENTRYPOINT ["service"]
