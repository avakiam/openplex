// Package api is the REST + WebSocket API of openplexd (mounted at /api/v1).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/monitor"
)

// Server holds API dependencies.
type Server struct {
	cfg     *config.Config
	db      *db.DB
	orch    *docker.Orchestrator // nil when Docker is disabled/unavailable
	mon     *monitor.Sampler
	signer  *auth.Signer
	log     *slog.Logger
	ui      http.Handler
	version string

	loginLimiter *limiter
	tickets      ticketStore

	baseCtx context.Context
	bg      sync.WaitGroup
}

// Options configures New.
type Options struct {
	Config       *config.Config
	DB           *db.DB
	Orchestrator *docker.Orchestrator
	Monitor      *monitor.Sampler
	Logger       *slog.Logger
	UI           http.Handler
	Version      string
	BaseContext  context.Context // cancelled at shutdown; parent of background jobs
}

// New builds the API server.
func New(o Options) *Server {
	return &Server{
		cfg: o.Config, db: o.DB, orch: o.Orchestrator, mon: o.Monitor, log: o.Logger, ui: o.UI, version: o.Version,
		signer:       auth.NewSigner(o.Config.Secret, o.Config.AccessTTL),
		loginLimiter: newLimiter(10, 5*time.Minute),
		baseCtx:      o.BaseContext,
	}
}

// Wait blocks until background jobs finish.
func (s *Server) Wait() { s.bg.Wait() }

// Handler returns the root HTTP handler (API + embedded dashboard).
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, s.requestLogger, securityHeaders)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/system/health", s.handleHealth)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/refresh", s.handleRefresh)
		r.Post("/auth/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)

			r.Get("/auth/me", s.handleMe)
			r.Post("/auth/password", s.handleChangeOwnPassword)
			r.Post("/auth/2fa/setup", s.handleTOTPSetup)
			r.Post("/auth/2fa/enable", s.handleTOTPEnable)
			r.Post("/auth/2fa/disable", s.handleTOTPDisable)
			r.Post("/auth/ws-ticket", s.handleWSTicket)

			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Get("/users/{id}", s.handleGetUser)
			r.Patch("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)
			r.Post("/users/{id}/password", s.handleSetUserPassword)
			r.Post("/users/{id}/2fa/reset", s.handleResetUserTOTP)

			r.Get("/api-keys", s.handleListAPIKeys)
			r.Post("/api-keys", s.handleCreateAPIKey)
			r.Delete("/api-keys/{id}", s.handleDeleteAPIKey)
			r.Get("/ssh-keys", s.handleListSSHKeys)
			r.Post("/ssh-keys", s.handleCreateSSHKey)
			r.Delete("/ssh-keys/{id}", s.handleDeleteSSHKey)

			r.Get("/runtimes", s.handleListRuntimes)
			r.Get("/domains", s.handleListDomains)
			r.Post("/domains", s.handleCreateDomain)
			r.Route("/domains/{id}", func(r chi.Router) {
				r.Get("/", s.handleGetDomain)
				r.Patch("/", s.handleUpdateDomain)
				r.Delete("/", s.handleDeleteDomain)
				for _, a := range []string{"deploy", "start", "stop", "restart"} {
					r.Post("/actions/"+a, s.handleDomainAction)
				}
				r.Get("/stats", s.handleDomainStats)
				r.Get("/logs/ws", s.handleLogsWS)
				r.Get("/redirects", s.handleListRedirects)
				r.Post("/redirects", s.handleCreateRedirect)
				r.Delete("/redirects/{rid}", s.handleDeleteRedirect)

				r.Get("/files", s.handleListFiles)
				r.Get("/files/content", s.handleReadFile)
				r.Put("/files/content", s.handleWriteFile)
				r.Get("/files/download", s.handleDownloadFile)
				r.Post("/files/upload", s.handleUpload)
				for _, op := range []string{"mkdir", "rename", "delete", "chmod", "extract", "compress"} {
					r.Post("/files/"+op, s.handleFileOp)
				}
			})

			r.Group(func(r chi.Router) {
				r.Use(requireRole(db.RoleAdmin))
				r.Get("/system/info", s.handleSystemInfo)
				r.Get("/system/stats", s.handleSystemStats)
				r.Get("/system/stats/ws", s.handleStatsWS)
				r.Get("/system/containers", s.handleContainers)
				r.Post("/system/reconcile", s.handleReconcile)
				r.Get("/audit", s.handleAudit)
			})
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusNotFound, "not_found", "no such endpoint")
		})
	})

	if s.ui != nil {
		r.Handle("/*", s.ui)
	}
	return r
}
