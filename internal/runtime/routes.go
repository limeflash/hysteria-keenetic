package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hysteria-keenetic/internal/logs"
)

type RouteManager struct {
	statePath string
	logger    *logs.Logger
}

type RouteState struct {
	InterfaceName    string   `json:"interfaceName"`
	IPv4DefaultRoute string   `json:"ipv4DefaultRoute"`
	IPv6DefaultRoute string   `json:"ipv6DefaultRoute"`
	PinnedIPv4Routes []string `json:"pinnedIPv4Routes"`
	PinnedIPv6Routes []string `json:"pinnedIPv6Routes"`
}

func NewRouteManager(statePath string, logger *logs.Logger) *RouteManager {
	return &RouteManager{
		statePath: statePath,
		logger:    logger,
	}
}

func (m *RouteManager) Activate(ctx context.Context, subscriptionURL, tunnelHost, interfaceName string) error {
	state := RouteState{
		InterfaceName:    strings.TrimSpace(interfaceName),
		IPv4DefaultRoute: firstRouteLine(runIP(ctx, false, "route", "show", "default")),
		IPv6DefaultRoute: firstRouteLine(runIP(ctx, true, "route", "show", "default")),
	}

	if state.InterfaceName == "" {
		return fmt.Errorf("route activation requires interface name")
	}
	if err := waitForInterface(ctx, state.InterfaceName); err != nil {
		return err
	}

	for _, cidr := range pinnedCIDRs(subscriptionURL, tunnelHost) {
		if strings.Contains(cidr, ":") {
			hostArgs, err := routeArgsForDestination(ctx, true, cidr)
			if err != nil {
				return err
			}
			if len(hostArgs) == 0 {
				continue
			}
			if err := m.run(ctx, true, append([]string{"route", "replace", cidr}, hostArgs...)...); err != nil {
				return err
			}
			state.PinnedIPv6Routes = append(state.PinnedIPv6Routes, cidr)
			continue
		}
		hostArgs, err := routeArgsForDestination(ctx, false, cidr)
		if err != nil {
			return err
		}
		if len(hostArgs) == 0 {
			continue
		}
		if err := m.run(ctx, false, append([]string{"route", "replace", cidr}, hostArgs...)...); err != nil {
			return err
		}
		state.PinnedIPv4Routes = append(state.PinnedIPv4Routes, cidr)
	}

	if err := m.run(ctx, false, "route", "replace", "default", "dev", state.InterfaceName); err != nil {
		return err
	}
	if state.IPv6DefaultRoute != "" {
		if err := m.run(ctx, true, "route", "replace", "default", "dev", state.InterfaceName); err != nil {
			m.logger.Printf("failed to switch ipv6 default route to %s: %v", state.InterfaceName, err)
		}
	}

	return m.save(state)
}

func (m *RouteManager) EnsureSubscriptionReachable(ctx context.Context, subscriptionURL string) error {
	var state RouteState
	if loaded, err := m.load(); err == nil {
		state = loaded
	} else if !os.IsNotExist(err) {
		return err
	}

	ipv4Base := strings.TrimSpace(state.IPv4DefaultRoute)
	ipv6Base := strings.TrimSpace(state.IPv6DefaultRoute)
	if ipv4Base == "" {
		ipv4Base = firstRouteLine(runIP(ctx, false, "route", "show", "default"))
	}
	if ipv6Base == "" {
		ipv6Base = firstRouteLine(runIP(ctx, true, "route", "show", "default"))
	}

	ipv4Args := routeArgs(strings.Fields(ipv4Base))
	ipv6Args := routeArgs(strings.Fields(ipv6Base))
	if len(ipv4Args) == 0 && len(ipv6Args) == 0 {
		return nil
	}

	parsed, err := neturl.Parse(strings.TrimSpace(subscriptionURL))
	if err != nil || parsed.Hostname() == "" {
		return nil
	}

	ips, err := net.LookupIP(parsed.Hostname())
	if err != nil {
		return nil
	}

	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			if len(ipv4Args) == 0 {
				continue
			}
			cidr := v4.String() + "/32"
			if m.logger != nil {
				m.logger.Printf("ensure subscription route %s via %s", cidr, strings.Join(ipv4Args, " "))
			}
			if err := m.run(ctx, false, append([]string{"route", "replace", cidr}, ipv4Args...)...); err != nil {
				return err
			}
			continue
		}
		if len(ipv6Args) == 0 {
			continue
		}
		cidr := ip.String() + "/128"
		if m.logger != nil {
			m.logger.Printf("ensure subscription route %s via %s", cidr, strings.Join(ipv6Args, " "))
		}
		if err := m.run(ctx, true, append([]string{"route", "replace", cidr}, ipv6Args...)...); err != nil {
			return err
		}
	}

	return nil
}

func (m *RouteManager) Deactivate(ctx context.Context) error {
	state, err := m.load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var errs []error
	if strings.TrimSpace(state.IPv4DefaultRoute) != "" {
		if err := m.restoreDefaultRoute(ctx, false, state.IPv4DefaultRoute); err != nil {
			errs = append(errs, err)
		}
	} else if state.InterfaceName != "" {
		if err := m.run(ctx, false, "route", "del", "default", "dev", state.InterfaceName); err != nil {
			errs = append(errs, err)
		}
	}

	if strings.TrimSpace(state.IPv6DefaultRoute) != "" {
		if err := m.restoreDefaultRoute(ctx, true, state.IPv6DefaultRoute); err != nil {
			errs = append(errs, err)
		}
	}

	for _, cidr := range state.PinnedIPv4Routes {
		if err := m.run(ctx, false, "route", "del", cidr); err != nil {
			m.logger.Printf("failed to delete pinned ipv4 route %s: %v", cidr, err)
		}
	}
	for _, cidr := range state.PinnedIPv6Routes {
		if err := m.run(ctx, true, "route", "del", cidr); err != nil {
			m.logger.Printf("failed to delete pinned ipv6 route %s: %v", cidr, err)
		}
	}

	if removeErr := os.Remove(m.statePath); removeErr != nil && !os.IsNotExist(removeErr) {
		errs = append(errs, removeErr)
	}

	return errorsJoin(errs)
}

func (m *RouteManager) save(state RouteState) error {
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.statePath, data, 0o600)
}

func (m *RouteManager) load() (RouteState, error) {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return RouteState{}, err
	}
	var state RouteState
	if err := json.Unmarshal(data, &state); err != nil {
		return RouteState{}, err
	}
	return state, nil
}

func (m *RouteManager) restoreDefaultRoute(ctx context.Context, ipv6 bool, routeLine string) error {
	parts := strings.Fields(strings.TrimSpace(routeLine))
	if len(parts) == 0 {
		return nil
	}
	args := append([]string{"route", "replace"}, parts...)
	return m.run(ctx, ipv6, args...)
}

func (m *RouteManager) run(ctx context.Context, ipv6 bool, args ...string) error {
	fullArgs := append([]string{}, args...)
	if ipv6 {
		fullArgs = append([]string{"-6"}, fullArgs...)
	}

	cmd := exec.CommandContext(ctx, "ip", fullArgs...)
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if m.logger != nil {
		m.logger.Printf("route cmd ip %s output=%s", strings.Join(fullArgs, " "), trimmed)
	}
	if err != nil {
		if trimmed == "" {
			return fmt.Errorf("ip %s: %w", strings.Join(fullArgs, " "), err)
		}
		return fmt.Errorf("ip %s: %w (%s)", strings.Join(fullArgs, " "), err, trimmed)
	}
	return nil
}

func runIP(ctx context.Context, ipv6 bool, args ...string) string {
	fullArgs := append([]string{}, args...)
	if ipv6 {
		fullArgs = append([]string{"-6"}, fullArgs...)
	}
	output, err := exec.CommandContext(ctx, "ip", fullArgs...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func routeArgs(fields []string) []string {
	if len(fields) == 0 {
		return nil
	}

	var args []string
	for idx := 0; idx < len(fields); idx++ {
		switch fields[idx] {
		case "via":
			if idx+1 < len(fields) {
				args = append(args, "via", fields[idx+1])
				idx++
			}
		case "dev":
			if idx+1 < len(fields) {
				args = append(args, "dev", fields[idx+1])
				idx++
			}
		case "src", "metric", "proto", "scope", "pref", "uid", "cache":
			return args
		}
	}
	return args
}

func routeArgsForDestination(ctx context.Context, ipv6 bool, cidr string) ([]string, error) {
	target := cidr
	if strings.Contains(target, "/") {
		host, _, err := net.ParseCIDR(target)
		if err == nil {
			target = host.String()
		} else {
			target = strings.SplitN(target, "/", 2)[0]
		}
	}

	output := runIP(ctx, ipv6, "route", "get", target)
	if output == "" {
		return nil, fmt.Errorf("unable to resolve route for %s", cidr)
	}

	args := routeArgs(strings.Fields(output))
	if len(args) == 0 {
		return nil, fmt.Errorf("unable to parse route for %s from %q", cidr, output)
	}
	return args, nil
}

func pinnedCIDRs(subscriptionURL, tunnelHost string) []string {
	seen := map[string]struct{}{}
	var cidrs []string

	addHost := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" {
			return
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return
		}
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				cidr := v4.String() + "/32"
				if _, ok := seen[cidr]; ok {
					continue
				}
				seen[cidr] = struct{}{}
				cidrs = append(cidrs, cidr)
				continue
			}
			cidr := ip.String() + "/128"
			if _, ok := seen[cidr]; ok {
				continue
			}
			seen[cidr] = struct{}{}
			cidrs = append(cidrs, cidr)
		}
	}

	if parsed, err := neturl.Parse(strings.TrimSpace(subscriptionURL)); err == nil {
		addHost(parsed.Hostname())
	}
	addHost(tunnelHost)

	return cidrs
}

func errorsJoin(errs []error) error {
	var filtered []error
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	var builder strings.Builder
	for idx, err := range filtered {
		if idx > 0 {
			builder.WriteString("; ")
		}
		builder.WriteString(err.Error())
	}
	return fmt.Errorf("%s", builder.String())
}

func waitForInterface(ctx context.Context, interfaceName string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, err := net.InterfaceByName(interfaceName); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for interface %s: %w", interfaceName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func firstRouteLine(output string) string {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line
	}
	return ""
}
