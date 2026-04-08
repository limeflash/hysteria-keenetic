package keenetic

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type NDMCClient struct {
	binaryPath string
}

type OpkgTunConfig struct {
	InterfaceName string
	Description   string
	IPv4CIDR      string
	IPv6CIDR      string
	MTU           int
	Enabled       bool
}

func NewNDMCClient(binaryPath string) *NDMCClient {
	if strings.TrimSpace(binaryPath) == "" {
		binaryPath = "ndmc"
	}
	return &NDMCClient{binaryPath: binaryPath}
}

func (c *NDMCClient) SyncOpkgTun(ctx context.Context, cfg OpkgTunConfig) error {
	systemName := RouterInterfaceName(cfg.InterfaceName)
	if systemName == "" {
		return fmt.Errorf("invalid interface name %q", cfg.InterfaceName)
	}

	commands := []string{
		"interface " + systemName,
		fmt.Sprintf("interface %s description %s", systemName, strconv.Quote(strings.TrimSpace(cfg.Description))),
		fmt.Sprintf("interface %s security-level public", systemName),
		fmt.Sprintf("interface %s ip global auto", systemName),
	}

	if ip, mask, err := cidrToIPMask(cfg.IPv4CIDR); err == nil && ip != "" && mask != "" {
		commands = append(commands, fmt.Sprintf("interface %s ip address %s %s", systemName, ip, mask))
	}

	if cfg.MTU > 0 {
		commands = append(commands, fmt.Sprintf("interface %s ip mtu %d", systemName, cfg.MTU))
		commands = append(commands, fmt.Sprintf("interface %s ip tcp adjust-mss pmtu", systemName))
	}

	if cfg.Enabled {
		commands = append(commands, fmt.Sprintf("interface %s up", systemName))
	}

	for _, command := range commands {
		if err := c.run(ctx, command); err != nil {
			return err
		}
	}

	return nil
}

func (c *NDMCClient) Save(ctx context.Context) error {
	return c.run(ctx, "system configuration save")
}

func RouterInterfaceName(interfaceName string) string {
	trimmed := strings.TrimSpace(interfaceName)
	if trimmed == "" {
		return ""
	}
	lowered := strings.ToLower(trimmed)
	if !strings.HasPrefix(lowered, "opkgtun") {
		return trimmed
	}
	return "OpkgTun" + trimmed[len("opkgtun"):]
}

func (c *NDMCClient) run(ctx context.Context, command string) error {
	cmd := exec.CommandContext(ctx, c.resolveBinaryPath(), "-c", command)
	cmd.Env = cleanNDMCEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("ndmc %q failed: %s", command, message)
	}
	return nil
}

func (c *NDMCClient) resolveBinaryPath() string {
	if strings.Contains(c.binaryPath, "/") {
		return c.binaryPath
	}
	for _, candidate := range []string{"/bin/ndmc", "/sbin/ndmc", "/usr/bin/ndmc", "/usr/sbin/ndmc"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return c.binaryPath
}

func cleanNDMCEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "LD_LIBRARY_PATH=") {
			continue
		}
		if strings.HasPrefix(item, "PATH=") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "LD_LIBRARY_PATH=")
	env = append(env, "PATH=/bin:/sbin:/usr/bin:/usr/sbin:/opt/bin:/opt/sbin")
	return env
}

func cidrToIPMask(cidr string) (string, string, error) {
	if strings.TrimSpace(cidr) == "" {
		return "", "", nil
	}
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", err
	}
	mask := net.IP(network.Mask).To4()
	if mask == nil {
		return "", "", fmt.Errorf("non-ipv4 cidr %q", cidr)
	}
	return ip.String(), mask.String(), nil
}
