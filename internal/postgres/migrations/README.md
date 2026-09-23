# Migrations

Schema changes, applied by `postgres.Open` at startup when
`postgres.migrate_on_start` is true (the default), under an advisory lock so
concurrent instances do not race.

Files are [goose](https://github.com/pressly/goose) SQL migrations, named
`NNNNN_description.sql` and applied in version order:

```sql
-- +goose Up
CREATE TABLE widgets (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY);

-- +goose Down
DROP TABLE widgets;
```

They are embedded in the binary, so a deploy cannot ship code without the
schema it expects. To run them as a separate deploy step instead, set
`POSTGRES_MIGRATE_ON_START=false` and use the goose CLI against this directory.

This file is here so the directory exists before the first migration does;
goose ignores anything that is not `.sql`.
