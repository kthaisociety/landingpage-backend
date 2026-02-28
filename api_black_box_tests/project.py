import requests
import jwt
import uuid
import json
import time

BASE_URL = "http://localhost:8080/api/v1/projects"


def create_jwt(role="admin,user"):
    jwt_key = ""
    jf = open("key.txt", "r")
    jwt_key = jf.read().strip()
    jf.close()
    claims = {
        "iss": "KTHAIS",
        "email": "joelmah@kthais.com",
        "roles": role,
        "user_id": str(uuid.uuid4()),
        "exp": int(time.time()) + 600,
    }
    token = jwt.encode(claims, jwt_key, algorithm="HS256")
    return token


def admin_cookies():
    return {"jwt": create_jwt("admin,user")}


def user_cookies():
    return {"jwt": create_jwt("user")}


# ──────────────────────────────────────────────
#  CRUD helpers
# ──────────────────────────────────────────────

def create_project(name, description="", skills=None, status="planning"):
    data = {
        "name": name,
        "description": description,
        "skills": skills or [],
        "status": status,
    }
    resp = requests.post(BASE_URL, json=data, cookies=admin_cookies())
    return resp


def list_projects():
    return requests.get(BASE_URL)


def get_project(project_id):
    return requests.get(f"{BASE_URL}/{project_id}")


def update_project(project_id, data):
    return requests.put(f"{BASE_URL}/{project_id}", json=data, cookies=admin_cookies())


def delete_project(project_id):
    return requests.delete(f"{BASE_URL}/{project_id}", cookies=admin_cookies())


def add_member(project_id, user_id):
    return requests.post(
        f"{BASE_URL}/{project_id}/members",
        json={"user_id": str(user_id)},
        cookies=admin_cookies(),
    )


def remove_member(project_id, user_id):
    return requests.delete(
        f"{BASE_URL}/{project_id}/members/{user_id}",
        cookies=admin_cookies(),
    )


# ──────────────────────────────────────────────
#  Tests
# ──────────────────────────────────────────────

def test_list_empty():
    """GET /projects should return a list (possibly empty)."""
    resp = list_projects()
    assert resp.ok, f"List failed: {resp.status_code} {resp.text}"
    assert isinstance(resp.json(), list), "Expected a JSON array"
    print("PASS: test_list_empty")


def test_create_project():
    """POST /projects with admin JWT should create a project."""
    resp = create_project("Black Box Test Project", "testing", ["Go", "Python"])
    assert resp.status_code == 201, f"Create failed: {resp.status_code} {resp.text}"
    data = resp.json()
    assert data["name"] == "Black Box Test Project"
    assert data["status"] == "planning"
    assert data["skills"] == ["Go", "Python"]
    assert data["members"] == []
    assert "id" in data
    print(f"PASS: test_create_project (id={data['id']})")
    return data["id"]


def test_create_project_missing_name():
    """POST /projects without a name should return 400."""
    resp = requests.post(BASE_URL, json={"description": "no name"}, cookies=admin_cookies())
    assert resp.status_code == 400, f"Expected 400, got {resp.status_code}"
    print("PASS: test_create_project_missing_name")


def test_create_project_invalid_status():
    """POST /projects with bad status should return 400."""
    resp = create_project("Bad Status", status="banana")
    assert resp.status_code == 400, f"Expected 400, got {resp.status_code}"
    print("PASS: test_create_project_invalid_status")


def test_create_project_default_status():
    """POST /projects without status should default to planning."""
    data = {"name": "Default Status Project"}
    resp = requests.post(BASE_URL, json=data, cookies=admin_cookies())
    assert resp.status_code == 201, f"Create failed: {resp.status_code} {resp.text}"
    assert resp.json()["status"] == "planning"
    # cleanup
    delete_project(resp.json()["id"])
    print("PASS: test_create_project_default_status")


def test_get_project(project_id):
    """GET /projects/:id should return the project."""
    resp = get_project(project_id)
    assert resp.ok, f"Get failed: {resp.status_code} {resp.text}"
    data = resp.json()
    assert data["id"] == project_id
    assert data["name"] == "Black Box Test Project"
    print(f"PASS: test_get_project")


def test_get_project_not_found():
    """GET /projects/:id with fake ID should return 404."""
    fake_id = str(uuid.uuid4())
    resp = get_project(fake_id)
    assert resp.status_code == 404, f"Expected 404, got {resp.status_code}"
    print("PASS: test_get_project_not_found")


def test_get_project_invalid_id():
    """GET /projects/:id with bad UUID should return 400."""
    resp = get_project("not-a-uuid")
    assert resp.status_code == 400, f"Expected 400, got {resp.status_code}"
    print("PASS: test_get_project_invalid_id")


def test_update_project(project_id):
    """PUT /projects/:id should update fields."""
    resp = update_project(project_id, {"name": "Updated Name", "status": "active"})
    assert resp.ok, f"Update failed: {resp.status_code} {resp.text}"
    data = resp.json()
    assert data["name"] == "Updated Name"
    assert data["status"] == "active"
    print("PASS: test_update_project")


def test_update_project_partial(project_id):
    """PUT /projects/:id with only description should not overwrite other fields."""
    resp = update_project(project_id, {"description": "new desc"})
    assert resp.ok, f"Update failed: {resp.status_code} {resp.text}"
    data = resp.json()
    assert data["description"] == "new desc"
    assert data["name"] == "Updated Name", f"Name was overwritten: {data['name']}"
    assert data["status"] == "active", f"Status was overwritten: {data['status']}"
    print("PASS: test_update_project_partial")


def test_update_project_empty_name(project_id):
    """PUT /projects/:id with empty name should return 400."""
    resp = update_project(project_id, {"name": ""})
    assert resp.status_code == 400, f"Expected 400, got {resp.status_code}"
    print("PASS: test_update_project_empty_name")


def test_update_project_invalid_status(project_id):
    """PUT /projects/:id with bad status should return 400."""
    resp = update_project(project_id, {"status": "banana"})
    assert resp.status_code == 400, f"Expected 400, got {resp.status_code}"
    print("PASS: test_update_project_invalid_status")


def test_update_project_not_found():
    """PUT /projects/:id with fake ID should return 404."""
    fake_id = str(uuid.uuid4())
    resp = update_project(fake_id, {"name": "Ghost"})
    assert resp.status_code == 404, f"Expected 404, got {resp.status_code}"
    print("PASS: test_update_project_not_found")


def test_list_contains_project(project_id):
    """GET /projects should contain our created project."""
    resp = list_projects()
    assert resp.ok
    ids = [p["id"] for p in resp.json()]
    assert project_id in ids, f"Project {project_id} not in list"
    print("PASS: test_list_contains_project")


def test_unauthorized_create():
    """POST /projects without JWT should return 401."""
    resp = requests.post(BASE_URL, json={"name": "No Auth"})
    assert resp.status_code == 401, f"Expected 401, got {resp.status_code}"
    print("PASS: test_unauthorized_create")


def test_non_admin_create():
    """POST /projects with user-only JWT should return 401."""
    resp = requests.post(
        BASE_URL,
        json={"name": "User Only"},
        cookies=user_cookies(),
    )
    assert resp.status_code == 401, f"Expected 401, got {resp.status_code}"
    print("PASS: test_non_admin_create")


def test_delete_project(project_id):
    """DELETE /projects/:id should return 200."""
    resp = delete_project(project_id)
    assert resp.ok, f"Delete failed: {resp.status_code} {resp.text}"
    print("PASS: test_delete_project")


def test_get_after_delete(project_id):
    """GET /projects/:id after delete should return 404."""
    resp = get_project(project_id)
    assert resp.status_code == 404, f"Expected 404, got {resp.status_code}"
    print("PASS: test_get_after_delete")


def test_delete_not_found():
    """DELETE /projects/:id with fake ID should return 404."""
    fake_id = str(uuid.uuid4())
    resp = delete_project(fake_id)
    assert resp.status_code == 404, f"Expected 404, got {resp.status_code}"
    print("PASS: test_delete_not_found")


def test_all_statuses():
    """Create projects with each valid status."""
    for status in ["planning", "active", "completed"]:
        resp = create_project(f"Status {status}", status=status)
        assert resp.status_code == 201, f"Create with status '{status}' failed: {resp.status_code}"
        assert resp.json()["status"] == status
        delete_project(resp.json()["id"])
    print("PASS: test_all_statuses")


# ──────────────────────────────────────────────
#  Full workflow
# ──────────────────────────────────────────────

def run_all():
    passed = 0
    failed = 0

    def run(fn, *args):
        nonlocal passed, failed
        try:
            result = fn(*args)
            passed += 1
            return result
        except AssertionError as e:
            print(f"FAIL: {fn.__name__} — {e}")
            failed += 1
            return None
        except Exception as e:
            print(f"ERROR: {fn.__name__} — {e}")
            failed += 1
            return None

    print("=" * 50)
    print("Project Handler — API Black Box Tests")
    print("=" * 50)

    # Validation & edge cases (no state dependency)
    run(test_list_empty)
    run(test_create_project_missing_name)
    run(test_create_project_invalid_status)
    run(test_create_project_default_status)
    run(test_get_project_not_found)
    run(test_get_project_invalid_id)
    run(test_update_project_not_found)
    run(test_delete_not_found)
    run(test_all_statuses)

    # Auth tests
    run(test_unauthorized_create)
    run(test_non_admin_create)

    # CRUD workflow
    project_id = run(test_create_project)
    if project_id:
        run(test_get_project, project_id)
        run(test_list_contains_project, project_id)
        run(test_update_project, project_id)
        run(test_update_project_partial, project_id)
        run(test_update_project_empty_name, project_id)
        run(test_update_project_invalid_status, project_id)
        run(test_delete_project, project_id)
        run(test_get_after_delete, project_id)

    print("=" * 50)
    print(f"Results: {passed} passed, {failed} failed, {passed + failed} total")
    print("=" * 50)


if __name__ == "__main__":
    run_all()
