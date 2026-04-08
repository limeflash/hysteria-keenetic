package keenetic

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticateChallengeFlow(t *testing.T) {
	const (
		login     = "admin"
		password  = "router-pass"
		realm     = "keenetic"
		challenge = "abcdef123456"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("X-NDM-Realm", realm)
			w.Header().Set("X-NDM-Challenge", challenge)
			w.WriteHeader(http.StatusUnauthorized)
		case http.MethodPost:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}

			expectedMD5 := md5.Sum([]byte(login + ":" + realm + ":" + password))
			expectedSHA := sha256.Sum256([]byte(challenge + hex.EncodeToString(expectedMD5[:])))
			if body["login"] != login || body["password"] != hex.EncodeToString(expectedSHA[:]) {
				t.Fatalf("unexpected body: %#v", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	if err := client.Authenticate(context.Background(), login, password); err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
}
