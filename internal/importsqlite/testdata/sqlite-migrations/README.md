# The SQLite migrations, kept as a fixture

These are the 18 migration files that built Remedy's SQLite database, copied byte for byte from
`internal/store/migrations/` before the server moved to PostgreSQL (`docs/specs/2026-10-22-postgres-only-design.md`,
decision 6). Applied in name order to an empty SQLite file, they give the SQLite schema 018, the schema the importer
(`cmd/remedy-import-sqlite`, decision 9) reads.

They are test data only:

- the importer's tests build their SQLite fixture from them;
- `internal/store`'s schema test applies them to an in-memory SQLite and compares the result with the PostgreSQL baseline
  (`internal/store/migrations/001_baseline.sql`): tables, columns, defaults, foreign keys and indexes.

They are never executed against PostgreSQL, and nothing in the server embeds them. Do not edit them: they must stay what
the released SQLite versions ran. They go when the importer goes.
