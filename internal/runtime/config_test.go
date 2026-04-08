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
	})

	if !strings.Contains(config, "server: \"127.0.0.1:8443\"") {
		t.Fatalf("expected combined server field, got:\n%s", config)
	}

	if !strings.Contains(config, "name: \"opkgtun0\"") {
		t.Fatalf("expected interface name in config, got:\n%s", config)
	}

	if !strings.Contains(config, "ipv4: \"10.250.0.1/30\"") {
		t.Fatalf("expected deterministic IPv4 address, got:\n%s", config)
	}

	if strings.Contains(config, "\n  route:\n") {
		t.Fatalf("did not expect hysteria to install default routes automatically, got:\n%s", config)
	}
}

func TestDefaultTunSettingsUsesInterfaceIndex(t *testing.T) {
	settings := DefaultTunSettings("opkgtun7")
	if settings.IPv4CIDR != "10.250.7.1/30" {
		t.Fatalf("unexpected IPv4 CIDR: %s", settings.IPv4CIDR)
	}
	if settings.IPv6CIDR != "fd00:250:0:7::1/126" {
		t.Fatalf("unexpected IPv6 CIDR: %s", settings.IPv6CIDR)
	}
}
