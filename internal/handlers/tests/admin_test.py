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


# --- RUN ---

if __name__ == "__main__":
    test_checkadmin()
    test_list_users()

    r = test_create_user()
    if r.status_code == 201:
        new_user = r.json()
        uid = new_user["user_id"]

        test_lookup_uuid(new_user["email"])
        test_promote(uid)
        test_list_admins()
        test_demote(uid)