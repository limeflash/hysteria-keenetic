package app

import (
	"os"
	"path/filepath"
)

const (
	defaultBaseDir       = "/opt/etc/hysteria-manager"
	defaultProfilesDir   = "/opt/etc/hysteria-manager/profiles"
	defaultLogDir        = "/opt/var/log/hysteria-manager"
	defaultListenAddress = "0.0.0.0:2230"
	defaultHysteriaBin   = "/opt/bin/hysteria"
)

type Config struct {
	BaseDir             string
	ProfilesDir         string
	LogDir              string
	ListenAddress       string
	HysteriaBinaryPath  string
	KeeneticBaseURL     string
	KeeneticRCIURL      string
	StateFilePath       string
	ManagerLogPath      string
	HysteriaLogPath     string
	RuntimeConfigPath   string
	DefaultRefreshHours int
}

func LoadConfigFromEnv() Config {
	baseDir := envOrDefault("HM_BASE_DIR", defaultBaseDir)
	profilesDir := envOrDefault("HM_PROFILES_DIR", filepath.Join(baseDir, "profiles"))
	logDir := envOrDefault("HM_LOG_DIR", defaultLogDir)

	return Config{
		BaseDir:             baseDir,
		ProfilesDir:         profilesDir,
		LogDir:              logDir,
		ListenAddress:       envOrDefault("HM_LISTEN_ADDR", defaultListenAddress),
		HysteriaBinaryPath:  envOrDefault("HYSTERIA_BINARY", defaultHysteriaBin),
		KeeneticBaseURL:     envOrDefault("KEENETIC_BASE_URL", "http://127.0.0.1"),
		KeeneticRCIURL:      envOrDefault("KEENETIC_RCI_URL", "http://127.0.0.1:79"),
		StateFilePath:       envOrDefault("HM_STATE_FILE", filepath.Join(baseDir, "state.json")),
		ManagerLogPath:      envOrDefault("HM_MANAGER_LOG", filepath.Join(logDir, "manager.log")),
		HysteriaLogPath:     envOrDefault("HM_HYSTERIA_LOG", filepath.Join(logDir, "hysteria.log")),
		RuntimeConfigPath:   envOrDefault("HM_RUNTIME_CONFIG", filepath.Join(baseDir, "runtime.yaml")),
		DefaultRefreshHours: 12,
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
