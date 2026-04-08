package keenetic

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type AuthClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewAuthClient(baseURL string) *AuthClient {
	return &AuthClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *AuthClient) Authenticate(ctx context.Context, login, password string) error {
	login = strings.TrimSpace(login)
	if login == "" || password == "" {
		return errors.New("router login and password are required")
	}

	authURL := c.baseURL + "/auth"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
	default:
		return fmt.Errorf("unexpected auth challenge status: %d", resp.StatusCode)
	}

	realm := resp.Header.Get("X-NDM-Realm")
	challenge := resp.Header.Get("X-NDM-Challenge")
	if realm == "" || challenge == "" {
		return errors.New("keenetic auth challenge headers are missing")
	}

	md5Bytes := md5.Sum([]byte(login + ":" + realm + ":" + password))
	md5Hex := hex.EncodeToString(md5Bytes[:])

	shaBytes := sha256.Sum256([]byte(challenge + md5Hex))
	payload := map[string]string{
		"login":    login,
		"password": hex.EncodeToString(shaBytes[:]),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	postReq.Header.Set("Content-Type", "application/json")

	postResp, err := c.httpClient.Do(postReq)
	if err != nil {
		return err
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		return fmt.Errorf("router credentials were rejected with status %d", postResp.StatusCode)
	}

	return nil
}

func HostnameFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		return ""
	}
	return (&url.URL{Scheme: scheme, Host: host}).String()
}
