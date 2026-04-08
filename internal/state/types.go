package state

type AppState struct {
	Subscription SubscriptionSource `json:"subscription"`
	Tunnels      []TunnelProfile    `json:"tunnels"`
	Runtime      RuntimeStatus      `json:"runtime"`
}

type SubscriptionSource struct {
	URL                  string `json:"url"`
	HWID                 string `json:"hwid"`
	UserAgent            string `json:"userAgent"`
	LastRefreshAt        string `json:"lastRefreshAt"`
	LastError            string `json:"lastError"`
	RefreshIntervalHours int    `json:"refreshIntervalHours"`
}

type TunnelProfile struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	InterfaceName          string   `json:"interfaceName"`
	Server                 string   `json:"server"`
	Port                   int      `json:"port"`
	Auth                   string   `json:"-"`
	AuthMasked             string   `json:"authMasked"`
	SNI                    string   `json:"sni"`
	ALPN                   []string `json:"alpn"`
	IsWarp                 bool     `json:"isWarp"`
	Active                 bool     `json:"active"`
	Missing                bool     `json:"missing"`
	LastSeenInSubscription string   `json:"lastSeenInSubscription"`
	LastUpdatedAt          string   `json:"lastUpdatedAt"`
}

type RuntimeStatus struct {
	State          string `json:"state"`
	ActiveTunnelID string `json:"activeTunnelId"`
	PID            int    `json:"pid"`
	Connected      bool   `json:"connected"`
	LastConnectAt  string `json:"lastConnectAt"`
	LastError      string `json:"lastError"`
}

func DefaultState(defaultRefreshHours int) AppState {
	return AppState{
		Subscription: SubscriptionSource{
			UserAgent:            "v2raytun",
			RefreshIntervalHours: defaultRefreshHours,
		},
		Runtime: RuntimeStatus{
			State: "stopped",
		},
		Tunnels: []TunnelProfile{},
	}
}
