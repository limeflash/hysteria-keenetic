package keenetic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hysteria-keenetic/internal/logs"
)

type RCIClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *logs.Logger
}

func NewRCIClient(baseURL string, logger *logs.Logger) *RCIClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:79"
	}
	return &RCIClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

func (c *RCIClient) SyncOpkgTun(ctx context.Context, cfg OpkgTunConfig) error {
	systemName := RouterInterfaceName(cfg.InterfaceName)
	if systemName == "" {
		return fmt.Errorf("invalid interface name %q", cfg.InterfaceName)
	}

	ipAddress, ipMask, err := cidrToIPMask(cfg.IPv4CIDR)
	if err != nil {
		return err
	}

	exists, err := c.InterfaceExists(ctx, systemName)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := c.PostBatch(ctx, []any{
			map[string]any{
				"interface": map[string]any{
					"name": systemName,
				},
			},
		}); err != nil {
			return err
		}
	}

	var batch []any
	batch = append(batch, map[string]any{
		"interface": map[string]any{
			"name": systemName,
			"ip": map[string]any{
				"global": map[string]any{
					"enabled": true,
					"order":   0,
				},
				"address": []map[string]any{
					{
						"address": ipAddress,
						"mask":    ipMask,
					},
				},
				"mtu": cfg.MTU,
			},
		},
	})

	descriptionPayload := map[string]any{
		"interface": map[string]any{
			systemName: map[string]any{
				"description": strings.TrimSpace(cfg.Description),
				"up":          cfg.Enabled,
			},
		},
	}
	batch = append(batch, descriptionPayload)

	if cfg.Enabled {
		batch = append(batch, map[string]any{
			"interface": map[string]any{
				systemName: map[string]any{
					"up": true,
				},
			},
		})
	}

	if _, err := c.PostBatch(ctx, batch); err != nil {
		return err
	}
	return nil
}

func (c *RCIClient) InterfaceExists(ctx context.Context, systemName string) (bool, error) {
	path := fmt.Sprintf("%s/rci/interface?name=%s", c.baseURL, url.QueryEscape(systemName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if c.logger != nil {
		c.logger.Printf("rci get %s status=%d response=%s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("rci returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, err
	}
	_, ok := decoded[systemName]
	return ok, nil
}

func (c *RCIClient) Save(ctx context.Context) error {
	_, err := c.PostBatch(ctx, []any{
		map[string]any{
			"system": map[string]any{
				"configuration": map[string]any{
					"save": map[string]any{},
				},
			},
		},
	})
	return err
}

func (c *RCIClient) PostBatch(ctx context.Context, payload []any) ([]map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := c.baseURL + "/rci/"
	if c.logger != nil {
		c.logger.Printf("rci post %s payload=%s", url, string(body))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if c.logger != nil {
		c.logger.Printf("rci post %s status=%d response=%s", url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rci returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}

	if err := firstRCIError(decoded); err != nil {
		return decoded, err
	}

	return decoded, nil
}

func firstRCIError(items []map[string]any) error {
	for _, item := range items {
		if err := walkRCIValue(item); err != nil {
			return err
		}
	}
	return nil
}

func walkRCIValue(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		if rawStatus, ok := typed["status"]; ok {
			if statuses, ok := rawStatus.([]any); ok {
				for _, statusItem := range statuses {
					statusMap, ok := statusItem.(map[string]any)
					if !ok {
						continue
					}
					if strings.EqualFold(stringValue(statusMap["status"]), "error") {
						return fmt.Errorf("%s", stringValue(statusMap["message"]))
					}
				}
			}
		}
		for _, next := range typed {
			if err := walkRCIValue(next); err != nil {
				return err
			}
		}
	case []any:
		for _, next := range typed {
			if err := walkRCIValue(next); err != nil {
				return err
			}
		}
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}
