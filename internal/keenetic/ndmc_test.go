package keenetic

import "testing"

func TestRouterInterfaceName(t *testing.T) {
	if got := RouterInterfaceName("opkgtun7"); got != "OpkgTun7" {
		t.Fatalf("unexpected router interface name: %s", got)
	}
}

func TestCIDRToIPMask(t *testing.T) {
	ip, mask, err := cidrToIPMask("10.250.7.1/30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.250.7.1" {
		t.Fatalf("unexpected ip: %s", ip)
	}
	if mask != "255.255.255.252" {
		t.Fatalf("unexpected mask: %s", mask)
	}
}
