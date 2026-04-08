package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ApplyForwardRules adds iptables FORWARD and OUTPUT rules so traffic can
// flow through the VPN tunnel interface. Uses iptables-restore --noflush
// to add rules atomically without disturbing existing chains.
func ApplyForwardRules(ctx context.Context, interfaceName string) error {
	rules := fmt.Sprintf("*filter\n"+
		"-A FORWARD -i %s -j ACCEPT\n"+
		"-A FORWARD -o %s -j ACCEPT\n"+
		"-A OUTPUT -o %s -j ACCEPT\n"+
		"COMMIT\n", interfaceName, interfaceName, interfaceName)

	cmd := exec.CommandContext(ctx, "iptables-restore", "--noflush")
	cmd.Stdin = strings.NewReader(rules)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables-restore --noflush: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// RemoveForwardRules removes the FORWARD and OUTPUT rules added by ApplyForwardRules.
func RemoveForwardRules(ctx context.Context, interfaceName string) error {
	cmds := [][]string{
		{"iptables", "-D", "FORWARD", "-i", interfaceName, "-j", "ACCEPT"},
		{"iptables", "-D", "FORWARD", "-o", interfaceName, "-j", "ACCEPT"},
		{"iptables", "-D", "OUTPUT", "-o", interfaceName, "-j", "ACCEPT"},
	}
	var errs []error
	for _, args := range cmds {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(output))
			if !strings.Contains(strings.ToLower(msg), "no chain/target/match") &&
				!strings.Contains(strings.ToLower(msg), "does a matching rule exist") {
				errs = append(errs, fmt.Errorf("%s: %w (%s)", strings.Join(args, " "), err, msg))
			}
		}
	}
	return errorsJoin(errs)
}
