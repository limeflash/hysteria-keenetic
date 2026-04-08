package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"hysteria-keenetic/internal/keenetic"
	"hysteria-keenetic/internal/logs"
	"hysteria-keenetic/internal/remnawave"
	"hysteria-keenetic/internal/runtime"
	"hysteria-keenetic/internal/state"
)

//go:embed ui/*
var uiFS embed.FS

type App struct {
	cfg        Config
	logger     *logs.Logger
	store      *state.Store
	remna      *remnawave.Client
	runtime    *runtime.Manager
	auth       *keenetic.AuthClient
	ndmc       *keenetic.NDMCClient
	httpServer *http.Server
	sessions   map[string]sessionInfo
	mu         sync.Mutex
	sessionMu  sync.Mutex
}

type subscriptionImportRequest struct {
	URL string `json:"url"`
}

type settingsPatchRequest struct {
	URL            *string `json:"url"`
	RegenerateHWID bool    `json:"regenerateHWID"`
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type sessionInfo struct {
	Username  string
	ExpiresAt time.Time
}

type logsResponse struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

type healthResponse struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

type settingsResponse struct {
	ListenAddress  string                   `json:"listenAddress"`
	HysteriaBinary string                   `json:"hysteriaBinary"`
	Subscription   state.SubscriptionSource `json:"subscription"`
	Runtime        state.RuntimeStatus      `json:"runtime"`
}

type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

func New(cfg Config) (*App, error) {
	logger, err := logs.New(cfg.ManagerLogPath)
	if err != nil {
		return nil, err
	}

	store, err := state.NewStore(cfg.StateFilePath, cfg.DefaultRefreshHours)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}

	app := &App{
		cfg:      cfg,
		logger:   logger,
		store:    store,
		auth:     keenetic.NewAuthClient(cfg.KeeneticBaseURL),
		ndmc:     keenetic.NewNDMCClient("ndmc"),
		sessions: make(map[string]sessionInfo),
	}

	app.remna = remnawave.NewClient(logger)
	app.runtime = runtime.NewManager(cfg.HysteriaBinaryPath, cfg.RuntimeConfigPath, cfg.HysteriaLogPath, logger, app.handleUnexpectedRuntimeExit)
	app.httpServer = &http.Server{
		Addr:    cfg.ListenAddress,
		Handler: app.routes(),
	}

	return app, app.ensureFilesystem()
}

func (a *App) Run() error {
	a.logger.Printf("starting hysteria-manager on %s", a.cfg.ListenAddress)
	go a.autoRefreshLoop()
	go a.restoreRuntimeIfNeeded()
	return a.httpServer.ListenAndServe()
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/session", a.handleSession)
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/settings", a.requireAuth(a.handleGetSettings))
	mux.HandleFunc("PATCH /api/settings", a.requireAuth(a.handlePatchSettings))
	mux.HandleFunc("POST /api/subscription/import", a.requireAuth(a.handleImportSubscription))
	mux.HandleFunc("POST /api/subscription/refresh", a.requireAuth(a.handleRefreshSubscription))
	mux.HandleFunc("GET /api/tunnels", a.requireAuth(a.handleListTunnels))
	mux.HandleFunc("POST /api/tunnels/", a.requireAuth(a.handleTunnelAction))
	mux.HandleFunc("GET /api/runtime/status", a.requireAuth(a.handleRuntimeStatus))
	mux.HandleFunc("GET /api/logs", a.requireAuth(a.handleLogs))
	mux.Handle("/", a.serveUI())
	return mux
}

func (a *App) serveUI() http.Handler {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status: "ok",
		Time:   time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *App) handleSession(w http.ResponseWriter, r *http.Request) {
	username, ok := a.sessionUsername(r)
	writeJSON(w, http.StatusOK, sessionResponse{
		Authenticated: ok,
		Username:      username,
	})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := a.auth.Authenticate(ctx, req.Login, req.Password); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	token := generateSessionToken()
	expiry := time.Now().UTC().Add(24 * time.Hour)

	a.sessionMu.Lock()
	a.sessions[token] = sessionInfo{Username: req.Login, ExpiresAt: expiry}
	a.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "hm_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiry,
	})

	writeJSON(w, http.StatusOK, sessionResponse{
		Authenticated: true,
		Username:      req.Login,
	})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("hm_session"); err == nil {
		a.sessionMu.Lock()
		delete(a.sessions, cookie.Value)
		a.sessionMu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "hm_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, sessionResponse{})
}

func (a *App) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	snapshot := a.store.Snapshot()
	writeJSON(w, http.StatusOK, settingsResponse{
		ListenAddress:  a.cfg.ListenAddress,
		HysteriaBinary: a.cfg.HysteriaBinaryPath,
		Subscription:   snapshot.Subscription,
		Runtime:        snapshot.Runtime,
	})
}

func (a *App) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	next, err := a.store.Update(func(st *state.AppState) error {
		if req.URL != nil {
			st.Subscription.URL = strings.TrimSpace(*req.URL)
		}
		if req.RegenerateHWID {
			st.Subscription.HWID = generateHWID()
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, settingsResponse{
		ListenAddress:  a.cfg.ListenAddress,
		HysteriaBinary: a.cfg.HysteriaBinaryPath,
		Subscription:   next.Subscription,
		Runtime:        next.Runtime,
	})
}

func (a *App) handleImportSubscription(w http.ResponseWriter, r *http.Request) {
	var req subscriptionImportRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	next, err := a.ImportSubscription(r.Context(), req.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, http.StatusOK, next)
}

func (a *App) handleRefreshSubscription(w http.ResponseWriter, r *http.Request) {
	next, err := a.RefreshSubscription(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, next)
}

func (a *App) handleListTunnels(w http.ResponseWriter, _ *http.Request) {
	snapshot := a.store.Snapshot()
	writeJSON(w, http.StatusOK, snapshot.Tunnels)
}

func (a *App) handleTunnelAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, errors.New("unknown tunnel action"))
		return
	}

	id, action := parts[0], parts[1]
	switch action {
	case "activate":
		next, err := a.ActivateTunnel(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, next)
	case "deactivate":
		next, err := a.DeactivateTunnel("api request")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, next)
	default:
		writeError(w, http.StatusNotFound, errors.New("unknown tunnel action"))
	}
}

func (a *App) handleRuntimeStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.store.Snapshot().Runtime)
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "manager"
	}

	path := a.cfg.ManagerLogPath
	switch source {
	case "manager":
	case "hysteria":
		path = a.cfg.HysteriaLogPath
	default:
		writeError(w, http.StatusBadRequest, errors.New("unknown log source"))
		return
	}

	content, err := logs.TailLines(path, 250)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, logsResponse{Source: source, Content: content})
}

func (a *App) ImportSubscription(ctx context.Context, rawURL string) (state.AppState, error) {
	url := strings.TrimSpace(rawURL)
	if url == "" {
		return state.AppState{}, errors.New("subscription url is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	current := a.store.Snapshot()
	if current.Subscription.HWID == "" {
		current.Subscription.HWID = generateHWID()
	}
	current.Subscription.URL = url

	if err := a.store.Replace(current); err != nil {
		return state.AppState{}, err
	}

	return a.refreshSubscriptionLocked(ctx)
}

func (a *App) RefreshSubscription(ctx context.Context) (state.AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshSubscriptionLocked(ctx)
}

func (a *App) refreshSubscriptionLocked(ctx context.Context) (state.AppState, error) {
	current := a.store.Snapshot()
	if strings.TrimSpace(current.Subscription.URL) == "" {
		return state.AppState{}, errors.New("subscription url is not configured")
	}
	if current.Subscription.HWID == "" {
		current.Subscription.HWID = generateHWID()
		if err := a.store.Replace(current); err != nil {
			return state.AppState{}, err
		}
	}

	result, err := a.remna.FetchSubscription(ctx, current.Subscription.URL, current.Subscription.HWID, current.Subscription.UserAgent, a.cfg.DefaultRefreshHours)
	if err != nil {
		updated, saveErr := a.store.Update(func(st *state.AppState) error {
			st.Subscription.LastError = err.Error()
			return nil
		})
		if saveErr != nil {
			return state.AppState{}, errors.Join(err, saveErr)
		}
		return updated, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	next, err := a.store.Update(func(st *state.AppState) error {
		st.Subscription.UserAgent = result.UserAgent
		st.Subscription.LastRefreshAt = now
		st.Subscription.LastError = ""
		st.Subscription.RefreshIntervalHours = result.RefreshIntervalHours
		st.Tunnels = mergeProfiles(st.Tunnels, result.Profiles, now)

		activeExists := false
		for _, tunnel := range st.Tunnels {
			if tunnel.ID == st.Runtime.ActiveTunnelID && !tunnel.Missing {
				activeExists = true
				break
			}
		}
		if st.Runtime.ActiveTunnelID != "" && !activeExists {
			st.Runtime.State = "stopped"
			st.Runtime.PID = 0
			st.Runtime.Connected = false
			st.Runtime.LastError = "active tunnel disappeared from subscription"
			st.Runtime.ActiveTunnelID = ""
			for i := range st.Tunnels {
				st.Tunnels[i].Active = false
			}
		}
		return nil
	})
	if err != nil {
		return state.AppState{}, err
	}

	if current.Runtime.ActiveTunnelID != "" && next.Runtime.ActiveTunnelID == "" {
		if _, stopErr := a.runtime.Deactivate("active tunnel disappeared from subscription"); stopErr != nil {
			a.logger.Printf("failed to stop runtime after missing tunnel: %v", stopErr)
		}
	}

	if err := a.syncProfileConfigs(next.Tunnels); err != nil {
		a.logger.Printf("failed to sync profile configs: %v", err)
	}
	if err := a.syncNDMSProfiles(ctx, next.Tunnels); err != nil {
		a.logger.Printf("failed to sync NDMS profiles: %v", err)
	}

	return next, nil
}

func (a *App) ActivateTunnel(ctx context.Context, tunnelID string) (state.AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	current := a.store.Snapshot()
	var selected *state.TunnelProfile
	for i := range current.Tunnels {
		if current.Tunnels[i].ID == tunnelID {
			selected = &current.Tunnels[i]
			break
		}
	}
	if selected == nil {
		return state.AppState{}, errors.New("tunnel not found")
	}
	if selected.Missing {
		return state.AppState{}, errors.New("cannot activate a tunnel that is missing from the latest subscription refresh")
	}

	prepareCtx, prepareCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := a.syncTunnelToNDMS(prepareCtx, *selected, false); err != nil {
		a.logger.Printf("failed to precreate NDMS tunnel %s: %v", selected.InterfaceName, err)
	} else if err := a.ndmc.Save(prepareCtx); err != nil {
		a.logger.Printf("failed to save NDMS config for %s: %v", selected.InterfaceName, err)
	}
	prepareCancel()

	status, err := a.runtime.Activate(ctx, runtime.Profile{
		Name:          selected.Name,
		InterfaceName: selected.InterfaceName,
		Server:        selected.Server,
		Port:          selected.Port,
		Auth:          selected.Auth,
		SNI:           selected.SNI,
		ALPN:          append([]string{}, selected.ALPN...),
	})
	if err != nil {
		updated, saveErr := a.store.Update(func(st *state.AppState) error {
			st.Runtime.State = "error"
			st.Runtime.ActiveTunnelID = ""
			st.Runtime.Connected = false
			st.Runtime.PID = 0
			st.Runtime.LastError = err.Error()
			for i := range st.Tunnels {
				st.Tunnels[i].Active = false
			}
			return nil
		})
		if saveErr != nil {
			return state.AppState{}, errors.Join(err, saveErr)
		}
		return updated, err
	}

	activateCtx, activateCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := a.syncTunnelToNDMS(activateCtx, *selected, true); err != nil {
		a.logger.Printf("failed to mark NDMS tunnel active for %s: %v", selected.InterfaceName, err)
	} else if err := a.ndmc.Save(activateCtx); err != nil {
		a.logger.Printf("failed to save active NDMS config for %s: %v", selected.InterfaceName, err)
	}
	activateCancel()

	next, err := a.store.Update(func(st *state.AppState) error {
		status.ActiveTunnelID = tunnelID
		status.LastError = ""
		st.Runtime = status
		for i := range st.Tunnels {
			st.Tunnels[i].Active = st.Tunnels[i].ID == tunnelID
		}
		return nil
	})
	if err != nil {
		return state.AppState{}, err
	}

	return next, nil
}

func (a *App) DeactivateTunnel(reason string) (state.AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	status, err := a.runtime.Deactivate(reason)
	if err != nil {
		return state.AppState{}, err
	}

	return a.store.Update(func(st *state.AppState) error {
		st.Runtime = status
		st.Runtime.LastError = ""
		st.Runtime.ActiveTunnelID = ""
		st.Runtime.Connected = false
		st.Runtime.PID = 0
		for i := range st.Tunnels {
			st.Tunnels[i].Active = false
		}
		return nil
	})
}

func (a *App) autoRefreshLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		snapshot := a.store.Snapshot()
		if snapshot.Subscription.URL == "" || snapshot.Subscription.RefreshIntervalHours <= 0 {
			continue
		}
		if !refreshDue(snapshot.Subscription.LastRefreshAt, snapshot.Subscription.RefreshIntervalHours) {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := a.RefreshSubscription(ctx)
		cancel()
		if err != nil {
			a.logger.Printf("auto refresh failed: %v", err)
			continue
		}
		a.logger.Printf("subscription refreshed automatically")
	}
}

func (a *App) restoreRuntimeIfNeeded() {
	time.Sleep(500 * time.Millisecond)
	snapshot := a.store.Snapshot()
	if snapshot.Runtime.ActiveTunnelID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := a.ActivateTunnel(ctx, snapshot.Runtime.ActiveTunnelID); err != nil {
		a.logger.Printf("failed to restore active tunnel: %v", err)
	}
}

func (a *App) handleUnexpectedRuntimeExit(err error) {
	a.logger.Printf("runtime exited unexpectedly: %v", err)
	_, updateErr := a.store.Update(func(st *state.AppState) error {
		st.Runtime.State = "error"
		st.Runtime.Connected = false
		st.Runtime.PID = 0
		st.Runtime.LastError = err.Error()
		st.Runtime.ActiveTunnelID = ""
		for i := range st.Tunnels {
			st.Tunnels[i].Active = false
		}
		return nil
	})
	if updateErr != nil {
		a.logger.Printf("failed to persist runtime exit state: %v", updateErr)
	}
}

func (a *App) ensureFilesystem() error {
	for _, path := range []string{a.cfg.BaseDir, a.cfg.ProfilesDir, a.cfg.LogDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) syncProfileConfigs(tunnels []state.TunnelProfile) error {
	if err := os.MkdirAll(a.cfg.ProfilesDir, 0o755); err != nil {
		return err
	}

	for _, tunnel := range tunnels {
		if tunnel.Missing {
			continue
		}

		content := runtime.BuildClientConfig(runtime.Profile{
			Name:          tunnel.Name,
			InterfaceName: tunnel.InterfaceName,
			Server:        tunnel.Server,
			Port:          tunnel.Port,
			Auth:          tunnel.Auth,
			SNI:           tunnel.SNI,
			ALPN:          append([]string{}, tunnel.ALPN...),
		})

		path := filepath.Join(a.cfg.ProfilesDir, tunnel.ID+".yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}

	return nil
}

func (a *App) syncNDMSProfiles(ctx context.Context, tunnels []state.TunnelProfile) error {
	var errs []error
	synced := false
	for _, tunnel := range tunnels {
		if tunnel.Missing {
			continue
		}
		if err := a.syncTunnelToNDMS(ctx, tunnel, tunnel.Active); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", tunnel.InterfaceName, err))
			continue
		}
		synced = true
	}
	if synced {
		if err := a.ndmc.Save(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *App) syncTunnelToNDMS(ctx context.Context, tunnel state.TunnelProfile, enabled bool) error {
	tun := runtime.DefaultTunSettings(tunnel.InterfaceName)
	return a.ndmc.SyncOpkgTun(ctx, keenetic.OpkgTunConfig{
		InterfaceName: tunnel.InterfaceName,
		Description:   tunnel.Name,
		IPv4CIDR:      tun.IPv4CIDR,
		IPv6CIDR:      tun.IPv6CIDR,
		MTU:           tun.MTU,
		Enabled:       enabled,
	})
}

func mergeProfiles(existing []state.TunnelProfile, fresh []remnawave.Profile, now string) []state.TunnelProfile {
	byID := make(map[string]state.TunnelProfile, len(existing))
	usedInterfaces := make(map[string]struct{}, len(existing))
	for _, tunnel := range existing {
		byID[tunnel.ID] = tunnel
		if tunnel.InterfaceName != "" {
			usedInterfaces[tunnel.InterfaceName] = struct{}{}
		}
	}

	seen := make(map[string]struct{}, len(fresh))
	merged := make([]state.TunnelProfile, 0, len(fresh))

	for _, incoming := range fresh {
		id := stableTunnelID(incoming)
		seen[id] = struct{}{}

		tunnel := byID[id]
		tunnel.ID = id
		tunnel.Name = incoming.Name
		if tunnel.InterfaceName == "" {
			tunnel.InterfaceName = nextInterfaceName(usedInterfaces)
		}
		usedInterfaces[tunnel.InterfaceName] = struct{}{}
		tunnel.Server = incoming.Server
		tunnel.Port = incoming.Port
		tunnel.Auth = incoming.Auth
		tunnel.AuthMasked = remnawave.MaskSecret(incoming.Auth)
		tunnel.SNI = incoming.SNI
		tunnel.ALPN = append([]string{}, incoming.ALPN...)
		tunnel.IsWarp = incoming.IsWarp
		tunnel.Missing = false
		tunnel.LastSeenInSubscription = now
		tunnel.LastUpdatedAt = now
		merged = append(merged, tunnel)
	}

	for _, tunnel := range existing {
		if _, ok := seen[tunnel.ID]; ok {
			continue
		}
		tunnel.Missing = true
		tunnel.Active = false
		tunnel.LastUpdatedAt = now
		merged = append(merged, tunnel)
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Missing != merged[j].Missing {
			return !merged[i].Missing
		}
		if merged[i].IsWarp != merged[j].IsWarp {
			return !merged[i].IsWarp
		}
		return strings.ToLower(merged[i].Name) < strings.ToLower(merged[j].Name)
	})

	return merged
}

func nextInterfaceName(used map[string]struct{}) string {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if strings.HasPrefix(strings.ToLower(iface.Name), "opkgtun") {
				used[iface.Name] = struct{}{}
			}
		}
	}

	for idx := 0; idx < 256; idx++ {
		name := fmt.Sprintf("opkgtun%d", idx)
		if _, exists := used[name]; !exists {
			return name
		}
	}
	return fmt.Sprintf("opkgtun-%d", time.Now().Unix())
}

func stableTunnelID(profile remnawave.Profile) string {
	key := fmt.Sprintf("%s|%d|%s|%s", profile.Server, profile.Port, profile.Auth, profile.Name)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func refreshDue(lastRefreshAt string, everyHours int) bool {
	if everyHours <= 0 {
		return false
	}
	if lastRefreshAt == "" {
		return true
	}
	timestamp, err := time.Parse(time.RFC3339, lastRefreshAt)
	if err != nil {
		return true
	}
	return time.Since(timestamp) >= time.Duration(everyHours)*time.Hour
}

func generateHWID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("keenetic-%d", time.Now().UnixNano())
	}
	return "keenetic-" + hex.EncodeToString(buffer)
}

func generateSessionToken() string {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionUsername(r); !ok {
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		next(w, r)
	}
}

func (a *App) sessionUsername(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("hm_session")
	if err != nil || cookie.Value == "" {
		return "", false
	}

	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()

	session, ok := a.sessions[cookie.Value]
	if !ok {
		return "", false
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		delete(a.sessions, cookie.Value)
		return "", false
	}
	return session.Username, true
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
