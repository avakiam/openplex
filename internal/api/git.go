package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/gitdeploy"
	"openplex.dev/openplex/internal/secrets"
	"openplex.dev/openplex/internal/ssl"
)

// gitDeployTimeout bounds one deploy: fetching and the build command.
const gitDeployTimeout = 30 * time.Minute

type gitView struct {
	*db.GitDeploy
	// WebhookPath is the panel path that triggers a deploy (it holds the secret token).
	WebhookPath string `json:"webhook_path"`
}

func gitWebhookPath(g *db.GitDeploy) string {
	return fmt.Sprintf("/api/v1/hooks/git/%d/%s", g.DomainID, g.WebhookToken)
}

func (s *Server) handleGetGit(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	g, err := s.db.GetGitDeploy(r.Context(), d.ID)
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusOK, nil)
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gitView{GitDeploy: g, WebhookPath: gitWebhookPath(g)})
}

// handlePutGit creates or changes the Git deploy of a website. A new one
// gets its own deploy key and webhook token; nothing is deployed yet (the
// key must first be added to the repository).
func (s *Server) handlePutGit(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	var in struct {
		RepoURL       string `json:"repo_url"`
		Branch        string `json:"branch"`
		Target        string `json:"target"`
		BuildCommand  string `json:"build_command"`
		AutoDeploy    bool   `json:"auto_deploy"`
		RotateWebhook bool   `json:"rotate_webhook"`
	}
	if !decode(w, r, &in) {
		return
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no files of their own; set up Git on the website")
		return
	}
	target, err := gitdeploy.CleanTarget(in.Target)
	if err == nil {
		err = gitdeploy.ValidRepo(in.RepoURL)
	}
	if err == nil {
		err = gitdeploy.ValidBranch(in.Branch)
	}
	if err == nil {
		err = gitdeploy.ValidBuild(in.BuildCommand)
	}
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	ctx := r.Context()
	g, err := s.db.GetGitDeploy(ctx, d.ID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		priv, pub, kerr := gitdeploy.NewKey("openplex-" + d.Name)
		if kerr != nil {
			s.fail(w, r, kerr)
			return
		}
		enc, kerr := s.box.Seal(priv)
		if kerr != nil {
			s.fail(w, r, kerr)
			return
		}
		g = &db.GitDeploy{DomainID: d.ID, RepoURL: in.RepoURL, Branch: in.Branch, Target: target, BuildCommand: in.BuildCommand,
			AutoDeploy: in.AutoDeploy, WebhookToken: secrets.Password(32), KeyEnc: enc, PublicKey: pub}
		if err := s.db.CreateGitDeploy(ctx, g); err != nil {
			s.fail(w, r, err)
			return
		}
		s.audit(r, "git.created", d.Name, in.RepoURL+" "+in.Branch)
		writeJSON(w, http.StatusCreated, gitView{GitDeploy: g, WebhookPath: gitWebhookPath(g)})
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	g.RepoURL, g.Branch, g.Target, g.BuildCommand, g.AutoDeploy = in.RepoURL, in.Branch, target, in.BuildCommand, in.AutoDeploy
	if in.RotateWebhook {
		g.WebhookToken = secrets.Password(32)
	}
	if err := s.db.UpdateGitDeploy(ctx, g); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "git.updated", d.Name, in.RepoURL+" "+in.Branch)
	writeJSON(w, http.StatusOK, gitView{GitDeploy: g, WebhookPath: gitWebhookPath(g)})
}

func (s *Server) handleDeleteGit(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	g, err := s.db.GetGitDeploy(r.Context(), d.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if g.Status == db.GitRunning {
		writeErr(w, http.StatusConflict, "conflict", "a deploy is running; wait for it to finish")
		return
	}
	if err := s.db.DeleteGitDeploy(r.Context(), d.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.orch != nil {
		_ = os.RemoveAll(s.orch.GitDir(d.ID))
	}
	s.audit(r, "git.removed", d.Name, g.RepoURL)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGitDeployNow(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil || !s.requireOrch(w) {
		return
	}
	started, err := s.db.StartGitDeploy(r.Context(), d.ID, "manual")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "git.deploy", d.Name, "")
	if started {
		s.runGitDeploys(d.ID)
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": !started})
}

// handleGitWebhook is called by the Git host on a push. It needs no session:
// the token in the path is the secret.
func (s *Server) handleGitWebhook(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	g, err := s.db.GetGitDeploy(r.Context(), id)
	if err == nil && subtle.ConstantTimeCompare([]byte(g.WebhookToken), []byte(chi.URLParam(r, "token"))) != 1 {
		err = db.ErrNotFound
	}
	if err != nil {
		if !s.webhookLimiter.allow(ip) {
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts")
			return
		}
		writeErr(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if r.Header.Get("X-GitHub-Event") == "ping" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
		return
	}
	if !g.AutoDeploy {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "automatic deploys are off"})
		return
	}
	var payload struct {
		Ref string `json:"ref"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	_ = json.Unmarshal(body, &payload)
	if b := gitdeploy.PushedBranch(payload.Ref); b != "" && b != g.Branch {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "push to another branch"})
		return
	}
	if s.orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "docker_unavailable", "Docker is not available on this server")
		return
	}
	started, err := s.db.StartGitDeploy(r.Context(), id, "webhook")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.db.Audit(r.Context(), db.AuditEntry{Action: "git.webhook", Target: strconv.FormatInt(id, 10), Detail: ip}); err != nil {
		s.log.Warn("audit write failed", "err", err)
	}
	if started {
		s.runGitDeploys(id)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deploying"})
}

// runGitDeploys deploys website id in the background, again as long as
// deploys were asked for while one ran.
func (s *Server) runGitDeploys(id int64) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		for again := true; again; {
			ctx, cancel := context.WithTimeout(s.baseCtx, gitDeployTimeout)
			commit, subject, out, err := s.gitDeploy(ctx, id)
			cancel()
			failure := ""
			if err != nil {
				failure = err.Error()
				s.log.Warn("git deploy failed", "domain_id", id, "err", err)
			} else {
				s.log.Info("git deploy finished", "domain_id", id, "commit", commit)
			}
			var ferr error
			again, ferr = s.db.FinishGitDeploy(context.WithoutCancel(s.baseCtx), id, commit, subject, out, failure)
			if ferr != nil {
				s.log.Warn("record git deploy", "err", ferr)
				return
			}
		}
	}()
}

// gitDeploy fetches the branch, runs the build command and restarts
// applications that keep their code in memory.
func (s *Server) gitDeploy(ctx context.Context, id int64) (commit, subject, output string, err error) {
	out := &tailBuffer{max: 32 << 10}
	defer func() { output = out.String() }()
	d, err := s.db.GetDomain(ctx, id)
	if err != nil {
		return "", "", "", err
	}
	g, err := s.db.GetGitDeploy(ctx, id)
	if err != nil {
		return "", "", "", err
	}
	key, err := s.box.Open(g.KeyEnc)
	if err != nil {
		return "", "", "", fmt.Errorf("reading the deploy key: %w", err)
	}
	dir := s.orch.GitDir(id)
	uid := docker.SiteUID(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", "", err
	}
	_ = os.Chown(dir, uid, uid)
	keyFile := filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		return "", "", "", err
	}
	defer os.Remove(keyFile)
	_ = os.Chown(keyFile, uid, uid)

	code, err := s.orch.RunGit(ctx, d, g.RepoURL, g.Branch, g.Target, out)
	if err != nil {
		return "", "", "", err
	}
	commit, subject = gitdeploy.Result(out.String())
	if code != 0 {
		return "", "", "", fmt.Errorf("fetching the repository failed (exit %d): %s", code, lastLines(out.String(), 4))
	}
	if g.BuildCommand != "" {
		fmt.Fprintf(out, "==> running the build command\n")
		dir := path.Join(ssl.EdgeSitesRoot, d.Name, g.Target)
		for {
			code, err = s.orch.RunSiteBuild(ctx, d, dir, g.BuildCommand, out)
			if !errors.Is(err, docker.ErrSiteNotRunning) {
				break
			}
			if d.Status != db.StatusRunning {
				return commit, subject, "", errors.New("the website is not running, so the build command could not run; start it and deploy again")
			}
			select {
			case <-ctx.Done():
				return commit, subject, "", ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
		if err != nil {
			return commit, subject, "", err
		}
		if code != 0 {
			return commit, subject, "", fmt.Errorf("the build command failed (exit %d): %s", code, lastLines(out.String(), 4))
		}
	}
	switch d.Runtime {
	case "node", "python", "go":
		if d.Status == db.StatusRunning {
			fmt.Fprintf(out, "==> restarting the application\n")
			if err := s.orch.RestartSite(ctx, d); err != nil {
				return commit, subject, "", fmt.Errorf("restarting the application: %w", err)
			}
		}
	}
	fmt.Fprintf(out, "==> deployed %s\n", commit)
	return commit, subject, "", nil
}
