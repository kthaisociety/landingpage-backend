# JobListingHandler

## Overview

`JobListingHandler` manages job listing CRUD operations and listing retrieval for the backend API.
It registers routes under `/joblistings`, with several admin-only endpoints protected by `middleware.RoleRequired(h.cfg, "admin")`.

## Registered Endpoints

### `POST /joblistings/admin/new`

- Purpose: Create a new job listing.
- Access: Admin only.
- Request body: JSON matching `models.JobListing`.
- Response:
  - `202 Accepted` with `{"message":"success","id": <job id>}` on success.
  - `400 Bad Request` when JSON binding fails.
  - `500 Internal Server Error` when the database insert fails.

### `PUT /joblistings/admin/update`

- Purpose: Update an existing job listing.
- Access: Admin only.
- Query parameters:
  - `id` (UUID): The job listing ID to update.
- Request body: JSON with updated `models.JobListing` fields.
- Response:
  - `200 OK` with `{"message":"Success"}` on success.
  - `400 Bad Request` when `id` is missing or invalid, or JSON binding fails.
  - `404 Not Found` when the job listing cannot be found.
  - `500 Internal Server Error` when the update fails.

### `DELETE /joblistings/admin/delete`

- Purpose: Delete a job listing by ID.
- Access: Admin only.
- Query parameters:
  - `id` (UUID): The job listing ID to delete.
- Response:
  - `200 OK` with `ok` on success.
  - `400 Bad Request` when `id` is missing.
  - `500 Internal Server Error` when the delete operation fails.

### `POST /joblistings/admin/full`

- Purpose: Create a job listing and optionally create a company with logo upload.
- Access: Admin only.
- Request format: multipart/form-data
  - `job` file: JSON payload describing the job listing.
  - `logo` file: optional company logo file upload.
- Behavior:
  - Parses `job` JSON from the multipart form.
  - Builds a `models.JobListing` from flexible payload fields.
  - Uses `models.NewCompany(...)` to resolve or create the company.
- Response:
  - `202 Accepted` with `{"success":"ok"}` on success.
  - `400 Bad Request` when `job` JSON is missing or invalid.
  - `500 Internal Server Error` when company creation or job listing creation fails.

### `GET /joblistings/all`

- Purpose: Retrieve all job listings in a summarized format.
- Access: Public.
- Response:
  - `200 OK` with a JSON array of `SmallJobListing` objects.
- Behavior:
  - Joins `job_listings` with `companies` to include company name.
  - Orders listings from newest to oldest by creation time, with ID as a deterministic tie-breaker.
  - Returns fields: `id`, `title`, `company`, `salary`.

### `GET /joblistings/job`

- Purpose: Retrieve a single job listing by ID.
- Access: Public.
- Query parameters:
  - `id` (UUID): The job listing ID to fetch.
- Response:
  - `200 OK` with the full `models.JobListing` JSON.
  - `400 Bad Request` when `id` is missing.
  - `404 Not Found` when the job listing cannot be found.

### `POST /joblistings/click`

- Purpose: Record a click on a job listing's "Apply" button/link. A simple atomic counter (`apply_click_count`), not an event log — intended for jobs where the application process happens off-site (company URL/email) and we'd otherwise have no visibility into applicant engagement.
- Access: Public, rate-limited via `middleware.ClickRateLimit()` (its own Redis key prefix/threshold, separate from the general `middleware.RateLimit()` used by form submissions). The rate-limit key is derived from the raw TCP peer address (`RemoteAddr`), not `gin.Context.ClientIP()` — this engine never configures `SetTrustedProxies`, so `ClientIP()` would honor a client-supplied `X-Forwarded-For`/`X-Real-IP` header unconditionally, letting a caller spoof a new IP per request and bypass the quota to inflate this company-facing metric.
- Query parameters:
  - `id` (UUID): The job listing ID whose click count to increment.
- Response:
  - `204 No Content` on success.
  - `400 Bad Request` when `id` is missing or invalid.
  - `404 Not Found` when the job listing cannot be found.
- Behavior: increments `apply_click_count` with an atomic `UPDATE ... SET apply_click_count = apply_click_count + 1`, avoiding a read-modify-write race under concurrent clicks. The current count is returned via `GET /joblistings/job` and `GET /joblistings/all` (field `applyClickCount`).

## Notes

- The admin routes are grouped under `/joblistings/admin` and require admin role enforcement.
- The `SingleUpload` endpoint is intended to make it easier to create job listings with file uploads and company creation.
- `GetAllListings` returns a slimmed view for listing results rather than the full job model.
