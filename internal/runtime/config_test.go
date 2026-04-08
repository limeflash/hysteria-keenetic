package runtime

import (
	"strings"
	"testing"
)

func TestBuildClientConfigFormatsServerAsSingleString(t *testing.T) {
	config := BuildClientConfig(Profile{
		Name:          "Mock",
		InterfaceName: "opkgtun0",
		Server:        "127.0.0.1",
		Port:          8443,
		Auth:          "secret",
		SNI:           "example.com",
		ALPN:          []string{"h3"},
	}, RoutePlan{
		IPv4Excludes: []string{"192.168.0.0/16"},
		IPv6Excludes: []string{"fc00::/7"},
	})

	if !strings.Contains(config, "server: \"127.0.0.1:8443\"") {
		t.Fatalf("expected combined server field, got:\n%s", config)
	}

	if !strings.Contains(config, "name: \"opkgtun0\"") {
		t.Fatalf("expected interface name in config, got:\n%s", config)
	}
}
