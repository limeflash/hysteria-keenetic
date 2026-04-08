package remnawave

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hysteria-keenetic/internal/logs"
)

type Client struct {
	httpClient *http.Client
	logger     *logs.Logger
}

type FetchResult struct {
	Profiles             []Profile
	UserAgent            string
	RefreshIntervalHours int
}

type Profile struct {
	Name   string
	Server string
	Port   int
	Auth   string
	SNI    string
	ALPN   []string
	IsWarp bool
}

type xrayEnvelope struct {
	Remarks   string         `json:"remarks"`
	Outbounds []xrayOutbound `json:"outbounds"`
}

type xrayOutbound struct {
	Protocol       string               `json:"protocol"`
	Settings       xrayOutboundSettings `json:"settings"`
	StreamSettings xrayStreamSettings   `json:"streamSettings"`
}

type xrayOutboundSettings struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Version int    `json:"version"`
}

type xrayStreamSettings struct {
	Network          string               `json:"network"`
	Security         string               `json:"security"`
	HysteriaSettings xrayHysteriaSettings `json:"hysteriaSettings"`
	TLSSettings      xrayTLSSettings      `json:"tlsSettings"`
}

type xrayHysteriaSettings struct {
	Version int    `json:"version"`
	Auth    string `json:"auth"`
}

type xrayTLSSettings struct {
	ServerName string   `json:"serverName"`
	ALPN       []string `json:"alpn"`
}

func NewClient(logger *logs.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 25 * time.Second},
		logger:     logger,
	}
}

func (c *Client) FetchSubscription(ctx context.Context, url, hwid, primaryUserAgent string, defaultRefreshHours int) (FetchResult, error) {
	userAgents := orderedUserAgents(primaryUserAgent)
	var lastErr error

	for _, userAgent := range userAgents {
		result, err := c.fetchOnce(ctx, url, hwid, userAgent, defaultRefreshHours)
		if err == nil {
			return result, nil
		}
		lastErr = err
		c.logger.Printf("subscription fetch with user-agent=%s failed: %v", userAgent, err)
	}

	if lastErr == nil {
		lastErr = errors.New("subscription fetch failed")
	}
	return FetchResult{}, lastErr
}

func (c *Client) fetchOnce(ctx context.Context, url, hwid, userAgent string, defaultRefreshHours int) (FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FetchResult{}, err
	}

	req.Header.Set("x-hwid", hwid)
	req.Header.Set("x-device-os", "KeeneticOS")
	req.Header.Set("x-ver-os", "4.x")
	req.Header.Set("x-device-model", "Keenetic")
	req.Header.Set("user-agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return FetchResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return FetchResult{}, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return FetchResult{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return FetchResult{}, errors.New("empty subscription response")
	}
	if strings.HasPrefix(strings.ToLower(raw), "<!doctype html") || strings.HasPrefix(raw, "<html") {
		return FetchResult{}, errors.New("subscription endpoint returned html instead of json")
	}

	var envelopes []xrayEnvelope
	if err := json.Unmarshal(body, &envelopes); err != nil {
		return FetchResult{}, fmt.Errorf("invalid json subscription payload: %w", err)
	}

	profiles := parseProfiles(envelopes)
	if len(profiles) == 0 {
		if looksLikeAppNotSupported(envelopes) {
			return FetchResult{}, errors.New("app not supported for this user-agent")
		}
		return FetchResult{}, errors.New("no hysteria2 profiles found in subscription")
	}

	return FetchResult{
		Profiles:             profiles,
		UserAgent:            userAgent,
		RefreshIntervalHours: parseRefreshInterval(resp.Header.Get("profile-update-interval"), defaultRefreshHours),
	}, nil
}

func parseProfiles(envelopes []xrayEnvelope) []Profile {
	profiles := make([]Profile, 0, len(envelopes))
	for _, envelope := range envelopes {
		for _, outbound := range envelope.Outbounds {
			if strings.ToLower(outbound.Protocol) != "hysteria" {
				continue
			}
			version := outbound.Settings.Version
			if outbound.StreamSettings.HysteriaSettings.Version != 0 {
				version = outbound.StreamSettings.HysteriaSettings.Version
			}
			if version != 2 {
				continue
			}

			name := strings.TrimSpace(envelope.Remarks)
			if name == "" {
				name = outbound.Settings.Address
			}

			profiles = append(profiles, Profile{
				Name:   name,
				Server: outbound.Settings.Address,
				Port:   outbound.Settings.Port,
				Auth:   outbound.StreamSettings.HysteriaSettings.Auth,
				SNI:    outbound.StreamSettings.TLSSettings.ServerName,
				ALPN:   append([]string{}, outbound.StreamSettings.TLSSettings.ALPN...),
				IsWarp: detectWarp(name, outbound.Settings.Address, outbound.StreamSettings.TLSSettings.ServerName),
			})
			break
		}
	}
	return profiles
}

func orderedUserAgents(primary string) []string {
	candidates := []string{strings.TrimSpace(primary), "happ"}
	seen := make(map[string]struct{}, len(candidates))
	ordered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			candidate = "v2raytun"
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		ordered = append(ordered, candidate)
	}
	return ordered
}

func parseRefreshInterval(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func looksLikeAppNotSupported(envelopes []xrayEnvelope) bool {
	if len(envelopes) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(envelopes[0].Remarks), "App not supported")
}

func detectWarp(values ...string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), "warp") {
			return true
		}
	}
	return false
}

func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return strings.Repeat("*", len(secret))
	}
	return secret[:4] + strings.Repeat("*", len(secret)-8) + secret[len(secret)-4:]
}
