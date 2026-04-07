# RegistrationHandler

## Overview

`RegistrationHandler` manages event registration operations for the backend API.
It registers routes under `/registrations`, with authentication required for all endpoints and admin-only access for management operations.
Handles user registrations for events, including status updates and attendance tracking.

## Registered Endpoints

### `GET /registrations`

- Purpose: List all registrations.
- Access: Authenticated users only.
- Response:
  - `200 OK` with array of all registrations.
  - `500 Internal Server Error` on DB error.

### `POST /registrations`

- Purpose: Create a new registration.
- Access: Authenticated users only.
- Request body: JSON matching `models.Registration`.
- Response:
  - `201 Created` with new registration.
  - `400 Bad Request` on invalid input.
  - `500 Internal Server Error` on DB error.

### `GET /registrations/:id`

- Purpose: Get a specific registration by ID.
- Access: Authenticated users only.
- URL parameters:
  - `id` (uint): Registration ID.
- Response:
  - `200 OK` with registration data.
  - `404 Not Found` if registration not found.

### `GET /registrations/my`

- Purpose: Get all registrations for the current user.
- Access: Authenticated users only.
- Response:
  - `200 OK` with user's registrations and profile info.
  - `500 Internal Server Error` on DB error.

### `GET /registrations/event/:eventId`

- Purpose: Get all registrations for a specific event.
- Access: Authenticated users only.
- URL parameters:
  - `eventId` (uint): Event ID.
- Response:
  - `200 OK` with array of registrations for the event.
  - `500 Internal Server Error` on DB error.

### `POST /registrations/register/:eventId`

- Purpose: Register the current user for an event.
- Access: Authenticated users only.
- URL parameters:
  - `eventId` (uint): Event ID.
- Behavior: Checks for existing registration, event capacity, and creates pending registration.
- Response:
  - `201 Created` with new registration.
  - `404 Not Found` if event not found.
  - `409 Conflict` if already registered.
  - `400 Bad Request` if event at capacity.
  - `500 Internal Server Error` on DB error.

### `PUT /registrations/:id/cancel`

- Purpose: Cancel a registration (user can only cancel their own).
- Access: Authenticated users only.
- URL parameters:
  - `id` (uint): Registration ID.
- Behavior: Sets status to rejected.
- Response:
  - `200 OK` with success message.
  - `403 Forbidden` if not the user's registration.
  - `404 Not Found` if registration not found.
  - `500 Internal Server Error` on DB error.

### `PUT /registrations/admin/:id`

- Purpose: Update a registration.
- Access: Admin only.
- URL parameters:
  - `id` (uint): Registration ID.
- Request body: JSON with updated registration fields.
- Response:
  - `200 OK` with updated registration.
  - `400 Bad Request` on invalid input.
  - `404 Not Found` if registration not found.
  - `500 Internal Server Error` on DB error.

### `DELETE /registrations/admin/:id`

- Purpose: Delete a registration.
- Access: Admin only.
- URL parameters:
  - `id` (uint): Registration ID.
- Response:
  - `200 OK` with success message.
  - `500 Internal Server Error` on DB error.

### `PUT /registrations/admin/:id/status`

- Purpose: Update registration status.
- Access: Admin only.
- URL parameters:
  - `id` (uint): Registration ID.
- Request body: JSON with `{"status": "pending|approved|rejected"}`.
- Response:
  - `200 OK` with updated registration.
  - `400 Bad Request` on invalid input.
  - `404 Not Found` if registration not found.
  - `500 Internal Server Error` on DB error.

### `PUT /registrations/admin/:id/attended`

- Purpose: Mark attendance for a registration.
- Access: Admin only.
- URL parameters:
  - `id` (uint): Registration ID.
- Request body: JSON with `{"attended": true|false}`.
- Response:
  - `200 OK` with updated registration.
  - `400 Bad Request` on invalid input.
  - `404 Not Found` if registration not found.
  - `500 Internal Server Error` on DB error.

## Notes

- All endpoints require JWT authentication and a registered user profile.
- Admin endpoints are under `/registrations/admin` and require "admin" role.
- `RegisterForEvent` enforces event capacity limits and prevents duplicate registrations.
- `CancelRegistration` only allows users to cancel their own registrations.
- Status values: `pending`, `approved`, `rejected`.
- Preloads related models (Event, User) in some queries for full data.