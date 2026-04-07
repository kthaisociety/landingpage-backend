package utils

import (
	"log"
	"testing"

	"github.com/google/uuid"
)

func TestParseJWT(t *testing.T) {
	test_jwt := "eyJhbGciOiJSUzI1NiIsImtpZCI6ImM4YWI3MTUzMDk3MmJiYTIwYjQ5Zjc4YTA5Yzk4NTJjNDNmZjkxMTgiLCJ0eXAiOiJKV1QifQ.eyJpc3MiOiJodHRwczovL2FjY291bnRzLmdvb2dsZS5jb20iLCJhenAiOiI2ODc4OTA1MzA1MS1lcDQ4dDVxbGNrNThnYW0xaGlkNDFnYmJvaHYzbHIyNi5hcHBzLmdvb2dsZXVzZXJjb250ZW50LmNvbSIsImF1ZCI6IjY4Nzg5MDUzMDUxLWVwNDh0NXFsY2s1OGdhbTFoaWQ0MWdiYm9odjNscjI2LmFwcHMuZ29vZ2xldXNlcmNvbnRlbnQuY29tIiwic3ViIjoiMTE2MDY5ODg0OTYzMDA3NTM0NTU3IiwiaGQiOiJrdGhhaXMuY29tIiwiZW1haWwiOiJ2aXZpZW5uZUBrdGhhaXMuY29tIiwiZW1haWxfdmVyaWZpZWQiOnRydWUsImF0X2hhc2giOiJULUlrQXdROHVzRXFFLUFhOU5oT2JRIiwibmFtZSI6IlZpdmllbm5lIEN1cmV3aXR6IiwicGljdHVyZSI6Imh0dHBzOi8vbGgzLmdvb2dsZXVzZXJjb250ZW50LmNvbS9hL0FDZzhvY0t1bURQRTZoWW5ERzY4bkt0Nlp1Q3JtbGtMUEJPbXpxQVBValp3bFJGTWpYU3ZBZz1zOTYtYyIsImdpdmVuX25hbWUiOiJWaXZpZW5uZSIsImZhbWlseV9uYW1lIjoiQ3VyZXdpdHoiLCJpYXQiOjE3NTk5NDY2ODEsImV4cCI6MTc1OTk1MDI4MX0.afkVhYSvqRvPjnkfNOmAkiS9xo-mwFKy2cgzR2xndHRE1XE8DrVuHZflIdG3PMknW4-R6tfhHZZqLH7OXJGgg9AKbQk1C_pl10rk_5hG5vm2b2Es_r32M6ZyUEHIQqBeT5AQ4cSTPmduQuTGwI9Ku0dv12xJQ25FJXmzktHe0ijMNBeIeScfhXZqlQSs6Dgutd2n9YaJCJmcxFbFihN8fyhXEXWm6F4BdjtE6bY2oPUKTxrlbelkz-B0z72Y6utZrBnDMEFmfpARISmUynaCM5zVA4DX4IeVtTGRPuAG-Y2T0jAM-s6wn4nGXZYbuRDW6Q7qVz-gX_1Pv9e63NZJmQ"
	_, err := ParseJWT(string(test_jwt))
	if err != nil {
		t.Errorf("Error Parsing JWT %v\n", err)
	}
}

func TestKeyFetch(t *testing.T) {
	GetGoogleJWKSKey()
}

// this is only to confirm that we can verify a google token, but requires a fresh token
// func TestParseAndVerifyGoogle(t *testing.T) {
// 	filename := "google.jwt"
// 	f, _ := os.Open(filename)
// 	reader := bufio.NewReader(f)
// 	test_jwt, isPrefix, _ := reader.ReadLine()
// 	if isPrefix {
// 		log.Println("Long JWT")
// 	}
// 	valid, token := ParseAndVerifyGoogle(string(test_jwt))
// 	if !valid {
// 		t.Errorf("Token not valid!!")
// 	} else {
// 		claims, _ := token.Claims.(jwt.MapClaims)
// 		for keys, values := range claims {
// 			log.Printf("Key: %v --- Value: %v\n", keys, values)
// 		}
// 	}
// 	if false {
// 		t.Errorf("No error\n")
// 	}
// }

func TestParseAndVerify(t *testing.T) {
	key := `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCx+aR9uA0eX4B3
cAYDdvJ0g4JSLfcxtvKS+rC64lyPVoNg4tX2JHdoYdar1p5jBnFrQQCqNQAqFZt7
wYaJBBfPll5AN+JXt65/e/yTu4hzjn4GPp88lHnJ/CBg36CDuPGvm26h1pZ/r1mS
BF4QpQeBK9THpApn2OPaw6jVvP/tE3OiX8ms/xhpBTXMJmQK29ThGBeb2uRReY+T
kS8Z1O1DXGa5KRorkGJZAKiLbvCNqUuv4oaVg7jyr614ta+g4NtHnY6nlaTejWXi
bS1fF7JT1+CtvdcxbfSfRlevoUb68hpujMxuVOBYviCZSRIHk2UGNc9wFzFTXbwQ
qKpr3HPRAgMBAAECggEAE9XGx1wj2ia6opURloFDNEkT1STaT+gb0NWvrKRdvHvf
2IRvZCdcR33f9vbMYCzvpwxvjoippAcUdQ50eADExpXzmySfTTdjc0HWPIDCDF7t
HLUN+ipyCFjZIvLJaOTLys5/3fmUfFaGnQdvlFtQIs1HwZg+sKQzgMYdovSVcU5x
uagAZzmJr78yWXzFt4m30ypDy3qBuo/vCNCF0mgIsvB9S/HCoEN9s6tY92jqTfRL
mRo6ScAskW5MZP6Flkk4IVUbIFnJ9saTu+UfvASZPBwOlExkPwJg/oir8ec3avEO
YcYf9usDO4AF3GkssXVYvPCEdN48FbmY115Q9vcjoQKBgQDx0/jXCJu6jt5tWXFP
/MGyT+kHGMbBiqLNPPd94RRBfSslm24dNVXMLwOHchg8whEO2pJpNaLy6fJ9jv8Q
3cF7SYqIrHq+RaM9vhjioZ4eym+Enp6HRhWSUxj3StaUJAxu6suwnNjQA2+3vxYr
4ddlVmkcTO2U9s5QWL1T33dc2wKBgQC8Z7ehExNsG3PRZY9AxGDRFOVbdNaMBv2t
kBjCORuwt5H7bEYdIuCmvJklfRfVe3NzmfIQVkIhGyUCX6g6xfflFyBTdXcRwh0Z
wVHr7UU9JoX8tmngw5PT00TXwF6zUxhf/UmZieE19UweZHeOknSznqbMLyALQkjP
yQ2I1ZL7wwKBgA5Le3AqoBn9DATmvp390O1bb+jtfAJA0bLUIcUdIvdkEMLeVzn+
xx2Uwd6lzez5g8ye+vyhIQq+7YiihU1X7nH9POUrXO4Wa7ngnNP4vcIQMVtjjPdu
GyRVKSqlD94d62Y7FuNPwjk5msb/0q2xYewpmXkEyx59IGD7feWRVhr1AoGABVgf
0lbbLy7cKy1pUdoAMQ4Zr21yBIjSO1EiEqhSC8I5Rtt8ZakunCwvX+vbeDfHP5k3
T5VSzOObOiUCfaBN9tagGR304bES6D8elsWlOCXWmSOHf1Os5s5QXppbVVTfFSH3
K37Iv6IUpawN5CJtYwb2Dkar7wXTUOmQE7iTMccCgYEA4uuzOerUWPq78kFMqts8
KKCzr1aFoVsLEc30MPEt8ezIbkjQwrGZLBSKgbA9aoI/TAhBfyNsQm7nWE+3fKVs
S43+xKtc0XeDShR0hcuvSMnh7keedg641AaqfnNTI22/ivmCCrGI+8MFjZOJBdn3
mQWo5Mg2hOqaTKRCpdodXb4=
-----END PRIVATE KEY-----
`
	uuid, _ := uuid.Parse("50c06e4d-b594-4489-9d4b-a513f63c90bd")
	newJwt := WriteJWT("vivienne@kthais.com", []string{"user", "admin", "queen"}, uuid, key, 15)
	valid, _ := ParseAndVerify(newJwt, key)
	if !valid {
		t.Errorf("Could not validate JWT: \n")
	}
}

func TestJWTCreate(t *testing.T) {
	key := `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCx+aR9uA0eX4B3
cAYDdvJ0g4JSLfcxtvKS+rC64lyPVoNg4tX2JHdoYdar1p5jBnFrQQCqNQAqFZt7
wYaJBBfPll5AN+JXt65/e/yTu4hzjn4GPp88lHnJ/CBg36CDuPGvm26h1pZ/r1mS
BF4QpQeBK9THpApn2OPaw6jVvP/tE3OiX8ms/xhpBTXMJmQK29ThGBeb2uRReY+T
kS8Z1O1DXGa5KRorkGJZAKiLbvCNqUuv4oaVg7jyr614ta+g4NtHnY6nlaTejWXi
bS1fF7JT1+CtvdcxbfSfRlevoUb68hpujMxuVOBYviCZSRIHk2UGNc9wFzFTXbwQ
qKpr3HPRAgMBAAECggEAE9XGx1wj2ia6opURloFDNEkT1STaT+gb0NWvrKRdvHvf
2IRvZCdcR33f9vbMYCzvpwxvjoippAcUdQ50eADExpXzmySfTTdjc0HWPIDCDF7t
HLUN+ipyCFjZIvLJaOTLys5/3fmUfFaGnQdvlFtQIs1HwZg+sKQzgMYdovSVcU5x
uagAZzmJr78yWXzFt4m30ypDy3qBuo/vCNCF0mgIsvB9S/HCoEN9s6tY92jqTfRL
mRo6ScAskW5MZP6Flkk4IVUbIFnJ9saTu+UfvASZPBwOlExkPwJg/oir8ec3avEO
YcYf9usDO4AF3GkssXVYvPCEdN48FbmY115Q9vcjoQKBgQDx0/jXCJu6jt5tWXFP
/MGyT+kHGMbBiqLNPPd94RRBfSslm24dNVXMLwOHchg8whEO2pJpNaLy6fJ9jv8Q
3cF7SYqIrHq+RaM9vhjioZ4eym+Enp6HRhWSUxj3StaUJAxu6suwnNjQA2+3vxYr
4ddlVmkcTO2U9s5QWL1T33dc2wKBgQC8Z7ehExNsG3PRZY9AxGDRFOVbdNaMBv2t
kBjCORuwt5H7bEYdIuCmvJklfRfVe3NzmfIQVkIhGyUCX6g6xfflFyBTdXcRwh0Z
wVHr7UU9JoX8tmngw5PT00TXwF6zUxhf/UmZieE19UweZHeOknSznqbMLyALQkjP
yQ2I1ZL7wwKBgA5Le3AqoBn9DATmvp390O1bb+jtfAJA0bLUIcUdIvdkEMLeVzn+
xx2Uwd6lzez5g8ye+vyhIQq+7YiihU1X7nH9POUrXO4Wa7ngnNP4vcIQMVtjjPdu
GyRVKSqlD94d62Y7FuNPwjk5msb/0q2xYewpmXkEyx59IGD7feWRVhr1AoGABVgf
0lbbLy7cKy1pUdoAMQ4Zr21yBIjSO1EiEqhSC8I5Rtt8ZakunCwvX+vbeDfHP5k3
T5VSzOObOiUCfaBN9tagGR304bES6D8elsWlOCXWmSOHf1Os5s5QXppbVVTfFSH3
K37Iv6IUpawN5CJtYwb2Dkar7wXTUOmQE7iTMccCgYEA4uuzOerUWPq78kFMqts8
KKCzr1aFoVsLEc30MPEt8ezIbkjQwrGZLBSKgbA9aoI/TAhBfyNsQm7nWE+3fKVs
S43+xKtc0XeDShR0hcuvSMnh7keedg641AaqfnNTI22/ivmCCrGI+8MFjZOJBdn3
mQWo5Mg2hOqaTKRCpdodXb4=
-----END PRIVATE KEY-----
`
	uuid, _ := uuid.Parse("50c06e4d-b594-4489-9d4b-a513f63c90bd")
	newJwt := WriteJWT("vivienne@kthais.com", []string{"user", "admin", "queen"}, uuid, key, 15)
	log.Printf("JWT Generated: %v\n", newJwt)
}
