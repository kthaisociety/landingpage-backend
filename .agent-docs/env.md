# Backend Environment

`.env.example` lists every variable with comments. Staging and production set the same names from
`kthaisociety/deployments` (`projects/landingpage-backend/project.yaml`); see the header of `.env.example`.

## Effectively Required For Normal Startup

- `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`
- `JWT_PRIVATE_KEY`, `JWT_PUBLIC_KEY`: RS256 key pair for the member jwt cookie (PEM; real newlines or literal `\n`)
- A reachable Postgres: `DATABASE_URL`, or `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE`
- A reachable Redis: `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD` (the rate limiters panic at route setup without it)

## Other Common Variables

- `SERVER_PORT`, `SERVER_HOST`
- `BACKEND_URL` (OAuth callback base), `FRONTEND_URL` (links in emails), `ALLOWED_ORIGINS`, `TRUSTED_PROXY_HOST`
- `SESSION_KEY`
- `SECURE_COOKIE`, `COOKIE_DOMAIN`: the jwt cookie is `__Host-jwt` when `SECURE_COOKIE=true` and `COOKIE_DOMAIN` is unset (staging, production), else `jwt`
- `DEVELOPMENT_MODE`; `DEV_ROLE_OVERRIDES`: local dev only, e.g. `you@kthais.com=user,member,admin`; roles are applied on each Google sign-in
- `OAUTH_RATE_LIMIT_REQUESTS`
- `MAILCHIMP_API_KEY`, `MAILCHIMP_USER`, `MAILCHIMP_LIST_ID`
- `LUMA_API_KEY`, `LUMA_MEMBERS_TIER_ID`
- `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `SES_REGION`, `SES_SENDER`, `SES_REPLY_TO`
- `MCP_SERVICE_SECRET`, `ONBOARDING_SERVICE_SECRET`, `ONBOARDING_SERVICE_URL`
- `R2_Bucket`, `R2_Secret_Access_Key`, `R2_Access_Key_Id`, `R2_Endpoint`

## Startup Gotchas

- `internal/config.LoadConfig()` fatals if Google OAuth credentials are missing.
- `cmd/api` exits at startup if the JWT keys are missing, malformed, or not a matching pair (`config.ValidateJWTKeys`).
- `cmd/api` exits if `DATABASE_URL` is set but isn't a `postgres://` or `postgresql://` URL.
- `LoadConfig()` errors if `DEV_ROLE_OVERRIDES` is set without `DEVELOPMENT_MODE=true`, or if it names an unknown role.
- Mailchimp, Luma and SES only log a warning and stay disabled when their keys are missing.
- Without `SESSION_KEY`, a random key is generated per process, so sessions don't survive a restart.
- Database settings alone are not enough to boot the API successfully.
