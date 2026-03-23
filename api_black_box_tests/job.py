import requests
import jwt
import uuid
import json
import time

def create_jwt():
    jwt_key = ""
    jf = open("key.txt", "r")
    jwt_key = jf.read().strip()
    jf.close()
    claims = {
        "iss": "KTHAIS",
        "email": "vivienne@kthais.com",
        "roles": "admin,user",
        "id": str(uuid.uuid4()),
        "exp": int(time.time()) + 600 
    }
    print(f"Key: {jwt_key}")
    token = jwt.encode(claims, jwt_key, algorithm="HS256")
    # with open("pytoken.txt", "w") as f:
        # f.write(token)
    return token


def Upload_jobs(file_path, api_url):
    token = create_jwt() 
    f = open("output_companies.json", "r")
    companie_list = json.load(f)
    companies = dict()
    for c in companie_list:
        companies[c['name']] = c['id']
    f.close()
    with open(file_path, 'rb') as file:
        headers = {"authorization": f"Bearer {token}"}
        cookies = {"jwt": token}
        for line in file.readlines():
            title, description, salary, location, jobType, cname = line.decode('utf-8').strip().split(',')
            id = uuid.uuid4()
            company_id = companies[cname]
            data = {
                "id": str(id),
                "title": title,
                "salary": salary,
                "description": description,
                "location": location,
                "jobType": jobType,
                "company": company_id
            }
            response = requests.post(f"{api_url}/admin/new", json=data, headers=headers, cookies=cookies)
            if response.ok:
                print(f"Uploaded job listing: {title}")
            else:
                print(f"Failed to upload job listing: {title}, Status Code: {response.status_code}, Response: {response.text}")


def get_jobs(api_url, save=False):
    # f = open("output_companies.json")
    # companies = json.load(f)
    # f.close()
    response = requests.get(f"{api_url}/all")
    expected_titles = []
    with open("jobs.csv", "rb") as f:
        for line in f.readlines():
            title, description, salary, location, jobType, cname = line.decode('utf-8').strip().split(',')
            expected_titles.append(title)
    if response.ok:
        jobs = response.json()
        print(jobs)
        titles = [j["title"] for j in jobs]
        success = True
        for t in expected_titles:
            if t not in titles:
                print(f"Job listing {t} not found in API response")
                success = False
        if success:
            print("All job listings verified successfully.")
        """
        Test get single here
        """
        id = jobs[-1]["id"]
        params = {"id": id}
        resp = requests.get(f"{api_url}/job", params=params)
        if resp.ok:
            print(f"Got Job: {resp.json()}")
        else:
            print(f"Failed to get job: {resp.status_code} -- {resp.text}")
        """
        Test Update here
        """
        cookies = {"jwt": create_jwt()}
        data = {
            "description": "this is the updated, kosher description"
        }
        resp = requests.put(f"{api_url}/admin/update", cookies=cookies, params={"id": id}, json=data)
        if not resp.ok:
            print(f"Update failed: {resp.status_code} -- {resp.text}")
        else:
            resp = requests.get(f"{api_url}/job", params=params)
            print(f"got updated: {resp.json()}")
        ids = [j["id"] for j in jobs]
        return ids
    else:
        print(f"Failed to retrieve job listings, Status Code: {response.status_code}, Response: {response.text}")
        return []


def delete_ids(api_url, ids):
    token = create_jwt()
    cookies = {"jwt": token}
    success = True
    for id in ids:
        params = {"id": id}
        resp = requests.delete(f"{api_url}/admin/delete", cookies=cookies, params=params)
        if not resp.ok:
            print(f"Failed to delete {id}: {resp.status_code} -- {resp.text}") 
            success = False
    if success:
        print("All Deleted Successfully")

def test_full_upload(api_url):
    token = create_jwt()
    cookies = {"jwt": token}
    job = {
        "id": str(uuid.uuid4()),
        "title": "0.00001x engineer",
        "description": "one step forward, 4 steps backwards",
        "salary": "too much",
        "location": "sthlm",
        "jobType": "full-time fully remote",
        "company": "KTHAIS",
        "company_description": "we make ai and ai accessories",
        "appurl": "http://kthais.com",
        "contact": "john ai",
        "startdate": time.time(),
        "enddate": time.time(),
    }
    files = {
        "logo": ("aislogo.png", open("aislogo.png", "rb"), "image/png"),
        "job": ("job.json", json.dumps(job), "application/json"),
    }
    resp = requests.post(f"{api_url}/admin/full", cookies=cookies, files=files)
    if resp.ok:
        print(f"Job upload Response: {resp.json()}")
    else:
        print(f"Single Post Failed: {resp.text}")


if __name__ == "__main__":
    api_url = "http://localhost:8080/api/v1/joblistings"
    # Upload_jobs("jobs.csv", api_url)
    ids = get_jobs(api_url)
    delete_ids(api_url, ids)
    test_full_upload(api_url)