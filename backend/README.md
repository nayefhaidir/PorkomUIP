# UIP Meeting API (Go)

This service implements the portal API independently of Next.js. It runs on Go
1.27.3 and owns its database access and upload storage.

## Run

```sh
cd backend
export DATABASE_URL='file:./uip.db'
export AUTH_SECRET='replace-with-at-least-32-random-characters'
export UPLOAD_DIR='../uploads'
go run ./cmd/api
```

Set `DATABASE_URL` to a PostgreSQL URL (`postgres://` or `postgresql://`) or a
SQLite file URL (`file:./uip.db`, `file:/absolute/path/uip.db`, or
`sqlite:///absolute/path/uip.db`). SQLite enables foreign keys, WAL, a busy
timeout, and a single connection to serialize writes. The schema is applied at
startup. Existing PostgreSQL Prisma tables are imported idempotently into the
Go-owned schema when detected.

Set a unique `DEMO_PASSWORD` before running `go run ./cmd/api seed`; the seed
command refuses to create demo accounts if it is unset. Do not use seeded
accounts outside local development.

`UPLOAD_DIR` selects the local upload directory (default `uploads`). SMTP
credentials are encrypted at rest using `MAIL_ENCRYPTION_KEY`, a 64-character
hex-encoded 32-byte key. Configure `PUBLIC_APP_URL` to the frontend's externally
reachable base URL so email links point to the correct portal. The API's
background worker dispatches the email outbox and scheduled reminders once per
minute; no separate internal dispatch endpoint is exposed. The listener
defaults to `:8080`; set `ADDR` to override it.

For Docker deployments from the repository root, `sh scripts/deploy.sh postgres`
or `sh scripts/deploy.sh sqlite` creates a protected `.mail-encryption.env` file
if it does not exist and injects the key into the API container. Keep that file
with deployment backups; do not rotate it after SMTP credentials are saved.
For a standalone API process, provide `MAIL_ENCRYPTION_KEY` through the process
environment or secret manager.

SMTP hostnames are resolved and pinned to a single IP before connection. Private
and loopback targets are blocked by default to limit SSRF; set
`SMTP_ALLOW_PRIVATE_NETWORK=true` only when connecting to a trusted internal
relay is required.

## Validate

```sh
go test -race ./...
go vet ./...
```
