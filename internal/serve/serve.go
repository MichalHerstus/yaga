// Package serve implements the `yaga wedit` web-based YAML config editor
// (E4). It starts a local HTTP server exposing a small JSON REST API over the
// same Go logic the TUI editor uses (parser.ValidateAll,
// schema.CollectReferences) plus an embedded
// vanilla-JS single-page app. The command name is `wedit` (not the E4-drafted
// `serve`) so the web version of the editor is clearly distinguishable from a
// running generated dashboard.
package serve

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/MichalHerstus/yaga/internal/mcp"
	"github.com/MichalHerstus/yaga/internal/types"
)

//go:embed static/*
var staticFS embed.FS

// DefaultPort is the port wedit binds when --port is not given.
const DefaultPort = 9090

// Server owns the in-memory config being edited, the disk path it maps to,
// and the HTTP routes of the editor API. It follows the same pattern as the
// TUI editor's `Editor`: an in-memory *types.Config written back to disk on
// save.
type Server struct {
	mu         sync.RWMutex
	cfg        *types.Config
	configPath string
	rev        uint64 // bumped on every in-memory config replacement
	subs       map[chan uint64]struct{}

	port int
	open bool // open the browser automatically after binding

	mcp    mcpHandler
	mux    *http.ServeMux
	stubDB *sql.DB // in-memory sqlite stub for E6 debug dry-runs
}

// Options configures a wedit server.
type Options struct {
	Port        int  // listen port (0 -> DefaultPort)
	OpenBrowser bool // run `open`/`xdg-open` after the port is bound
}

// New builds a wedit server around a parsed config.
func New(cfg *types.Config, configPath string, opts Options) *Server {
	if opts.Port <= 0 {
		opts.Port = DefaultPort
	}
	s := &Server{
		cfg:        cfg,
		configPath: configPath,
		port:       opts.Port,
		open:       opts.OpenBrowser,
		mux:        http.NewServeMux(),
	}
	s.mcp = mcp.New(serverMCPState{s: s})
	s.routes()
	return s
}

// Handler returns the fully wired http.Handler (API + static SPA).
func (s *Server) Handler() http.Handler {
	return s.mux
}

// replaceConfig swaps the in-memory config, bumps the revision counter and
// notifies every SSE subscriber (non-blocking, coalesced to the latest rev).
// Every path that replaces the config must go through here so the SPA's
// auto-refresh and stale-write guard see the change.
func (s *Server) replaceConfig(cfg *types.Config) uint64 {
	s.mu.Lock()
	s.cfg = cfg
	s.rev++
	for ch := range s.subs {
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- s.rev:
		default:
		}
	}
	s.mu.Unlock()
	return s.rev
}

// currentRev returns the latest config revision.
func (s *Server) currentRev() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rev
}

// subscribe registers a channel that receives the config revision on every
// change (coalesced). The returned unsub func must be called when the
// subscriber goes away.
func (s *Server) subscribe() (chan uint64, func()) {
	ch := make(chan uint64, 1)
	s.mu.Lock()
	if s.subs == nil {
		s.subs = make(map[chan uint64]struct{})
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// handleEvents serves a Server-Sent Events stream (`event: rev` / `data: N`)
// that fires whenever the in-memory config is replaced, so the SPA can refresh
// without polling. An initial event carries the current rev so a client that
// subscribes late syncs immediately.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fl.Flush()
	ch, unsub := s.subscribe()
	defer unsub()
	writeRev := func(rev uint64) {
		fmt.Fprintf(w, "event: rev\ndata: %d\n\n", rev)
		fl.Flush()
	}
	writeRev(s.currentRev())
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case rev := <-ch:
			writeRev(rev)
		case <-ticker.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		}
	}
}

// routes registers the REST API and the embedded SPA.
func (s *Server) routes() {
	mux := s.mux
	mux.HandleFunc("GET /api/config", s.handleConfigGet)
	mux.HandleFunc("PUT /api/config", s.handleConfigPut)
	mux.HandleFunc("POST /api/save", s.handleSave)
	mux.HandleFunc("GET /api/validate", s.handleValidate)
	mux.HandleFunc("POST /api/fix", s.handleFix)
	mux.HandleFunc("POST /mcp", s.handleMCPPost)
	mux.HandleFunc("GET /mcp", s.handleMCPGet)
	mux.HandleFunc("GET /api/raw", s.handleRawGet)
	mux.HandleFunc("PUT /api/raw", s.handleRawPut)
	mux.HandleFunc("GET /api/rev", s.handleRev)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("POST /api/lua-check", s.handleLuaCheck)
	mux.HandleFunc("POST /api/lua-run", s.handleLuaRun)
	mux.HandleFunc("POST /api/sql-run", s.handleSQLRun)
	mux.HandleFunc("POST /api/sample-refresh", s.handleSampleRefresh)

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed is static; cannot fail
	}
	mux.HandleFunc("GET /preview", s.handlePreview)
	mux.HandleFunc("GET /preview/styles.css", s.handlePreviewStyles)
	mux.HandleFunc("GET /preview/chart.js", s.handlePreviewChart)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		http.FileServer(http.FS(sub)).ServeHTTP(w, r)
	})))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		s.serveIndex(w)
	})
}

// serveIndex renders the SPA shell.
func (s *Server) serveIndex(w http.ResponseWriter) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// Start binds the port, prints the URL (and opens the browser when --open was
// given), then serves until SIGINT/SIGTERM, which triggers a graceful
// shutdown. It never returns a bind error after the port is in use by
// someone else.
func (s *Server) Start() error {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("wedit: cannot listen on :%d: %w", s.port, err)
	}
	url := fmt.Sprintf("http://localhost:%d/", s.port)
	fmt.Printf("WEdit: web config editor for %s\n", s.configPath)
	fmt.Printf("  open: %s\n", url)
	fmt.Printf("  Ctrl+C to stop.\n")
	if s.open {
		openBrowser(url)
	}

	srv := &http.Server{Handler: s.mux}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A second Ctrl+C forces an immediate exit.
	force := make(chan os.Signal, 1)
	signal.Notify(force, os.Interrupt)
	go func() {
		<-ctx.Done()
		<-force // wait for second SIGINT
		fmt.Println("\nWEdit: forced shutdown.")
		os.Exit(1)
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(l) }()

	select {
	case <-ctx.Done():
		fmt.Println("\nWEdit: shutting down gracefully…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// openBrowser best-effort opens url in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}
