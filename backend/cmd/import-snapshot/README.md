# Offline snapshot import rehearsal

This command wraps `internal/migration.Import` for a **dedicated, disposable PostgreSQL database ending in `_test`**. It requires an explicit schema and `-apply`. It does not create a database, run migrations, activate a sender or perform a cutover.

1. Obtain a private, verified `prepare.php` snapshot outside Git and every web root. Keep the original configuration/key and upload backup separately available.
2. Create a dedicated empty rehearsal schema and apply the Go migrations to **that schema**, using the same `search_path`. Use an owner account for migration/import; the runtime role is deliberately restricted.
3. Supply the test database DSN through a private environment file or environment settings. Do not paste passwords into command arguments, logs or chat.
4. Run from `backend`:

```sh
go run ./cmd/import-snapshot -help
go run ./cmd/import-snapshot -snapshot=/private/snapshot -schema=rehearsal -apply
```

`TEST_DATABASE_URL` is the default DSN variable; `-database-env=OTHER_PRIVATE_TEST_URL` selects another variable. Database and schema checks happen before import. The importer also enforces its own target and integrity requirements. The whole operation has a 15-minute deadline by default; `-timeout=30m` is available for larger rehearsal datasets.

Uploads remain in the verified source snapshot; the CLI does not put file bytes in PostgreSQL or make the snapshot a writable runtime directory. Before activation, copy uploads to a separate private runtime volume and verify their hashes. The Go runtime must use the original source application key so imported ciphertext and textual-HMAC values remain readable. Never activate a sender during rehearsal.

Only a successful import prints the sanitized report. Errors intentionally omit credentials, paths from underlying database errors and record values. `Reused`/the import registry distinguish replay from a new import. An import report is **not** proof that every archived legacy module works in Go or that production is ready. Review unsupported tables, restored files, IDs/sequences, foreign keys, encryption and queued delivery before any later deployment.

`-apply` changes the rehearsal schema. Omitting it performs no database operation; it is not a data-compatibility dry run. Use the snapshot verifier and an actual isolated rehearsal for compatibility checks.
