-- module:postgres,auth begin
-- API keys for internal/auth/pgstore. Only the SHA-256 of each key is stored;
-- `go run ./cmd/apikey` generates a key and prints the row to insert.
-- Revoke a key by setting revoked_at, which keeps the row for the audit trail.

-- +goose Up
CREATE TABLE api_keys (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text        NOT NULL,
    key_hash   bytea       NOT NULL UNIQUE CHECK (octet_length(key_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

-- +goose Down
DROP TABLE api_keys;
-- module:postgres,auth end
