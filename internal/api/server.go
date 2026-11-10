// Package api is the REST + WebSocket API of openplexd (mounted at /api/v1).
package api

import (
	"context"

	"log/slog"
	"net/http"
	"openplex.dev/openplex/internal/backup"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/captcha"
	"openplex.dev/openplex/internal/config"
	"openplex.dev/openplex/internal/cron"
	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/dns"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/firewall"
	"openplex.dev/openplex/internal/mail"
	"openplex.dev/openplex/internal/monitor"
	"openplex.dev/openplex/internal/secrets"
)

// Server holds API dependencies.
type Server struct {
	cfg      *config.Config
	db       *db.DB
	orch     *docker.Orchestrator // nil when Docker is disabled/unavailable
	mon      *monitor.Sampler
	signer   *auth.Signer
	box      *secrets.Box
	backups  *backup.Manager // nil without Docker
	cron     *cron.Manager
	firewall *firewall.Manager
	dns      *dns.Manager
	mail     *mail.Manager
	log      *slog.Logger
	ui       http.Handler
	version  string

	loginLimiter  *limiter
	tickets       ticketStore
	captchaPasses ticketStore // captcha solved, password right: the 2FA step may skip it
	captcha       *captcha.Verifier

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
	Box          *secrets.Box    // encrypts recoverable secrets (database passwords)
	Backups      *backup.Manager // nil when Docker is unavailable
	Cron         *cron.Manager
	Firewall     *firewall.Manager
	DNS          *dns.Manager
	Mail         *mail.Manager
}

// New builds the API server.
func New(o Options) *Server {
	return &Server{
		cfg: o.Config, db: o.DB, orch: o.Orchestrator, mon: o.Monitor, log: o.Logger, ui: o.UI, version: o.Version,
		signer:       auth.NewSigner(o.Config.Secret, o.Config.AccessTTL),
		box:          o.Box,
		backups:      o.Backups,
		cron:         o.Cron,
		firewall:     o.Firewall,
		dns:          o.DNS,
		mail:         o.Mail,
		loginLimiter: newLimiter(10, 5*time.Minute),
		captcha:      captcha.NewVerifier(),
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
		r.Get("/auth/captcha", s.handleLoginCaptcha)
		r.Post("/auth/refresh", s.handleRefresh)
		r.Post("/auth/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)

			r.Get("/auth/me", s.handleMe)
			r.Patch("/auth/me", s.handleUpdateMe)
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
			r.Post("/cron/preview", s.handleCronPreview)
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
				r.Get("/backups", s.handleItemBackups)
				r.Post("/backups", s.handleItemBackupNow)
				r.Post("/backups/{snap}/restore", s.handleItemRestore)
				r.Get("/logs/ws", s.handleLogsWS)
				r.Get("/terminal/ws", s.handleTerminalWS)
				r.Get("/redirects", s.handleListRedirects)
				r.Post("/redirects", s.handleCreateRedirect)
				r.Delete("/redirects/{rid}", s.handleDeleteRedirect)
				r.Get("/cron", s.handleListCron)
				r.Post("/cron", s.handleCreateCron)
				r.Patch("/cron/{jid}", s.handleUpdateCron)
				r.Delete("/cron/{jid}", s.handleDeleteCron)
				r.Post("/cron/{jid}/run", s.handleRunCron)
				r.Get("/cron/{jid}/runs", s.handleListCronRuns)
				r.Get("/dns", s.handleGetZone)
				r.Post("/dns/records", s.handleCreateRecord)
				r.Patch("/dns/records/{rid}", s.handleUpdateRecord)
				r.Delete("/dns/records/{rid}", s.handleDeleteRecord)
				r.Get("/mail", s.handleGetDomainMail)
				r.Post("/mail", s.handleEnableMail)
				r.Delete("/mail", s.handleDisableMail)
				r.Post("/mail/mailboxes", s.handleCreateMailbox)
				r.Patch("/mail/mailboxes/{mid}", s.handleUpdateMailbox)
				r.Delete("/mail/mailboxes/{mid}", s.handleDeleteMailbox)
				r.Post("/mail/mailboxes/{mid}/webmail", s.handleWebmailLogin)
				r.Get("/mail/backups", s.handleItemBackups)
				r.Post("/mail/backups", s.handleItemBackupNow)
				r.Post("/mail/backups/{snap}/restore", s.handleMailRestore)
				r.Post("/mail/aliases", s.handleCreateAlias)
				r.Patch("/mail/aliases/{aid}", s.handleUpdateAlias)
				r.Delete("/mail/aliases/{aid}", s.handleDeleteAlias)

				r.Get("/files", s.handleListFiles)
				r.Get("/files/content", s.handleReadFile)
				r.Put("/files/content", s.handleWriteFile)
				r.Get("/files/download", s.handleDownloadFile)
				r.Post("/files/upload", s.handleUpload)
				for _, op := range []string{"mkdir", "rename", "delete", "chmod", "extract", "compress"} {
					r.Post("/files/"+op, s.handleFileOp)
				}
			})

			r.Get("/database-engines", s.handleListEngines)
			r.Get("/databases", s.handleListDatabases)
			r.Post("/databases", s.handleCreateDatabase)
			r.Get("/databases/sizes", s.handleDatabaseSizes)
			r.Get("/databases/{id}", s.handleGetDatabase)
			r.Delete("/databases/{id}", s.handleDeleteDatabase)
			r.Get("/databases/{id}/credentials", s.handleDatabaseCredentials)
			r.Post("/databases/{id}/rotate-password", s.handleRotateDatabasePassword)
			r.Get("/databases/{id}/backups", s.handleItemBackups)
			r.Post("/databases/{id}/backups", s.handleItemBackupNow)
			r.Post("/databases/{id}/backups/{snap}/restore", s.handleItemRestore)

			r.Group(func(r chi.Router) {
				r.Use(requireRole(db.RoleAdmin))
				r.Get("/system/info", s.handleSystemInfo)
				r.Get("/system/stats", s.handleSystemStats)
				r.Get("/system/stats/ws", s.handleStatsWS)
				r.Get("/system/containers", s.handleContainers)
				r.Post("/system/reconcile", s.handleReconcile)
				r.Get("/audit", s.handleAudit)
				r.Get("/backups/settings", s.handleGetBackupSettings)
				r.Put("/backups/settings", s.handlePutBackupSettings)
				r.Post("/backups/test", s.handleTestBackupRepo)
				r.Get("/backups/recovery-key", s.handleRecoveryKey)
				r.Post("/backups/run", s.handleRunBackup)
				r.Get("/backups/runs", s.handleListBackupRuns)
				r.Get("/backups/panel-snapshots", s.handlePanelSnapshots)
				r.Get("/firewall", s.handleGetFirewall)
				r.Post("/firewall/apply", s.handleApplyFirewall)
				r.Post("/firewall/confirm", s.handleConfirmFirewall)
				r.Post("/firewall/revert", s.handleRevertFirewall)
				r.Post("/firewall/blocks", s.handleCreateFirewallBlock)
				r.Delete("/firewall/blocks/{id}", s.handleDeleteFirewallBlock)
				r.Get("/fail2ban", s.handleGetFail2Ban)
				r.Get("/dns", s.handleGetDNS)
				r.Put("/dns/settings", s.handlePutDNSSettings)
				r.Get("/mail", s.handleGetMail)
				r.Put("/mail/settings", s.handlePutMailSettings)
				r.Get("/captcha", s.handleGetCaptcha)
				r.Put("/captcha", s.handlePutCaptcha)
				r.Post("/fail2ban/unban", s.handleFail2BanUnban)
			})
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusNotFound, "not_found", "no such endpoint")
		})
	})

	r.Handle("/tools/adminer", s.adminerProxy())
	r.Handle("/tools/adminer/*", s.adminerProxy())
	if s.ui != nil {
		r.Handle("/*", s.ui)
	}
	return r
}
