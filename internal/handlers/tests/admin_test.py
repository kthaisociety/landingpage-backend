import requests
import json
import os
import datetime
import uuid
import jwt
from dotenv import load_dotenv

# Load JWTSigningKey from .env (three levels up from this file)
load_dotenv(os.path.join(os.path.dirname(__file__), "../../../.env"))

BASE_URL = "http://localhost:8080/api/v1"

def generate_admin_token():
    signing_key = os.getenv("JWTSigningKey", "test123456")
    now = datetime.datetime.now(datetime.timezone.utc)
    payload = {
        "email": "testadmin@example.com",
        "roles": "admin",
        "user_id": str(uuid.uuid4()),
        "iss": "KTHAIS",
        "iat": now,
        "nbf": now,
        "exp": now + datetime.timedelta(minutes=60),
    }
    return jwt.encode(payload, signing_key, algorithm="HS256")

session = requests.Session()
session.cookies.set("jwt", generate_admin_token())

def call(method, path, **kwargs):
    url = BASE_URL + path
    r = session.request(method, url, **kwargs)
    print(f"\n{method} {path}")
    print("Status:", r.status_code)
    try:
        print(json.dumps(r.json(), indent=2))
    except:
        print(r.text)
    return r

# --- TESTS ---

def test_list_users():
    call("GET", "/admin/users")
    
def test_create_user():
    return call("POST", "/admin/users", json={
        "email": "tesuser@example.com"
    })
    
def test_lookup_uuid(email):
    call("GET", f"/admin/users/uuid?email={email}")
    
def test_promote(user_id):
    call("PUT", "/admin/setadmin", json={"user_id": user_id})

def test_demote(user_id):
    call("PUT", "/admin/unsetadmin", json={"user_id": user_id})

def test_list_admins():
    call("GET", "/admin/listadmins")

def test_checkadmin():
    call("GET", "/admin/checkadmin")

def test_create_project(name):
    return call("POST", "/projects", json={"name": name, "description": "Test project"})

def test_add_project_member(project_id, user_id):
    return call("POST", f"/projects/{project_id}/members", json={"user_id": user_id})

def test_delete_project(project_id):
    return call("DELETE", f"/projects/{project_id}")

def test_filter_users(has_profile=None, registered=None, created_after=None, created_before=None, team_id=None, project_id=None):
    params = {}
    if has_profile is not None:
        params["has_profile"] = str(has_profile).lower()
    if registered is not None:
        params["registered"] = str(registered).lower()
    if created_after is not None:
        params["created_after"] = created_after
    if created_before is not None:
        params["created_before"] = created_before
    if team_id is not None:
        params["team_id"] = team_id
    if project_id is not None:
        params["project_id"] = project_id
    return call("GET", "/admin/users/filter", params=params)


# --- RUN ---

if __name__ == "__main__":
    test_checkadmin()
    test_list_users()

    uid = None
    r = test_create_user()
    if r.status_code == 201:
        new_user = r.json()
        uid = new_user["user_id"]

        test_lookup_uuid(new_user["email"])
        test_promote(uid)
        test_list_admins()
        test_demote(uid)
    else:
        # User already exists — look them up
        r_lookup = call("GET", "/admin/users/uuid?email=tesuser@example.com")
        if r_lookup.status_code == 200:
            uid = r_lookup.json()["user_id"]
            print(f"(using existing user: {uid})")

    print("\n--- Users filter tests ---")
    test_filter_users()
    test_filter_users(has_profile=True)
    test_filter_users(has_profile=False)
    test_filter_users(registered=True)
    test_filter_users(registered=False)
    today = datetime.date.today().isoformat()
    test_filter_users(created_after="2024-01-01", created_before=today)
    test_filter_users(has_profile=True, registered=True, created_after="2024-01-01")

    print("\n--- Team/project filter tests (non-existent UUIDs — expect empty list) ---")
    fake_team_id = str(uuid.uuid4())
    fake_project_id = str(uuid.uuid4())
    test_filter_users(team_id=fake_team_id)
    test_filter_users(project_id=fake_project_id)
    test_filter_users(team_id=fake_team_id, project_id=fake_project_id)

    print("\n--- Team/project filter tests (happy path) ---")
    r_proj = test_create_project("Admin Test Project")
    if r_proj.status_code == 201 and uid:
        project = r_proj.json()
        project_id = project["id"]
        team_id = project.get("team_id")

        # Add user to project
        test_add_project_member(project_id, uid)

        # Filter by project_id — expect user to appear
        test_filter_users(project_id=project_id)

        # Filter by team_id — expect user to appear
        if team_id:
            test_filter_users(team_id=team_id)

        # Filter by both — expect user to appear
        if team_id:
            test_filter_users(team_id=team_id, project_id=project_id)

        # Cleanup
        test_delete_project(project_id)

        # Filter after deletion — expect empty list
        test_filter_users(project_id=project_id)

    # Invalid UUIDs — expect 400
    print("\n--- Invalid UUID tests (expect 400) ---")
    test_filter_users(team_id="not-a-uuid")
    test_filter_users(project_id="not-a-uuid")
    