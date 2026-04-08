package app

import (
	"testing"

	"hysteria-keenetic/internal/remnawave"
	"hysteria-keenetic/internal/state"
)

func TestMergeProfilesKeepsInterfaceNamesAndMarksMissing(t *testing.T) {
	existing := []state.TunnelProfile{
		{
			ID:            stableTunnelID(remnawave.Profile{Name: "A", Server: "a.example.com", Port: 443, Auth: "one"}),
			Name:          "A",
			InterfaceName: "opkgtun7",
			Server:        "a.example.com",
			Port:          443,
			Auth:          "one",
		},
		{
			ID:            stableTunnelID(remnawave.Profile{Name: "Old", Server: "old.example.com", Port: 8443, Auth: "two"}),
			Name:          "Old",
			InterfaceName: "opkgtun8",
			Server:        "old.example.com",
			Port:          8443,
			Auth:          "two",
		},
	}

	fresh := []remnawave.Profile{
		{Name: "A", Server: "a.example.com", Port: 443, Auth: "one"},
		{Name: "B", Server: "b.example.com", Port: 443, Auth: "three"},
	}

	merged := mergeProfiles(existing, fresh, "2026-04-08T00:00:00Z")
	if len(merged) != 3 {
		t.Fatalf("expected 3 profiles, got %d", len(merged))
	}

	var foundExisting, foundNew, foundMissing bool
	for _, item := range merged {
		switch item.Name {
		case "A":
			foundExisting = true
			if item.InterfaceName != "opkgtun7" {
				t.Fatalf("expected existing interface name to be preserved, got %s", item.InterfaceName)
			}
		case "B":
			foundNew = true
			if item.InterfaceName == "" {
				t.Fatalf("expected new tunnel to receive interface name")
			}
		case "Old":
			foundMissing = true
			if !item.Missing {
				t.Fatalf("expected old tunnel to be marked missing")
			}
		}
	}

	if !foundExisting || !foundNew || !foundMissing {
		t.Fatalf("missing expected tunnels after merge: %#v", merged)
	}
}
