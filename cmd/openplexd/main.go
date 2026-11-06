// Command openplexd is the OpenPlex control-plane daemon: API server,
// embedded dashboard, Docker orchestrator and telemetry sampler in one binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"openplex.dev/openplex/internal/api"
	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/backup"
	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/monitor"
	"openplex.dev/openplex/internal/secrets"
	"openplex.dev/openplex/internal/ui"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	var err error
	switch {
	case len(args) > 0 && args[0] == "version":
		fmt.Println(version)
		return
	case len(args) > 0 && args[0] == "reset-password":
		err = resetPassword(args[1:])
	default:
		err = run(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "openplexd:", err)
		os.Exit(1)
	}
}

func newLogger(dev bool) *slog.Logger {
	if dev {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Dev)
	slog.SetDefault(log)
	log.Info("starting openplexd", "version", version, "data_dir", cfg.DataDir, "listen", cfg.Listen)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer store.Close()
	if err := bootstrapAdmin(ctx, cfg, store, log); err != nil {
		return err
	}

	mon := monitor.New(2*time.Second, 300, cfg.DataDir, log)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); mon.Run(ctx) }()

	box, err := secrets.New(cfg.Secret, "database-passwords")
	if err != nil {
		return err
	}

	var orch *docker.Orchestrator
	if cfg.EnableDocker {
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		orch, err = docker.New(pingCtx, cfg, store, log)
		cancel()
		if err != nil {
			log.Error("Docker orchestrator disabled", "err", err)
			orch = nil
		} else {
			defer orch.Close()
			workers.Add(1)
			go func() {
				defer workers.Done()
				orch.RemoveStaleShells(ctx)
				if err := orch.Reconcile(ctx); err != nil {
					log.Error("initial reconcile finished with errors", "err", err)
				}
				if err := orch.ReconcileDatabases(ctx, box); err != nil {
					log.Error("database reconcile finished with errors", "err", err)
				}
			}()
		}
	}

	workers.Add(1)
	go func() {
		defer workers.Done()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := store.PruneRefreshTokens(ctx); err == nil && n > 0 {
					log.Info("pruned expired refresh tokens", "count", n)
				}
			}
		}
	}()

	var backups *backup.Manager
	if orch != nil {
		backups = backup.New(cfg, store, orch, box, log)
		workers.Add(1)
		go func() { defer workers.Done(); backups.Run(ctx) }()
	}

	srv := api.New(api.Options{
		Config: cfg, DB: store, Orchestrator: orch, Monitor: mon, Logger: log,
		UI: ui.Handler(), Version: version, BaseContext: ctx, Box: box, Backups: backups,
	})
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	var listeners []net.Listener
	tcp, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	listeners = append(listeners, tcp)
	_ = os.Remove(cfg.PanelSocket())
	if unix, err := net.Listen("unix", cfg.PanelSocket()); err != nil {
		log.Warn("panel unix socket unavailable; edge cannot proxy the panel", "err", err)
	} else {
		_ = os.Chmod(cfg.PanelSocket(), 0o660)
		listeners = append(listeners, unix)
	}

	errc := make(chan error, len(listeners))
	for _, l := range listeners {
		log.Info("listening", "addr", l.Addr().String())
		go func(l net.Listener) {
			if err := httpSrv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}(l)
	}

	select {
	case <-ctx.Done():
	case err = <-errc:
		log.Error("server error", "err", err)
	}
	log.Info("shutting down")
	stop()
	shutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	srv.Wait()
	workers.Wait()
	_ = os.Remove(cfg.PanelSocket())
	return err
}

// bootstrapAdmin creates the first admin with a random password written to
// a 0600 file on first boot.
func bootstrapAdmin(ctx context.Context, cfg *config.Config, store *db.DB, log *slog.Logger) error {
	n, err := store.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	pw := auth.RandomPassword(18)
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := store.CreateUser(ctx, &db.User{Username: "admin", PasswordHash: hash, Role: db.RoleAdmin}); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.AdminPassPath(), []byte(pw+"\n"), 0o600); err != nil {
		return fmt.Errorf("write initial admin password: %w", err)
	}
	log.Warn("created initial admin account; password stored on disk — change it after first login",
		"username", "admin", "password_file", cfg.AdminPassPath())
	return nil
}

// resetPassword: openplexd reset-password [--data-dir DIR] <username>
func resetPassword(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: openplexd reset-password [--data-dir DIR] <username>")
	}
	username := args[len(args)-1]
	cfg, err := config.Load(args[:len(args)-1])
	if err != nil {
		return err
	}
	ctx := context.Background()
	store, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer store.Close()
	u, err := store.GetUserByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("user %q: %w", username, err)
	}
	pw := auth.RandomPassword(18)
	if u.PasswordHash, err = auth.HashPassword(pw); err != nil {
		return err
	}
	u.TOTPEnabled, u.TOTPSecret, u.Disabled = false, "", false
	if err := store.UpdateUser(ctx, u); err != nil {
		return err
	}
	if err := store.RevokeUserRefreshTokens(ctx, u.ID); err != nil {
		return err
	}
	fmt.Printf("New password for %s: %s\n(2FA was reset and all sessions revoked)\n", u.Username, pw)
	return nil
}
