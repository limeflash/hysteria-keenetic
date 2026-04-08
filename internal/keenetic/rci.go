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

func (c *RCIClient) DeleteInterface(ctx context.Context, systemName string) error {
	systemName = strings.TrimSpace(systemName)
	if systemName == "" {
		return nil
	}

	endpoint := fmt.Sprintf("%s/rci/interface?name=%s", c.baseURL, url.QueryEscape(systemName))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if c.logger != nil {
		c.logger.Printf("rci delete %s status=%d response=%s", endpoint, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rci returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if err := walkRCIValue(decoded); err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "unable to find") || strings.Contains(message, "not found") {
			return nil
		}
		return err
	}
	return nil
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

// FQDNGroupPrefix prevents name collisions with other managers (e.g. awg-manager).
const FQDNGroupPrefix = "hm-"

// SyncFQDNGroup creates or replaces an FQDN object group in Keenetic NDMS.
// Keenetic's DNS proxy resolves these domains and keeps IP→interface mapping.
func (c *RCIClient) SyncFQDNGroup(ctx context.Context, groupName string, domains []string) error {
	fullName := FQDNGroupPrefix + groupName

	// First delete the existing group so members are replaced, not merged.
	_ = c.DeleteFQDNGroup(ctx, groupName)

	members := make([]map[string]any, 0, len(domains))
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		members = append(members, map[string]any{"fqdn": d})
	}
	if len(members) == 0 {
		return nil
	}

	_, err := c.PostBatch(ctx, []any{
		map[string]any{
			"object-group": map[string]any{
				"fqdn": map[string]any{
					"name":   fullName,
					"member": members,
				},
			},
		},
	})
	return err
}

// DeleteFQDNGroup removes an FQDN object group from Keenetic NDMS.
func (c *RCIClient) DeleteFQDNGroup(ctx context.Context, groupName string) error {
	fullName := FQDNGroupPrefix + groupName
	_, err := c.PostBatch(ctx, []any{
		map[string]any{
			"no object-group": map[string]any{
				"fqdn": map[string]any{
					"name": fullName,
				},
			},
		},
	})
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "unable to find") {
			return nil
		}
	}
	return err
}

// SyncDNSProxyRoute tells Keenetic to route IPs resolved from the FQDN group
// through the specified interface. This is the core of selective routing.
func (c *RCIClient) SyncDNSProxyRoute(ctx context.Context, groupName, interfaceSystemName string) error {
	fullName := FQDNGroupPrefix + groupName
	_, err := c.PostBatch(ctx, []any{
		map[string]any{
			"dns-proxy": map[string]any{
				"route": map[string]any{
					"fqdn":      fullName,
					"interface": interfaceSystemName,
				},
			},
		},
	})
	return err
}

// DeleteDNSProxyRoute removes a DNS proxy route for the given FQDN group.
func (c *RCIClient) DeleteDNSProxyRoute(ctx context.Context, groupName string) error {
	fullName := FQDNGroupPrefix + groupName
	_, err := c.PostBatch(ctx, []any{
		map[string]any{
			"no dns-proxy": map[string]any{
				"route": map[string]any{
					"fqdn": fullName,
				},
			},
		},
	})
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "unable to find") {
			return nil
		}
	}
	return err
}
