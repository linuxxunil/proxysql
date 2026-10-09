package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"proxysql/internal/config"
	"proxysql/internal/dbpool"
	"proxysql/internal/server"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: levelOf(cfg.Log.Level),
	})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := dbpool.New(ctx, cfg)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	srv := server.New(cfg, pool)
	httpSrv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      srv.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout.Duration,
		WriteTimeout: cfg.Server.WriteTimeout.Duration,
	}

	go func() {
		slog.Info("proxysql listening", "addr", cfg.Server.Listen, "base_path", cfg.Server.BasePath)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http serve", "err", err)
			os.Exit(1)
		}
	}()

	// Effective config (single source: config.yaml, except DSN which may come
	// from PROXYSQL_DB__DSN). Password is never logged.
	slog.Info("effective config",
		"db_dsn", config.RedactedDSN(cfg.DB.DSN),
		"base_path", cfg.Server.BasePath,
		"default_timeout", cfg.Query.DefaultTimeout.Duration.String(),
		"max_timeout", cfg.Query.MaxTimeout.Duration.String(),
		"allow_raw_sql", cfg.Query.AllowRawSQL,
		"allowed_statements", strings.Join(cfg.Query.AllowedStatements, ","),
	)

	<-ctx.Done()
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shCtx)
	slog.Info("proxysql stopped")
}

func levelOf(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
