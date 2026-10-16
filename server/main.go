package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anggasct/chartdb/server/api"
	"github.com/anggasct/chartdb/server/auth"
	"github.com/anggasct/chartdb/server/config"
	"github.com/anggasct/chartdb/server/store"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "apply pending migrations (creating the database when missing) and exit")
	flag.Parse()

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		slog.Error("CHARTDB_DATABASE_URL is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if *migrateOnly {
		if err := store.EnsureDatabase(ctx, cfg.DatabaseURL); err != nil {
			slog.Error("create database failed", "err", err)
			os.Exit(1)
		}
	}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("open database failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	if *migrateOnly {
		var applied int
		if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migration`).Scan(&applied); err != nil {
			slog.Error("migration status failed", "err", err)
			os.Exit(1)
		}
		slog.Info("migrations applied", "count", applied)
		return
	}

	srv := api.New(cfg, st)
	if cfg.OIDC != nil {
		provider, err := auth.NewGOIDCProvider(context.Background(), *cfg.OIDC)
		if err != nil {
			slog.Error("configure login provider failed", "err", err)
			os.Exit(1)
		}
		srv.AddProvider(cfg.OIDC.ID, provider)
	}

	handler := srv.Handler()
	if cfg.StaticDir != "" {
		handler = withStatic(handler, cfg.StaticDir)
	}

	httpServer := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("listening", "addr", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve failed", "err", err)
		os.Exit(1)
	}
}

func withStatic(next http.Handler, dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		candidate := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			http.ServeFile(w, r, candidate)
			return
		}
		if index := filepath.Join(dir, "index.html"); fileExists(index) {
			http.ServeFile(w, r, index)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
