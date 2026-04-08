package runtime

import (
	"fmt"
	"net"
	neturl "net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultTunTimeout    = "2m"
	defaultTunMTU        = 1400
	defaultBandwidthUp   = 100
	defaultBandwidthDown = 100
)

type Profile struct {
	Name          string
	InterfaceName string
	Server        string
	Port          int
	Auth          string
	SNI           string
	ALPN          []string
	BandwidthUp   int // Mbps, 0 = use default
	BandwidthDown int // Mbps, 0 = use default
}

type RoutePlan struct {
	IPv4Excludes []string
	IPv6Excludes []string
}

type TunSettings struct {
	IPv4CIDR string
	IPv6CIDR string
	MTU      int
	Timeout  string
}

func BuildClientConfig(profile Profile) string {
	tun := DefaultTunSettings(profile.InterfaceName)

	var builder strings.Builder

	fmt.Fprintf(&builder, "server: %s\n\n", yamlString(fmt.Sprintf("%s:%d", profile.Server, profile.Port)))
	fmt.Fprintf(&builder, "auth: %s\n\n", yamlString(profile.Auth))
	builder.WriteString("tls:\n")
	fmt.Fprintf(&builder, "  sni: %s\n", yamlString(profile.SNI))
	builder.WriteString("  insecure: false\n")
	if len(profile.ALPN) > 0 {
		builder.WriteString("  alpn:\n")
		for _, item := range profile.ALPN {
			fmt.Fprintf(&builder, "    - %s\n", yamlString(item))
		}
	}
	builder.WriteString("\n")

	bwUp := profile.BandwidthUp
	if bwUp <= 0 {
		bwUp = defaultBandwidthUp
	}
	bwDown := profile.BandwidthDown
	if bwDown <= 0 {
		bwDown = defaultBandwidthDown
	}
	builder.WriteString("bandwidth:\n")
	fmt.Fprintf(&builder, "  up: %d mbps\n", bwUp)
	fmt.Fprintf(&builder, "  down: %d mbps\n\n", bwDown)

	builder.WriteString("fastOpen: true\n\n")

	builder.WriteString("congestion:\n")
	builder.WriteString("  type: bbr\n\n")
	builder.WriteString("tun:\n")
	fmt.Fprintf(&builder, "  name: %s\n", yamlString(profile.InterfaceName))
	fmt.Fprintf(&builder, "  mtu: %d\n", tun.MTU)
	fmt.Fprintf(&builder, "  timeout: %s\n", tun.Timeout)
	builder.WriteString("  address:\n")
	fmt.Fprintf(&builder, "    ipv4: %s\n", yamlString(tun.IPv4CIDR))
	fmt.Fprintf(&builder, "    ipv6: %s\n", yamlString(tun.IPv6CIDR))
	return builder.String()
}

func DefaultTunSettings(interfaceName string) TunSettings {
	index := interfaceIndex(interfaceName)
	if index < 0 {
		index = fallbackInterfaceIndex(interfaceName)
	}

	return TunSettings{
		IPv4CIDR: fmt.Sprintf("10.250.%d.1/30", index%256),
		IPv6CIDR: fmt.Sprintf("fd00:250:0:%x::1/126", index&0xffff),
		MTU:      defaultTunMTU,
		Timeout:  defaultTunTimeout,
	}
}

func BuildRoutePlan(subscriptionURL, tunnelHost string) (RoutePlan, error) {
	ipv4Set := map[string]struct{}{
		"127.0.0.0/8":    {},
		"10.0.0.0/8":     {},
		"172.16.0.0/12":  {},
		"192.168.0.0/16": {},
		"169.254.0.0/16": {},
	}
	ipv6Set := map[string]struct{}{
		"::1/128":   {},
		"fc00::/7":  {},
		"fe80::/10": {},
	}

	subscriptionHost, err := hostFromURL(subscriptionURL)
	if err != nil {
		return RoutePlan{}, err
	}

	addResolvedCIDRs(ipv4Set, ipv6Set, subscriptionHost)
	addResolvedCIDRs(ipv4Set, ipv6Set, tunnelHost)
	addInterfaceCIDRs(ipv4Set, ipv6Set)
	addGatewayCIDRs(ipv4Set, ipv6Set)

	return RoutePlan{
		IPv4Excludes: sortedKeys(ipv4Set),
		IPv6Excludes: sortedKeys(ipv6Set),
	}, nil
}

func EnsureBinary(binaryPath string) error {
	if strings.Contains(binaryPath, "/") {
		_, err := exec.LookPath(binaryPath)
		return err
	}
	_, err := exec.LookPath(binaryPath)
	return err
}

func yamlString(value string) string {
	return fmt.Sprintf("%q", value)
}

func hostFromURL(raw string) (string, error) {
	parsed, err := neturl.Parse(raw)
	if err != nil {
		return "", err
	}
	return parsed.Hostname(), nil
}

func addResolvedCIDRs(ipv4Set, ipv6Set map[string]struct{}, host string) {
	if host == "" {
		return
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			cidr := (&net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}).String()
			ipv4Set[cidr] = struct{}{}
			continue
		}
		cidr := (&net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}).String()
		ipv6Set[cidr] = struct{}{}
	}
}

func addInterfaceCIDRs(ipv4Set, ipv6Set map[string]struct{}) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			prefix, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if prefix.IP.IsLoopback() {
				continue
			}
			if v4 := prefix.IP.To4(); v4 != nil {
				prefix.IP = v4
				ipv4Set[prefix.String()] = struct{}{}
				continue
			}
			ipv6Set[prefix.String()] = struct{}{}
		}
	}
}

func addGatewayCIDRs(ipv4Set, ipv6Set map[string]struct{}) {
	if output, err := exec.Command("sh", "-c", "ip route show default 2>/dev/null | awk '/default/ {print $3; exit}'").Output(); err == nil {
		gateway := strings.TrimSpace(string(output))
		if gateway != "" {
			addResolvedCIDRs(ipv4Set, ipv6Set, gateway)
		}
	}
	if output, err := exec.Command("sh", "-c", "ip -6 route show default 2>/dev/null | awk '/default/ {print $3; exit}'").Output(); err == nil {
		gateway := strings.TrimSpace(string(output))
		if gateway != "" {
			addResolvedCIDRs(ipv4Set, ipv6Set, gateway)
		}
	}
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func interfaceIndex(interfaceName string) int {
	trimmed := strings.TrimSpace(strings.ToLower(interfaceName))
	if !strings.HasPrefix(trimmed, "opkgtun") {
		return -1
	}

	value := strings.TrimPrefix(trimmed, "opkgtun")
	if value == "" {
		return -1
	}

	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return -1
	}
	return index
}

func fallbackInterfaceIndex(interfaceName string) int {
	sum := 0
	for _, char := range interfaceName {
		sum = (sum + int(char)) % 256
	}
	return sum
}
