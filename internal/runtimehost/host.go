package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cursor/internal/appdata"
	backend "cursor/internal/backend"
	"cursor/internal/backend/forwarder"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/certs"
	cursorhost "cursor/internal/cursor"
	"cursor/internal/mitm"
	"cursor/internal/netproxy"
	legacyruntime "cursor/internal/runtime"
)

const readyTimeout = 15 * time.Second

type State struct {
	BackendListenAddr     string `json:"backendListenAddr"`
	BackendRunning        bool   `json:"backendRunning"`
	ProxyListenAddr       string `json:"proxyListenAddr"`
	ProxyRunning          bool   `json:"proxyRunning"`
	CursorSettingsApplied bool   `json:"cursorSettingsApplied"`
	CAInstalled           bool   `json:"caInstalled"`
	CAFingerprint         string `json:"caFingerprint"`
	LastError             string `json:"lastError"`
}

type Host struct {
	mu              sync.Mutex
	store           *serverconfig.Store
	backend         *backend.Host
	usageStore      *forwarder.UsageFileStore
	proxy           *mitm.ProxyServer
	certManager     *certs.Manager
	caCertPEM       []byte
	caFingerprint   string
	settingsApplied bool
	caInstalled     bool
	hostStatePath   string
	lastError       string
}

func New() (*Host, error) {
	if err := appdata.EnsureAssistantHome(); err != nil {
		return nil, err
	}
	netproxy.InstallDefaultTransport()
	certPath := appdata.CACertFilePath()
	keyPath := filepath.Join(appdata.DataRootPath(), "ca.key")
	certManager, certPEM, err := certs.LoadOrCreateManager(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	_, fingerprint, statusErr := cursorhost.CACertStatus(certPEM)
	if fingerprint == "" {
		return nil, statusErr
	}
	store := serverconfig.NewStore(appdata.ConfigFilePath(), appdata.LogsRootPath())
	backendHost, err := backend.NewHost(store)
	if err != nil {
		return nil, err
	}
	return &Host{
		store:         store,
		backend:       backendHost,
		usageStore:    forwarder.NewUsageFileStore(appdata.HistoryRootPath()),
		certManager:   certManager,
		caCertPEM:     certPEM,
		caFingerprint: fingerprint,
		hostStatePath: filepath.Join(appdata.DataRootPath(), "host-state.json"),
	}, nil
}

func (host *Host) LoadConfig(ctx context.Context) (serverconfig.Config, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.backend.LoadConfig(ctx)
}

func (host *Host) SaveConfig(ctx context.Context, cfg serverconfig.Config) (serverconfig.Config, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	current, err := host.backend.LoadConfig(ctx)
	if err != nil {
		return serverconfig.Config{}, err
	}
	if host.backend.IsRunning() && (current.BackendListenAddr != cfg.BackendListenAddr || current.ProxyListenAddr != cfg.ProxyListenAddr) {
		return serverconfig.Config{}, errors.New("cannot change backend or proxy listen address while the service is running")
	}
	return host.backend.SaveConfig(ctx, cfg)
}

func (host *Host) Start(ctx context.Context) (State, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.proxy != nil && host.proxy.IsRunning() {
		return host.stateLocked(), nil
	}
	cfg, err := host.backend.LoadConfig(ctx)
	if err != nil {
		return host.failLocked(err)
	}
	if len(cfg.ModelAdapters) == 0 {
		return host.failLocked(errors.New("at least one enabled model is required"))
	}
	if !host.backend.IsRunning() {
		if err := host.backend.Start(); err != nil {
			return host.failLocked(err)
		}
	}
	readyCtx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	if err := host.waitForBackend(readyCtx); err != nil {
		_ = host.backend.Stop(context.Background())
		return host.failLocked(err)
	}
	proxyServer, err := mitm.NewProxyServer(cfg.ProxyListenAddr, host.backend.BaseURL(), "", "", host.certManager)
	if err != nil {
		_ = host.backend.Stop(context.Background())
		return host.failLocked(err)
	}
	if err := proxyServer.Start(); err != nil {
		_ = host.backend.Stop(context.Background())
		return host.failLocked(err)
	}
	host.proxy = proxyServer
	if err := host.applyCursorSettings(); err != nil {
		_ = host.stopLocked(context.Background())
		return host.failLocked(err)
	}
	host.lastError = ""
	return host.stateLocked(), nil
}

func (host *Host) Stop(ctx context.Context) (State, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if err := host.stopLocked(ctx); err != nil {
		return host.failLocked(err)
	}
	host.lastError = ""
	return host.stateLocked(), nil
}

func (host *Host) UsageEvents(cursor int64, limit int) (forwarder.UsageEventPage, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.usageStore.EventsAfter(cursor, limit)
}

func (host *Host) State() State {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.stateLocked()
}

func (host *Host) EnsureCAInstalled() (State, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	certPath, err := cursorhost.EnsureCACertFile(host.caCertPEM, appdata.CACertFilePath())
	if err != nil {
		return host.failLocked(err)
	}
	if err := cursorhost.EnsureCACertInstalled(host.caCertPEM, certPath); err != nil {
		return host.failLocked(err)
	}
	host.caInstalled = true
	host.lastError = ""
	return host.stateLocked(), nil
}

func (host *Host) RemoveCA() (State, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.settingsApplied || (host.proxy != nil && host.proxy.IsRunning()) {
		return host.failLocked(errors.New("stop the Cursor runtime before removing its CA"))
	}
	if err := cursorhost.RemoveCACertInstalled(host.caCertPEM, appdata.CACertFilePath()); err != nil {
		return host.failLocked(err)
	}
	host.caInstalled = false
	host.lastError = ""
	return host.stateLocked(), nil
}

func (host *Host) stopLocked(ctx context.Context) error {
	var result error
	if host.proxy != nil && host.proxy.IsRunning() {
		result = errors.Join(result, host.proxy.Stop(ctx))
	}
	if host.settingsApplied {
		result = errors.Join(result, cursorhost.RestoreHostTakeover(host.hostStatePath))
		if result == nil {
			host.settingsApplied = false
		}
	} else {
		result = errors.Join(result, cursorhost.RestoreHostTakeover(host.hostStatePath))
	}
	if host.backend != nil && host.backend.IsRunning() {
		result = errors.Join(result, host.backend.Stop(ctx))
	}
	return result
}

func (host *Host) applyCursorSettings() error {
	if host.proxy == nil {
		return errors.New("proxy is not initialized")
	}
	certPath, err := cursorhost.EnsureCACertFile(host.caCertPEM, appdata.CACertFilePath())
	if err != nil {
		return err
	}
	if err := cursorhost.EnsureCACertInstalled(host.caCertPEM, certPath); err != nil {
		return err
	}
	host.caInstalled = true
	proxyURL := cursorhost.ProxyURLFromListenAddr(host.proxy.Snapshot().ListenAddr)
	if err := cursorhost.ApplyHostTakeover(
		host.hostStatePath,
		proxyURL,
		certPath,
		legacyruntime.InjectAccountEmail,
		legacyruntime.InjectAuthToken,
	); err != nil {
		return err
	}
	host.settingsApplied = true
	return nil
}

func (host *Host) waitForBackend(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := host.backend.HealthCheck(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("backend did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (host *Host) stateLocked() State {
	caInstalled, _, caError := cursorhost.CACertStatus(host.caCertPEM)
	if caError == nil {
		host.caInstalled = caInstalled
	}
	state := State{
		CursorSettingsApplied: host.settingsApplied,
		CAInstalled:           host.caInstalled,
		CAFingerprint:         host.caFingerprint,
		LastError:             host.lastError,
	}
	if host.backend != nil {
		state.BackendListenAddr = host.backend.ListenAddr()
		state.BackendRunning = host.backend.IsRunning()
	}
	if host.proxy != nil {
		snapshot := host.proxy.Snapshot()
		state.ProxyListenAddr = snapshot.ListenAddr
		state.ProxyRunning = snapshot.Running
	}
	return state
}

func (host *Host) failLocked(err error) (State, error) {
	if err == nil {
		return host.stateLocked(), nil
	}
	host.lastError = strings.TrimSpace(err.Error())
	return host.stateLocked(), err
}
