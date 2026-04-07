package utils

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// this module will hanlde confirming google logins and issueing our own jwt, which will have user
// info attached.
// It will also decode jwts and validate them. Most auth pipeline steps should use this.

func ParseJWT(jwtIn string) (*jwt.Token, error) {
	// options := [jwt.WithoutClaimsValidtion()]
	jwtParser := jwt.NewParser()
	token, _, err := jwtParser.ParseUnverified(jwtIn, jwt.MapClaims{})
	return token, err
}

func getKeyById(kl *KeyList, id string) *JwksKey {
	for _, key := range kl.Keys {
		if key.Kid == id {
			return &key
		}
	}
	return nil
}

func keyfunc(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok {
		return nil, fmt.Errorf("token has no kid")
	}

	klist := GetGoogleJWKSKey()

	key := getKeyById(klist, kid)
	if key == nil {
		return nil, fmt.Errorf("no matching key found")
	}

	// Decode n and e (base64url to big.Int) and build rsa.PublicKey
	nBytes, _ := base64.RawURLEncoding.DecodeString(key.N)
	eBytes, _ := base64.RawURLEncoding.DecodeString(key.E)

	n := new(big.Int).SetBytes(nBytes)
	// e is usually 65537
	e := int(new(big.Int).SetBytes(eBytes).Int64())

	return &rsa.PublicKey{N: n, E: e}, nil
}

func ParseAndVerifyGoogle(jwtIn string) (bool, *jwt.Token) {
	jwtParser := jwt.NewParser()

	token, err := jwtParser.Parse(jwtIn, keyfunc)
	if err != nil {
		log.Printf("Error parsing encrypted token")
	}
	return token.Valid, token
}

// func ParseAndVerify(jwtIn string, skey string) (bool, *jwt.Token) {
// 	jwtParser := jwt.NewParser()
// 	kf := func(token *jwt.Token) (any, error) {
// 		return []byte(skey), nil
// 	}
// 	token, err := jwtParser.Parse(jwtIn, kf)
// 	if err != nil {
// 		log.Printf("Error parsing encrypted token %v\n", err)
// 	}
// 	return token.Valid, token
// }

// It uses public key to verify the signature

func ParseAndVerify(jwtIn string, pemKey string) (bool, *jwt.Token) {
	jwtParser := jwt.NewParser()

	kf := func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemKey))
		if err != nil {
			pubKey, pubErr := jwt.ParseRSAPublicKeyFromPEM([]byte(pemKey))
			if pubErr == nil {
				return pubKey, nil
			}
			return nil, fmt.Errorf("could not parse pem key: %v", err)
		}

		return &privateKey.PublicKey, nil
	}

	token, err := jwtParser.Parse(jwtIn, kf)
	if err != nil {
		log.Printf("Error verifying token: %v\n", err)
		return false, nil
	}

	return token.Valid, token
}

func GetJWTString(c *gin.Context) string {
	for _, cookie := range c.Request.Cookies() {
		if cookie.Name == "jwt" {
			return cookie.Value
		}
	}
	return ""
}

func GetJWT(c *gin.Context) *jwt.Token {
	for _, cookie := range c.Request.Cookies() {
		if cookie.Name == "jwt" {
			token, _ := ParseJWT(cookie.Value)
			return token
		}
	}
	return nil
}

func GetClaims(token *jwt.Token) jwt.MapClaims {
	claims, _ := token.Claims.(jwt.MapClaims)
	return claims
}

type JwksKey struct {
	N   string `json:"n"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	E   string `json:"e"`
}

type KeyList struct {
	Keys []JwksKey `json:"keys"`
}

func GetGoogleJWKSKey() *KeyList {
	url := "https://www.googleapis.com/oauth2/v3/certs"
	resp, err := http.Get(url)
	if err != nil {
		log.Println("Could not retrieve google credentials")
		return nil
	}
	defer resp.Body.Close()
	var data KeyList
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Printf("Error decoding json%v\n", err)
	}
	return &data
}

type UserClaims struct {
	Email  string    `json:"email"`
	Roles  string    `json:"roles"`
	UserID uuid.UUID `json:"user_id"`
	jwt.RegisteredClaims
}

func WriteJWT(email string, roles []string, Id uuid.UUID, key string, validMinutes int) string {
	// Create claims with multiple fields populated
	claims := UserClaims{
		email,
		strings.Join(roles, ","),
		Id,
		jwt.RegisteredClaims{
			// A usual scenario is to set the expiration time relative to the current time
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(validMinutes) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "KTHAIS",
		},
	}

	// token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// ss, err := token.SignedString([]byte(key))
	// if err != nil {
	// 	log.Printf("Failed to generate JWT token: %v\n", err)
	// }
	// return ss

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key))
	if err != nil {
		log.Fatalf("Fatal error parsing private key: %v", err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		log.Printf("Failed to generate JWT token: %v\n", err)
	}
	return signedToken
}
