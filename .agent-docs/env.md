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
- `DEVELOPMENT`
- `JWTSigningKey`
- `MAILCHIMP_USER`, `MAILCHIMP_LIST_ID`
- `SES_REGION`, `SES_REPLY_TO`
- `R2_Bucket`, `R2_Secret_Access_Key`, `R2_Access_Key_Id`, `R2_Endpoint`, `R2_Account_Id`

## Startup Gotchas

- `internal/config.LoadConfig()` fatals if Google OAuth credentials are missing.
- `internal/email.InitEmailService()` fails if `SES_SENDER` is empty.
- `internal/mailchimp.InitMailchimpApi()` fails if `MAILCHIMP_API_KEY` is empty.
- Database settings alone are not enough to boot the API successfully.
