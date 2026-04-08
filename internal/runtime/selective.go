package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hysteria-keenetic/internal/keenetic"
	"hysteria-keenetic/internal/logs"
	"hysteria-keenetic/internal/state"
)

// SelectiveRouteManager routes only chosen domains/CIDRs through the VPN
// tunnel using Keenetic NDMS FQDN object groups and DNS proxy routes.
// The system default route is never touched.
type SelectiveRouteManager struct {
	rci       *keenetic.RCIClient
	logger    *logs.Logger
	statePath string
}

// selectiveState persists the artefacts we created so Deactivate can clean up
// even after a process restart.
type selectiveState struct {
	InterfaceName string   `json:"interfaceName"`
	FQDNGroups    []string `json:"fqdnGroups"`
	StaticRoutes  []string `json:"staticRoutes"`
	PinnedRoutes  []string `json:"pinnedRoutes"`
}

func NewSelectiveRouteManager(rci *keenetic.RCIClient, statePath string, logger *logs.Logger) *SelectiveRouteManager {
	return &SelectiveRouteManager{
		rci:       rci,
		logger:    logger,
		statePath: statePath,
	}
}

func (m *SelectiveRouteManager) Activate(ctx context.Context, subscriptionURL, tunnelHost, interfaceName string, cfg state.RoutingConfig) error {
	interfaceName = strings.TrimSpace(interfaceName)
	if interfaceName == "" {
		return fmt.Errorf("selective route activation requires interface name")
	}
	if err := waitForInterface(ctx, interfaceName); err != nil {
		return err
	}

	ss := selectiveState{InterfaceName: interfaceName}

	// 1. Pin VPN server endpoint through WAN gateway so tunnel packets don't loop.
	for _, cidr := range pinnedCIDRs(subscriptionURL, tunnelHost) {
		ipv6 := strings.Contains(cidr, ":")
		hostArgs, err := routeArgsForDestination(ctx, ipv6, cidr)
		if err != nil || len(hostArgs) == 0 {
			continue
		}
		args := append([]string{"route", "replace", cidr}, hostArgs...)
		if err := runIPCmd(ctx, ipv6, m.logger, args...); err != nil {
			return err
		}
		ss.PinnedRoutes = append(ss.PinnedRoutes, cidr)
	}

	// 2. FQDN object groups + DNS proxy routes.
	ndmsIface := keenetic.RouterInterfaceName(interfaceName)
	for _, dg := range cfg.DomainGroups {
		if !dg.Enabled || len(dg.Domains) == 0 {
			continue
		}
		if err := m.rci.SyncFQDNGroup(ctx, dg.Name, dg.Domains); err != nil {
			return fmt.Errorf("sync fqdn group %s: %w", dg.Name, err)
		}
		if err := m.rci.SyncDNSProxyRoute(ctx, dg.Name, ndmsIface); err != nil {
			return fmt.Errorf("sync dns-proxy route %s: %w", dg.Name, err)
		}
		ss.FQDNGroups = append(ss.FQDNGroups, dg.Name)
		m.log("selective: applied dns routing for group %s -> %s", dg.Name, ndmsIface)
	}

	// 3. Static IP/CIDR routes.
	for _, sr := range cfg.StaticRoutes {
		if !sr.Enabled || strings.TrimSpace(sr.CIDR) == "" {
			continue
		}
		cidr := strings.TrimSpace(sr.CIDR)
		ipv6 := strings.Contains(cidr, ":")
		if err := runIPCmd(ctx, ipv6, m.logger, "route", "replace", cidr, "dev", interfaceName); err != nil {
			return fmt.Errorf("static route %s: %w", cidr, err)
		}
		ss.StaticRoutes = append(ss.StaticRoutes, cidr)
		m.log("selective: added static route %s dev %s", cidr, interfaceName)
	}

	// 4. Firewall FORWARD rules.
	if err := ApplyForwardRules(ctx, interfaceName); err != nil {
		m.log("selective: firewall rules warning: %v", err)
	}

	// 5. Save NDMS config.
	if err := m.rci.Save(ctx); err != nil {
		m.log("selective: ndms save warning: %v", err)
	}

	return m.saveState(ss)
}

func (m *SelectiveRouteManager) Deactivate(ctx context.Context) error {
	ss, err := m.loadState()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var errs []error

	// Remove DNS proxy routes and FQDN groups.
	for _, name := range ss.FQDNGroups {
		if err := m.rci.DeleteDNSProxyRoute(ctx, name); err != nil {
			errs = append(errs, err)
		}
		if err := m.rci.DeleteFQDNGroup(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}

	// Remove static routes.
	for _, cidr := range ss.StaticRoutes {
		ipv6 := strings.Contains(cidr, ":")
		if err := runIPCmd(ctx, ipv6, m.logger, "route", "del", cidr, "dev", ss.InterfaceName); err != nil {
			m.log("selective: failed to delete static route %s: %v", cidr, err)
		}
	}

	// Remove pinned endpoint routes.
	for _, cidr := range ss.PinnedRoutes {
		ipv6 := strings.Contains(cidr, ":")
		if err := runIPCmd(ctx, ipv6, m.logger, "route", "del", cidr); err != nil {
			m.log("selective: failed to delete pinned route %s: %v", cidr, err)
		}
	}

	// Remove firewall rules.
	if ss.InterfaceName != "" {
		if err := RemoveForwardRules(ctx, ss.InterfaceName); err != nil {
			m.log("selective: firewall cleanup warning: %v", err)
		}
	}

	// Save NDMS config.
	if err := m.rci.Save(ctx); err != nil {
		m.log("selective: ndms save on deactivate: %v", err)
	}

	if removeErr := os.Remove(m.statePath); removeErr != nil && !os.IsNotExist(removeErr) {
		errs = append(errs, removeErr)
	}

	return errorsJoin(errs)
}

// EnsureSubscriptionReachable is a no-op in selective mode because the
// default route is never replaced — the subscription URL is always reachable
// through the normal WAN gateway.
func (m *SelectiveRouteManager) EnsureSubscriptionReachable(_ context.Context, _ string) error {
	return nil
}

func (m *SelectiveRouteManager) log(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}

func (m *SelectiveRouteManager) saveState(ss selectiveState) error {
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ss, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.statePath, data, 0o600)
}

func (m *SelectiveRouteManager) loadState() (selectiveState, error) {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return selectiveState{}, err
	}
	var ss selectiveState
	if err := json.Unmarshal(data, &ss); err != nil {
		return selectiveState{}, err
	}
	return ss, nil
}

// runIPCmd is a shared helper for running ip commands with logging.
func runIPCmd(ctx context.Context, ipv6 bool, logger *logs.Logger, args ...string) error {
	fullArgs := append([]string{}, args...)
	if ipv6 {
		fullArgs = append([]string{"-6"}, fullArgs...)
	}
	cmd := exec.CommandContext(ctx, "ip", fullArgs...)
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if logger != nil {
		logger.Printf("route cmd ip %s output=%s", strings.Join(fullArgs, " "), trimmed)
	}
	if err != nil {
		if trimmed == "" {
			return fmt.Errorf("ip %s: %w", strings.Join(fullArgs, " "), err)
		}
		return fmt.Errorf("ip %s: %w (%s)", strings.Join(fullArgs, " "), err, trimmed)
	}
	return nil
}

// NewRoutingStrategy returns the appropriate RoutingStrategy based on mode.
func NewRoutingStrategy(mode string, rci *keenetic.RCIClient, statePath string, logger *logs.Logger) RoutingStrategy {
	if mode == "global" {
		return NewRouteManager(statePath, logger)
	}
	return NewSelectiveRouteManager(rci, statePath, logger)
}

// pinnedCIDRs, waitForInterface, routeArgsForDestination, errorsJoin
// are defined in routes.go and reused here (same package).
