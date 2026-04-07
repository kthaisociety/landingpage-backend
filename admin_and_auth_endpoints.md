# Admin and Auth API Endpoints Documentation

This document outlines the API endpoints defined in `admin_handler.go` and `auth_handler.go` for the KTHAIS backend.

## Admin Endpoints

All admin endpoints are grouped under `/admin` and require:
- Authentication via JWT
- Admin role

### GET /admin/users
- **Description**: Retrieves a list of all users in the system.
- **Response**: JSON array of user objects.

### GET /admin/users/uuid
- **Description**: Retrieves the UUID of a user by their email address.
- **Query Parameters**: `email` (required) - The email address of the user.
- **Response**: JSON object with `user_id`.

### GET /admin/users/:id
- **Description**: Retrieves a specific user by their UUID.
- **Path Parameters**: `id` (UUID) - The user's unique identifier.
- **Response**: JSON user object.

### GET /admin/users/filter
- **Description**: Retrieves a filtered list of users. All parameters are optional and combined with AND logic.
- **Query Parameters**:
  - `has_profile=true|false` — filter by whether a profile record exists
  - `registered=true|false` — filter by whether the profile is marked as registered
  - `created_after=YYYY-MM-DD` — filter users created on or after this date
  - `created_before=YYYY-MM-DD` — filter users created on or before this date
  - `team_id=<uuid>` — filter users who are members of the specified team
  - `project_id=<uuid>` — filter users who are members of a team linked to the specified project
- **Response**: JSON array of user objects.

### GET /admin/listadmins
- **Description**: Retrieves a list of all users with admin role.
- **Response**: JSON array of admin user objects.

### GET /admin/checkadmin
- **Description**: Checks if the authenticated user has admin role.
- **Response**: JSON object with `is_admin` boolean.

### POST /admin/users
- **Description**: Creates a new user.
- **Request Body**: JSON with `email`, `provider` (optional), `roles` (optional array).
- **Response**: JSON of the created user object.

### PUT /admin/setadmin
- **Description**: Promotes a user to admin role.
- **Request Body**: JSON with `user_id` (UUID).
- **Response**: JSON confirmation with `user_id` and `status`.

### PUT /admin/unsetadmin
- **Description**: Demotes a user from admin role.
- **Request Body**: JSON with `user_id` (UUID).
- **Response**: JSON confirmation with `user_id` and `status`.

## Auth Endpoints

Auth endpoints are grouped under `/auth`.

### GET /auth/google
- **Description**: Initiates Google OAuth authentication flow.
- **Rate Limited**: Yes
- **Response**: JSON with `url` for Google OAuth.

### GET /auth/google/callback
- **Description**: Handles the callback from Google OAuth, processes authentication, and redirects to frontend.
- **Rate Limited**: Yes
- **Response**: Redirect to frontend dashboard or registration page.

### GET /auth/status
- **Description**: Checks if the current JWT token is valid.
- **Response**: JSON with `authenticate` boolean.

### GET /auth/refresh_token
- **Description**: Refreshes the JWT token for the authenticated user.
- **Response**: Sets a new JWT cookie.

### GET /auth/logout
- **Description**: Logs out the user by clearing the session.
- **Response**: JSON confirmation message.</content>
<parameter name="filePath">/Users/jayci/KTHAIS/backend/admin_and_auth_endpoints.md