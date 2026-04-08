package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func (m *Manager) Activate(_ context.Context, profile Profile, subscriptionURL string) (state.RuntimeStatus, error) {
	if err := EnsureBinary(m.binaryPath); err != nil {
		return state.RuntimeStatus{}, fmt.Errorf("hysteria binary is unavailable: %w", err)
	}

	routePlan, err := BuildRoutePlan(subscriptionURL, profile.Server)
	if err != nil {
		return state.RuntimeStatus{}, fmt.Errorf("failed to build route plan: %w", err)
	}

	configContent := BuildClientConfig(profile, routePlan)
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0o755); err != nil {
		return state.RuntimeStatus{}, err
	}
	if err := os.WriteFile(m.configPath, []byte(configContent), 0o600); err != nil {
		return state.RuntimeStatus{}, err
	}

	if _, err := m.Deactivate("switching tunnel"); err != nil {
		return state.RuntimeStatus{}, err
	}

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

	select {
	case <-handle.done:
		m.logger.Printf("hysteria process exited during startup: %v", handle.waitErr)
		return state.RuntimeStatus{}, fmt.Errorf("hysteria exited during startup: %w", handle.waitErr)
	case <-time.After(2 * time.Second):
	}

	m.mu.Lock()
	m.current = handle
	m.mu.Unlock()

	go m.watch(handle)

	return state.RuntimeStatus{
		State:         "running",
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
		State:     "running",
		PID:       m.current.cmd.Process.Pid,
		Connected: true,
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
