package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"hysteria-keenetic/internal/logs"
	"hysteria-keenetic/internal/state"
)

type Manager struct {
	mu               sync.Mutex
	binaryPath       string
	configPath       string
	logPath          string
	logger           *logs.Logger
	onUnexpectedExit func(error)
	current          *processHandle
}

type processHandle struct {
	cmd           *exec.Cmd
	done          chan struct{}
	stopRequested bool
	waitErr       error
	interfaceName string
}

func NewManager(binaryPath, configPath, logPath string, logger *logs.Logger, onUnexpectedExit func(error)) *Manager {
	return &Manager{
		binaryPath:       binaryPath,
		configPath:       configPath,
		logPath:          logPath,
		logger:           logger,
		onUnexpectedExit: onUnexpectedExit,
	}
}

func (m *Manager) Activate(_ context.Context, profile Profile) (state.RuntimeStatus, error) {
	if err := EnsureBinary(m.binaryPath); err != nil {
		return state.RuntimeStatus{}, fmt.Errorf("hysteria binary is unavailable: %w", err)
	}

	configContent := BuildClientConfig(profile)
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0o755); err != nil {
		return state.RuntimeStatus{}, err
	}
	if err := os.WriteFile(m.configPath, []byte(configContent), 0o600); err != nil {
		return state.RuntimeStatus{}, err
	}

	if _, err := m.Deactivate("switching tunnel"); err != nil {
		return state.RuntimeStatus{}, err
	}
	knownInterfaces := listOpkgTunInterfaces()

	logFile, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return state.RuntimeStatus{}, err
	}

	cmd := exec.Command(m.binaryPath, "-c", m.configPath, "-l", "info", "client")
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return state.RuntimeStatus{}, err
	}

	handle := &processHandle{
		cmd:  cmd,
		done: make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		handle.waitErr = err
		close(handle.done)
	}()

	deadline := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var actualInterface string
	for actualInterface == "" {
		select {
		case <-handle.done:
			m.logger.Printf("hysteria process exited during startup: %v", handle.waitErr)
			return state.RuntimeStatus{}, fmt.Errorf("hysteria exited during startup: %w", handle.waitErr)
		case <-deadline:
			m.logger.Printf("hysteria startup timed out waiting for interface")
			_ = cmd.Process.Kill()
			<-handle.done
			return state.RuntimeStatus{}, fmt.Errorf("hysteria startup timed out: interface not ready after 15s")
		case <-ticker.C:
			iface := detectOpkgTunInterface(m.logPath, knownInterfaces)
			if iface == "" {
				continue
			}
			if _, err := net.InterfaceByName(iface); err != nil {
				continue
			}
			actualInterface = iface
		}
	}
	ticker.Stop()
	handle.interfaceName = actualInterface

	m.mu.Lock()
	m.current = handle
	m.mu.Unlock()

	go m.watch(handle)

	return state.RuntimeStatus{
		State:         "running",
		InterfaceName: actualInterface,
		PID:           cmd.Process.Pid,
		Connected:     true,
		LastConnectAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (m *Manager) Deactivate(reason string) (state.RuntimeStatus, error) {
	m.mu.Lock()
	current := m.current
	if current == nil {
		m.mu.Unlock()
		return state.RuntimeStatus{State: "stopped"}, nil
	}
	current.stopRequested = true
	m.current = nil
	m.mu.Unlock()

	if current.cmd.Process == nil {
		return state.RuntimeStatus{State: "stopped"}, nil
	}

	m.logger.Printf("stopping hysteria process: %s", reason)
	if err := current.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return state.RuntimeStatus{}, err
	}

	select {
	case <-current.done:
	case <-time.After(5 * time.Second):
		_ = current.cmd.Process.Kill()
		<-current.done
	}

	return state.RuntimeStatus{
		State: "stopped",
	}, nil
}

func (m *Manager) Status() state.RuntimeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil || m.current.cmd == nil || m.current.cmd.Process == nil {
		return state.RuntimeStatus{State: "stopped"}
	}

	return state.RuntimeStatus{
		State:         "running",
		InterfaceName: m.current.interfaceName,
		PID:           m.current.cmd.Process.Pid,
		Connected:     true,
	}
}

func (m *Manager) watch(handle *processHandle) {
	<-handle.done
	err := handle.waitErr

	m.mu.Lock()
	shouldNotify := !handle.stopRequested
	if m.current == handle {
		m.current = nil
	}
	m.mu.Unlock()

	if shouldNotify && m.onUnexpectedExit != nil {
		if err == nil {
			err = errors.New("hysteria process exited")
		}
		m.onUnexpectedExit(err)
	}
}

func listOpkgTunInterfaces() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	result := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(iface.Name)), "opkgtun") {
			result = append(result, iface.Name)
		}
	}
	return result
}

func detectOpkgTunInterface(logPath string, known []string) string {
	knownSet := make(map[string]struct{}, len(known))
	for _, name := range known {
		knownSet[name] = struct{}{}
	}

	re := regexp.MustCompile(`"interface":\s*"([^"]+)"`)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current := listOpkgTunInterfaces()
		for _, name := range current {
			if _, exists := knownSet[name]; !exists {
				return name
			}
		}

		data, err := os.ReadFile(logPath)
		if err == nil {
			matches := re.FindAllStringSubmatch(string(data), -1)
			for idx := len(matches) - 1; idx >= 0; idx-- {
				name := strings.TrimSpace(matches[idx][1])
				if name == "" || !strings.HasPrefix(strings.ToLower(name), "opkgtun") {
					continue
				}
				if len(knownSet) == 0 || !slices.Contains(known, name) {
					return name
				}
			}
		}

		time.Sleep(200 * time.Millisecond)
	}

	return ""
}
