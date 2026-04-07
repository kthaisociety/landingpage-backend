# Backend Architecture

## Stack

- Go 1.23
- Gin HTTP server
- GORM
- PostgreSQL
- Redis
- Google OAuth
- Mailchimp
- Amazon SES
- Cloudflare R2-style object storage settings

## Main Folders

- `cmd/api`: main API server entrypoint
- `cmd/seed`, `cmd/reset_jobs`, `cmd/add_altaal`: one-off utilities and seeders
- `internal/config`: env/config loading
- `internal/handlers`: route registration and request handlers
- `internal/middleware`: auth, admin, and rate-limit middleware
- `internal/models`: GORM models
- `internal/email`: SES-backed email rendering and sending
- `internal/mailchimp`: Mailchimp client
- `internal/utils`: JWT and blob helpers
- `internal/database`: Redis client setup
- `api_black_box_tests`: ad hoc Python API tests

## Runtime Behavior

- The API base path is `/api/v1`.
- `cmd/api/main.go` registers handlers through the `handlers.Handler` interface.
- Startup runs `AutoMigrate` for all registered models.
- Startup also initializes OAuth, Mailchimp, email, sessions, CORS, and database connections eagerly.
- There are frontend-compatibility alias routes for `/jobs` and `/jobs/:id`.

## Change Guidance

- Be careful with schema changes because `AutoMigrate` runs on boot.
- When changing auth/admin behavior, inspect middleware and handlers together.
- When changing routes, update related frontend expectations and any stale endpoint docs.
- Keep env/config access centralized in `internal/config`.
