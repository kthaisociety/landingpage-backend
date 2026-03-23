# kthaisociety/backend

Next-generation backend for KTH AI Society website.

## Requirements

- Go 1.23.x
- Docker

## Setup

Use Docker compose to run the PostgreSQL database locally.

```bash
docker compose up -d
```

Use Go to run the program.

```bash
go run cmd/api/main.go
```

## Git Workflow

This project adheres to [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) for commit messages. This is to ensure that the project is easily maintainable and that the commit history is clean and easy to understand.

### Complete Workflow: From Issue to Merged PR

1. **Issue Assignment**

   - Assign yourself to an issue from our [project board](https://github.com/orgs/kthaisociety/projects/2)
   - Team members can self-assign issues or may be tasked with specific issues during workshops/meetings

2. **Project Management**

   - Ensure the issue is added to our [project view](https://github.com/orgs/kthaisociety/projects/2)
   - Update the issue status to "In Progress" when you start working on it

3. **Branch Creation & Development**

   - Check out the project locally
   - Name your branch either `issue-X` (where X is the issue number) or use GitHub's "Create a branch" feature
   - Follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) for commit messages
   - Include `fixes #X` in your commit message if it fixes issue X
   - Example commit message:

     ```text
     ci: add deployment notification to Mattermost

     * notify on mattermost upon deployment

     fixes #17
     ```

4. **Pull Request Creation**

   - Create a pull request when your work is ready for review
   - Use "Draft Pull Request" if you want early feedback but the work is not yet complete

5. **Code Review Process**

   - Request reviews in the team Mattermost channel
   - Primary reviewer is typically the Head of IT
   - The review process may involve multiple rounds of feedback and changes
   - It is generally the responsibility of the PR creator to merge once approved
   - Coordinate with Head of IT before merging if it triggers a production deployment
   - All merges should be squashed

6. **Showcase**
   - Present your completed work during the next team meeting

## Notice about license

This project is licensed under the MIT license. See the [LICENSE](LICENSE) file for more information.

This does **not** apply to logos, icons, and images used in this project. They are the property of KTH AI Society and are not licensed for public, commercial, or personal use. If you wish to use them, please contact us at [contact@kthais.com](mailto:contact@kthais.com).
