import requests
import jwt
import uuid
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

def upload_companies(file_path, api_url):
    token = create_jwt() 
    with open(file_path, 'rb') as file:
        headers = {"authorization": f"Bearer {token}"}
        for line in file.readlines():
            name, description, logo = line.decode('utf-8').strip().split(',')
            id = uuid.uuid4()
            logo_id = uuid.uuid4()
            data = {
                "id": str(id),
                "name": name,
                "description": description,
                "logo": str(logo_id)
            }
            response = requests.post(f"{api_url}/admin/addCompany", json=data, headers=headers)
            if response.ok:
                print(f"Uploaded company: {name}")
            else:
                print(f"Failed to upload company: {name}, Status Code: {response.status_code}, Response: {response.text}")


def get_companies(api_url, save=False):
    token = create_jwt()
    headers = {"authorization": token}
    resp = requests.get(f"{api_url}/getAllCompanies", headers=headers)
    if resp.ok:
        names = []
        with open("companies.csv", "rb") as f:
            for line in f.readlines():
                name, description, logo = line.decode('utf-8').strip().split(',')
                names.append(name)
        companies = resp.json()
        c_names = [c['name'] for c in companies]
        success = True
        for name in names:
            if name not in c_names:
                print(f"Company {name} not found in API response")
                success = False
        if success:
            print("All companies verified successfully.")
            print(f"{c_names}")
        if save:
            with open("output_companies.json", "w") as out_file:
                import json
                json.dump(companies, out_file, indent=4)
    else:
        print(f"Failed to get companies, Status Code: {resp.status_code}, Response: {resp.text}")


def get_specific(api_url):
    token = create_jwt()
    headers = {"authorization": token}
    resp = requests.get(f"{api_url}/getAllCompanies", headers=headers)
    if resp.ok:
        names = []
        with open("companies.csv", "rb") as f:
            for line in f.readlines():
                name, description, logo = line.decode('utf-8').strip().split(',')
                names.append(name)
        companies = resp.json()
        id = companies[0]['id']
        params = {"id": id}
        resp2 = requests.get(f"{api_url}/getCompany", headers=headers, params=params)
        if resp2.ok:
            company = resp2.json()
            print(f"Retrieved company: {company}")

def getKTHAISLogo(api_url):
    resp = requests.get(f"{api_url}/getAllCompanies")
    data = resp.json()
    for c in data:
        if c["name"] == "KTHAIS":
            cresp = requests.get(f"{api_url}/getCompany", params={"id": c["id"]})
            cdata = cresp.json()
            logo_id = cdata["logo"]
            lresp = requests.get(f"{api_url}/logo", params={"id": logo_id})
            if lresp.ok:
                with open("dl_logo.png", "wb") as lfile:
                    lfile.write(lresp.content)
            else:
                print(f"Failed to fetch logo: {lresp.text}")

def delete_all(api_url):
    resp = requests.get(f"{api_url}/getAllCompanies", cookies={"jwt": create_jwt()})
    data = resp.json()
    for c in data:
        requests.delete(f"{api_url}/admin/delete", params={"id": c["id"]})


if __name__ == "__main__":
    api_url = "http://localhost:8080/api/v1/company"
    filepath = "./companies.csv"
    # upload_companies(filepath, api_url)
    # get_companies(api_url, save=False)
    # get_specific(api_url)
    # getKTHAISLogo(api_url)
    delete_all(api_url)