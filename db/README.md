# Database migrations

Production MySQL schema changes are versioned under `db/migrations` and use
`golang-migrate` naming. Apply the expand migration before rolling out a binary
that reads the new columns:

```bash
DB_DSN='user:password@tcp(mysql:3306)/ongrid?parseTime=true&charset=utf8mb4' make migrate-up
```

Rollback is a two-step operation: first roll the application back to a version
that does not read the added columns, then run `make migrate-down`. Local and
fresh single-node installs still use GORM AutoMigrate to initialize an empty
database.
