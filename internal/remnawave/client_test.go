package remnawave

import "testing"

func TestParseProfiles(t *testing.T) {
	envelopes := []xrayEnvelope{
		{
			Remarks: "Germany",
			Outbounds: []xrayOutbound{
				{
					Protocol: "hysteria",
					Settings: xrayOutboundSettings{
						Address: "ge1.example.com",
						Port:    443,
						Version: 2,
					},
					StreamSettings: xrayStreamSettings{
						HysteriaSettings: xrayHysteriaSettings{
							Version: 2,
							Auth:    "secret-value",
						},
						TLSSettings: xrayTLSSettings{
							ServerName: "ge1.example.com",
							ALPN:       []string{"h3"},
						},
					},
				},
			},
		},
		{
			Remarks: "Germany WARP",
			Outbounds: []xrayOutbound{
				{
					Protocol: "hysteria",
					Settings: xrayOutboundSettings{
						Address: "ge1-warp.example.com",
						Port:    8443,
						Version: 2,
					},
					StreamSettings: xrayStreamSettings{
						HysteriaSettings: xrayHysteriaSettings{
							Version: 2,
							Auth:    "secret-value",
						},
						TLSSettings: xrayTLSSettings{
							ServerName: "ge1-warp.example.com",
							ALPN:       []string{"h3"},
						},
					},
				},
			},
		},
	}

	profiles := parseProfiles(envelopes)
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}

	if profiles[0].Name != "Germany" || profiles[0].Server != "ge1.example.com" || profiles[0].Port != 443 {
		t.Fatalf("unexpected first profile: %#v", profiles[0])
	}

	if !profiles[1].IsWarp {
		t.Fatalf("expected second profile to be marked as warp")
	}
}

func TestMaskSecret(t *testing.T) {
	masked := MaskSecret("1234567890")
	if masked != "1234**7890" {
		t.Fatalf("unexpected mask: %s", masked)
	}
}
