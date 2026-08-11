package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/logger"
	"cursor/internal/runtimehost"
)

const readyMarker = "CC_SWITCH_SIDECAR_READY "

type options struct {
	listenAddr string
	authToken  string
	dataDir    string
	parentPID  int
}

type controlServer struct {
	host      *runtimehost.Host
	authToken string
	shutdown  func()
}

type errorResponse struct {
	Error string `json:"error"`
}

func main() {
	if handled, err := runInstallCA(os.Args[1:]); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	options, err := parseOptions()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := os.Setenv("CC_SWITCH_CURSOR_DATA_DIR", options.dataDir); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger.Init()
	host, err := runtimehost.New()
	if err != nil {
		logger.Errorf("initialize sidecar: %v", err)
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", options.listenAddr)
	if err != nil {
		logger.Errorf("listen control API: %v", err)
		os.Exit(1)
	}
	if !isLoopbackListener(listener.Addr()) {
		_ = listener.Close()
		logger.Errorf("control API must listen on loopback, got %s", listener.Addr())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var shutdownOnce sync.Once
	httpServer := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	shutdown := func() {
		shutdownOnce.Do(func() {
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer stopCancel()
			if _, err := host.Stop(stopCtx); err != nil {
				logger.Errorf("stop runtime host: %v", err)
			}
			_ = httpServer.Shutdown(stopCtx)
		})
	}
	control := &controlServer{host: host, authToken: options.authToken, shutdown: shutdown}
	httpServer.Handler = control.routes()

	ready, _ := json.Marshal(map[string]any{
		"address": listener.Addr().String(),
		"pid":     os.Getpid(),
		"version": 1,
	})
	fmt.Printf("%s%s\n", readyMarker, ready)

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
			shutdown()
		case <-ctx.Done():
		}
	}()
	if options.parentPID > 0 {
		go monitorParent(ctx, options.parentPID, shutdown)
	}

	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Errorf("control API stopped unexpectedly: %v", err)
		shutdown()
		os.Exit(1)
	}
	shutdown()
}

func parseOptions() (options, error) {
	var parsed options
	flag.StringVar(&parsed.listenAddr, "listen", "127.0.0.1:0", "loopback control API address")
	flag.StringVar(&parsed.authToken, "auth-token", "", "control API bearer token")
	flag.StringVar(&parsed.dataDir, "data-dir", "", "CC Switch managed data directory")
	flag.IntVar(&parsed.parentPID, "parent-pid", 0, "parent process to monitor")
	flag.Parse()
	parsed.authToken = strings.TrimSpace(parsed.authToken)
	parsed.dataDir = strings.TrimSpace(parsed.dataDir)
	if len(parsed.authToken) < 32 {
		return options{}, errors.New("--auth-token must contain at least 32 characters")
	}
	if parsed.dataDir == "" {
		return options{}, errors.New("--data-dir is required")
	}
	return parsed, nil
}

func (server *controlServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", server.authorized(server.health))
	mux.HandleFunc("GET /v1/state", server.authorized(server.state))
	mux.HandleFunc("GET /v1/config", server.authorized(server.loadConfig))
	mux.HandleFunc("PUT /v1/config", server.authorized(server.saveConfig))
	mux.HandleFunc("POST /v1/start", server.authorized(server.start))
	mux.HandleFunc("POST /v1/stop", server.authorized(server.stop))
	mux.HandleFunc("POST /v1/ca/install", server.authorized(server.installCA))
	mux.HandleFunc("POST /v1/ca/remove", server.authorized(server.removeCA))
	mux.HandleFunc("POST /v1/test-model", server.authorized(server.testModel))
	mux.HandleFunc("GET /v1/usage-events", server.authorized(server.usageEvents))
	mux.HandleFunc("POST /v1/shutdown", server.authorized(server.shutdownHandler))
	return mux
}

func (server *controlServer) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		expected := "Bearer " + server.authToken
		provided := request.Header.Get("Authorization")
		if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			writeJSON(writer, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
			return
		}
		next(writer, request)
	}
}

func (server *controlServer) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func (server *controlServer) state(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, server.host.State())
}

func (server *controlServer) loadConfig(writer http.ResponseWriter, request *http.Request) {
	config, err := server.host.LoadConfig(request.Context())
	writeResult(writer, config, err)
}

func (server *controlServer) saveConfig(writer http.ResponseWriter, request *http.Request) {
	var config serverconfig.Config
	if err := decodeJSON(request, &config); err != nil {
		writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	normalized, err := server.host.SaveConfig(request.Context(), config)
	writeResult(writer, normalized, err)
}

func (server *controlServer) start(writer http.ResponseWriter, request *http.Request) {
	state, err := server.host.Start(request.Context())
	writeResult(writer, state, err)
}

func (server *controlServer) stop(writer http.ResponseWriter, request *http.Request) {
	state, err := server.host.Stop(request.Context())
	writeResult(writer, state, err)
}

func (server *controlServer) installCA(writer http.ResponseWriter, _ *http.Request) {
	state, err := server.host.EnsureCAInstalled()
	writeResult(writer, state, err)
}

func (server *controlServer) removeCA(writer http.ResponseWriter, _ *http.Request) {
	state, err := server.host.RemoveCA()
	writeResult(writer, state, err)
}

func (server *controlServer) testModel(writer http.ResponseWriter, request *http.Request) {
	var adapter serverconfig.ModelAdapterConfig
	if err := decodeJSON(request, &adapter); err != nil {
		writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	result, err := server.host.TestModel(request.Context(), adapter)
	writeResult(writer, result, err)
}

func (server *controlServer) usageEvents(writer http.ResponseWriter, request *http.Request) {
	cursor, err := strconv.ParseInt(request.URL.Query().Get("cursor"), 10, 64)
	if err != nil && request.URL.Query().Get("cursor") != "" {
		writeJSON(writer, http.StatusBadRequest, errorResponse{Error: "cursor must be an integer"})
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	page, err := server.host.UsageEvents(cursor, limit)
	writeResult(writer, page, err)
}

func (server *controlServer) shutdownHandler(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusAccepted, map[string]any{"accepted": true})
	go server.shutdown()
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func writeResult(writer http.ResponseWriter, value any, err error) {
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func monitorParent(ctx context.Context, pid int, shutdown func()) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !runtimehost.ParentProcessAlive(pid) {
				shutdown()
				return
			}
		}
	}
}

func isLoopbackListener(address net.Addr) bool {
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
