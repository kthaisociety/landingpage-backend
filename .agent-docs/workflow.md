# Backend Workflow

## Setup

- Start local dependencies: `docker compose up -d`
- Run the API: `go run cmd/api/main.go`
- Run all Go tests: `go test ./...`
- Run a smaller test slice: `go test ./internal/...`
- Tidy modules after dependency changes: `go mod tidy`

Docker compose starts:

- PostgreSQL on `localhost:5432`
- Redis on `localhost:6379`

## Code Conventions

- Run `gofmt` on changed files.
- Keep handlers thin when practical.
- Reuse the existing `internal/` package boundaries.
- Extend current handlers/models instead of creating parallel abstractions for the same resource.
- Register new endpoints through the existing handler pattern unless there is a strong reason not to.

## Verification

Minimum checks for most backend changes:

- `go test ./...`
- run the API locally if startup, routing, auth, migration, or integration behavior changed

Useful manual checks:

- confirm the service boots with the expected env file
- confirm `/api/v1/health` responds successfully
- verify CORS and cookie behavior after auth/session changes
- verify alias routes such as `/api/v1/jobs` and `/api/v1/jobs/:id` if affected

## Testing Notes

The main automated surface is Go `*_test.go` files. There are also ad hoc Python scripts under `internal/handlers/tests` and `api_black_box_tests`, but they require a running API and Python dependencies such as `requests`, `PyJWT`, and `python-dotenv`.

Prefer adding or updating Go tests first when handler behavior changes.
