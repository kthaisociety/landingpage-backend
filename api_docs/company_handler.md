# CompanyHandler

## Overview

`CompanyHandler` manages company-related routes for the backend API.
It is registered under `/company` and exposes both public and admin-level endpoints.

## Registered Endpoints

### `POST /company/admin/addCompany`

- Purpose: Create a new company record.
- Access: Intended for admin use.
- Request body: JSON matching `models.Company`.
- Response:
  - `202 Accepted` with text `Company added successfully` on success.
  - `400 Bad Request` when JSON binding fails.
  - `500 Internal Server Error` when the database insert fails.

### `DELETE /company/admin/delete`

- Purpose: Delete a company by ID.
- Access: Intended for admin use.
- Query parameters:
  - `id` (string): company ID to delete.
- Response:
  - `200 OK` with body `ok` on success.
  - `400 Bad Request` when `id` is missing.
  - `500 Internal Server Error` when the delete operation fails.

### `GET /company/getCompany`

- Purpose: Fetch a single company by ID.
- Query parameters:
  - `id` (string): company ID to retrieve.
- Response:
  - `200 OK` with the company JSON when found.
  - `400 Bad Request` when `id` is missing.
  - `404 Not Found` when no company exists with the provided ID.

### `GET /company/getAllCompanies`

- Purpose: Fetch all companies.
- Response:
  - `200 OK` with a JSON array of companies.
- Behavior:
  - Only selects `id` and `name` from the `companies` table.

### `GET /company/logo`

- Purpose: Fetch company logo binary data.
- Query parameters:
  - `id` (UUID string): logo blob ID.
- Response:
  - `200 OK` with `image/png` binary data on success.
  - `400 Bad Request` when `id` is not a valid UUID.
  - `500 Internal Server Error` when the blob lookup or S3 initialization fails.

## Notes

- The route registration creates an admin sub-group for `addCompany` and `delete`.
- `GetCompany` and `GetAllCompanies` are exposed as public routes under `/company`.
- `GetLogo` fetches the blob payload from the blob store and returns it directly as PNG image data.
