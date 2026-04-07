# CLAUDE.md

## Project Overview

Backend for the KTH AI Society site. This package is a Go 1.23 service built with Gin and GORM, backed by PostgreSQL and Redis, and integrated with Google OAuth, Mailchimp, Amazon SES, and Cloudflare R2-style object storage settings.

Critical current constraint: startup initializes OAuth, Mailchimp, SES, sessions, and schema migration eagerly. Database settings alone are not enough for a clean local boot.

## Setup Commands

- Start local dependencies: `docker compose up -d`
- Run the API: `go run cmd/api/main.go`
- Run all Go tests: `go test ./...`

## Required Environment

- `GOOGLE_CLIENT_ID`
- `GOOGLE_CLIENT_SECRET`
- `MAILCHIMP_API_KEY`
- `SES_SENDER`

## Working Rules for Claude

- Do not remove or bypass startup checks for OAuth, Mailchimp, or SES unless the task explicitly includes changing the boot contract.
- Be careful with schema changes because `AutoMigrate` runs automatically on startup.
- When changing auth/admin behavior, inspect middleware and handler usage together.
- When changing blob or email flows, account for external service credentials and side effects.

## Verification

- `go test ./...`
- Run the API locally if the change affects startup, routing, auth, migrations, or integrations

## Additional Context

- `.agent-docs/architecture.md`: package layout, route behavior, and runtime notes
- `.agent-docs/env.md`: backend environment variables and boot constraints
- `.agent-docs/workflow.md`: setup, coding conventions, verification, and testing notes
