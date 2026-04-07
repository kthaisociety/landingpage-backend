# ProfileHandler

## Overview

`ProfileHandler` manages user profile operations for the backend API.
It registers routes under `/profile`, with authentication required for most endpoints and admin-only access for management operations.
Integrates with Mailchimp for newsletter subscriptions.

## Registered Endpoints

### `GET /profile/`

- Purpose: Retrieve the authenticated user's profile.
- Access: Authenticated users only.
- Response:
  - `200 OK` with profile data if exists, including `exists: true`.
  - `200 OK` with `{"userId": <uuid>, "exists": false}` if no profile found.

### `PUT /profile/`

- Purpose: Update the authenticated user's profile (creates if not exists).
- Access: Authenticated users only.
- Request body: JSON with profile fields (firstName, lastName, email, etc.).
- Behavior: Updates Mailchimp member on success.
- Response:
  - `200 OK` with updated profile on update.
  - `201 Created` with new profile on creation.
  - `400 Bad Request` on invalid input.
  - `404 Not Found` if user not found.
  - `500 Internal Server Error` on DB or Mailchimp errors.

### `POST /profile/`

- Purpose: Create a new profile for the authenticated user.
- Access: Authenticated users only.
- Request body: JSON with profile fields (firstName, lastName, email, etc.).
- Behavior: Subscribes to Mailchimp on creation.
- Response:
  - `201 Created` with new profile.
  - `400 Bad Request` on invalid input.
  - `404 Not Found` if user not found.
  - `409 Conflict` if profile already exists.
  - `500 Internal Server Error` on DB or Mailchimp errors.

### `GET /profile/admin`

- Purpose: List all profiles.
- Access: Admin only.
- Response:
  - `200 OK` with array of all profiles.
  - `500 Internal Server Error` on DB error.

### `PUT /profile/admin/:userId`

- Purpose: Update any user's profile.
- Access: Admin only.
- URL parameters:
  - `userId` (UUID): Target user ID.
- Request body: JSON with updated profile fields.
- Behavior: Updates Mailchimp member.
- Response:
  - `200 OK` with updated profile.
  - `400 Bad Request` on invalid userId or input.
  - `404 Not Found` if profile not found.
  - `500 Internal Server Error` on DB or Mailchimp errors.

### `DELETE /profile/admin/:userId`

- Purpose: Delete a user's profile.
- Access: Admin only.
- URL parameters:
  - `userId` (UUID): Target user ID.
- Response:
  - `200 OK` with success message.
  - `400 Bad Request` on invalid userId.
  - `404 Not Found` if profile not found.
  - `500 Internal Server Error` on DB error.

## Notes

- All endpoints under `/profile` require JWT authentication.
- Admin endpoints are under `/profile/admin` and require "admin" role.
- Profile creation/update automatically manages Mailchimp subscriptions.
- `UpdateMyProfile` can create a profile if it doesn't exist, similar to `CreateMyProfile`.
- `CreateMyProfile` returns conflict if profile exists, while `UpdateMyProfile` updates or creates.