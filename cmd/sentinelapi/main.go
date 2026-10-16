// Command sentinelapi runs the SentinelAPI HTTP service.
//
// Milestone M0: this is a runnable skeleton. It serves health endpoints only
// and is not yet a security gateway.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/MuhammadBilal561/sentinelapi/internal/api"
	"github.com/MuhammadBilal561/sentinelapi/internal/config"
)

const (
	exitOK      = 0
	exitRuntime = 1
	exitConfig  = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.LookupEnv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run starts the service and blocks until ctx is cancelled or the server
// fails. It returns the process exit code. Logs go to stdout; configuration
// errors, which occur before logging is set up, go to stderr.
func run(ctx context.Context, lookup config.LookupFunc, stdout, stderr io.Writer) int {
	cfg, err := config.Load(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "sentinelapi: invalid configuration:\n%v\n", err)
		return exitConfig
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	health := api.NewHealth(logger)
	srv := &http.Server{
		Handler:           api.NewHandler(api.Options{Logger: logger, Health: health}),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	// Bind first so an unusable address fails clearly before we report ready.
	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		logger.Error("cannot listen", "addr", cfg.HTTPAddr, "error", err.Error())
		return exitRuntime
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	health.SetReady(true)
	logger.Info("server listening", "addr", ln.Addr().String(), "log_level", cfg.LogLevel.String())

	select {
	case err := <-serveErr:
		// Serve only returns on its own when something went wrong.
		logger.Error("server stopped unexpectedly", "error", err.Error())
		return exitRuntime
	case <-ctx.Done():
	}

	logger.Info("shutdown signal received", "timeout", cfg.ShutdownTimeout.String())
	health.SetReady(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed, closing connections", "error", err.Error())
		_ = srv.Close()
		return exitRuntime
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server error during shutdown", "error", err.Error())
		return exitRuntime
	}
	logger.Info("shutdown complete")
	return exitOK
}
