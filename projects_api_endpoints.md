# Projects API Endpoints Documentation

This document outlines the API endpoints defined in `project_handler.go` for managing projects and their members.

## Projects Endpoints

All project endpoints are grouped under `/projects`.

### Public Endpoints

#### GET /projects
- **Description**: Retrieves a list of all projects with their associated members and details.
- **Authentication**: None required
- **Response**: JSON array of project objects with nested members.
- **Response Fields**:
  - `id` (UUID) - Project unique identifier
  - `name` (string) - Project name
  - `description` (string) - Project description
  - `skills` (array of strings) - Required/associated skills
  - `status` (string) - Project status (planning, active, completed)
  - `members` (array of objects) - Team members with user_id, first_name, last_name, email, profile_picture

#### GET /projects/:id
- **Description**: Retrieves a single project by UUID with all its details and members.
- **Authentication**: None required
- **Path Parameters**: `id` (UUID) - The project's unique identifier
- **Response**: JSON project object with full details and members.

### Authenticated Endpoints (Admin Only)

All write operations require authentication via JWT and admin role.

#### POST /projects
- **Description**: Creates a new project and an associated team.
- **Authentication**: Required (JWT + Admin role)
- **Request Body**: JSON with:
  - `name` (string, required) - Project name
  - `description` (string, optional) - Project description
  - `skills` (array of strings, optional) - Associated skills
  - `status` (string, optional) - Project status (planning, active, completed). Defaults to "planning"
  - `team_id` (string, optional) - UUID of an existing team to associate with the project
- **Response**: JSON project object with the created project details.
- **Notes**: If no team_id is provided, a new dedicated team is created automatically.

#### PUT /projects/:id
- **Description**: Updates an existing project.
- **Authentication**: Required (JWT + Admin role)
- **Path Parameters**: `id` (UUID) - The project's unique identifier
- **Request Body**: JSON with any of the following:
  - `name` (string, optional) - New project name
  - `description` (string, optional) - New project description
  - `skills` (array of strings, optional) - New skills array
  - `status` (string, optional) - New project status (planning, active, completed)
- **Response**: JSON updated project object.

#### DELETE /projects/:id
- **Description**: Deletes a project and removes its team-project associations (teams are not deleted).
- **Authentication**: Required (JWT + Admin role)
- **Path Parameters**: `id` (UUID) - The project's unique identifier
- **Response**: JSON confirmation message.

#### POST /projects/:id/members
- **Description**: Adds a user as a member to a project's associated team.
- **Authentication**: Required (JWT + Admin role)
- **Path Parameters**: `id` (UUID) - The project's unique identifier
- **Request Body**: JSON with:
  - `user_id` (string, required) - UUID of the user to add
- **Response**: JSON success message.
- **Errors**:
  - 409 Conflict: User is already a member of this project
  - 404 Not Found: User or project not found

#### DELETE /projects/:id/members/:userId
- **Description**: Removes a user from a project's associated team.
- **Authentication**: Required (JWT + Admin role)
- **Path Parameters**: 
  - `id` (UUID) - The project's unique identifier
  - `userId` (UUID) - The user to remove
- **Response**: JSON success message.
- **Errors**:
  - 404 Not Found: User is not a member of this project, or project/user not found

## Project Status Values

Valid status values for projects are:
- `planning` - Project is in planning phase
- `active` - Project is currently active
- `completed` - Project has been completed

## Response Example

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "AI Mentorship Platform",
  "description": "A platform connecting AI researchers with mentees",
  "skills": ["Python", "Machine Learning", "Full Stack"],
  "status": "active",
  "members": [
    {
      "user_id": "660e8400-e29b-41d4-a716-446655440001",
      "first_name": "John",
      "last_name": "Doe",
      "email": "john@example.com",
      "profile_picture": "https://example.com/image.jpg"
    }
  ]
}
```
