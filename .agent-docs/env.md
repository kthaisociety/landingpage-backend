# Backend Environment

## Effectively Required For Normal Startup

- `GOOGLE_CLIENT_ID`
- `GOOGLE_CLIENT_SECRET`
- `MAILCHIMP_API_KEY`
- `SES_SENDER`

## Other Common Variables

- `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE`
- `SERVER_PORT`
- `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`
- `ALLOWED_ORIGINS`
- `BACKEND_URL`
- `SESSION_KEY`
- `DEV_ROLE_OVERRIDES`: local dev only, e.g. `you@kthais.com=user,member,admin`; roles are applied on each Google sign-in
- `DEVELOPMENT`
- `JWT_PRIVATE_KEY`, `JWT_PUBLIC_KEY`: RS256 key pair for the member jwt cookie (old names `JWTSigningKey` / `JWTValidatingKey` still read as a fallback)
- `MAILCHIMP_USER`, `MAILCHIMP_LIST_ID`
- `SES_REGION`, `SES_REPLY_TO`
- `R2_Bucket`, `R2_Secret_Access_Key`, `R2_Access_Key_Id`, `R2_Endpoint`, `R2_Account_Id`

## Startup Gotchas

- `internal/config.LoadConfig()` fatals if Google OAuth credentials are missing.
- `internal/email.InitEmailService()` fails if `SES_SENDER` is empty.
- `internal/mailchimp.InitMailchimpApi()` fails if `MAILCHIMP_API_KEY` is empty.
- `LoadConfig()` errors if `DEV_ROLE_OVERRIDES` is set without `DEVELOPMENT_MODE=true`, or if it names an unknown role.
- `cmd/api` exits at startup if the JWT keys are missing, malformed, or not a matching pair (`config.ValidateJWTKeys`).
- Database settings alone are not enough to boot the API successfully.
