package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/monitor"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{"status": "ok", "version": s.version, "docker": s.orch != nil}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"host": monitor.Info(r.Context()), "version": s.version}
	if s.orch != nil {
		if info, err := s.orch.Info(r.Context()); err == nil {
			out["docker"] = info
		} else {
			out["docker_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"history": s.mon.History()})
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	if s.orch == nil {
		writeJSON(w, http.StatusOK, []docker.ContainerStats{})
		return
	}
	st, err := s.orch.Stats(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if s.orch == nil {
		writeErr(w, http.StatusServiceUnavailable, "docker_unavailable", "the Docker orchestrator is not running")
		return
	}
	s.audit(r, "system.reconcile", "", "")
	s.background(s.orch.Reconcile)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	es, err := s.db.ListAudit(r.Context(), before, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, es)
}

// ---------- WebSockets ----------

func (s *Server) acceptWS(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	opts := &websocket.AcceptOptions{}
	if s.cfg.Dev {
		opts.InsecureSkipVerify = true // dev UI runs on a different origin
	}
	return websocket.Accept(w, r, opts)
}

// handleStatsWS streams host samples (and container stats every 3rd tick).
func (s *Server) handleStatsWS(w http.ResponseWriter, r *http.Request) {
	c, err := s.acceptWS(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())
	ch, cancel := s.mon.Subscribe()
	defer cancel()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case smp := <-ch:
			msg := map[string]any{"type": "host", "sample": smp}
			if err := wsjson.Write(ctx, c, msg); err != nil {
				return
			}
			tick++
			if s.orch != nil && tick%3 == 1 {
				if st, err := s.orch.Stats(ctx); err == nil {
					if err := wsjson.Write(ctx, c, map[string]any{"type": "containers", "containers": st}); err != nil {
						return
					}
				}
			}
		}
	}
}

// wsWriter adapts a WebSocket to io.Writer (one text frame per Write).
type wsWriter struct {
	ctx context.Context
	c   *websocket.Conn
}

func (ww wsWriter) Write(p []byte) (int, error) {
	if err := ww.c.Write(ww.ctx, websocket.MessageText, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// handleLogsWS streams a site's access log (edge) or error log (container output).
func (s *Server) handleLogsWS(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	kind := r.URL.Query().Get("type")
	if kind == "" {
		kind = "access"
	}
	if kind != "access" && kind != "error" {
		badRequest(w, "type must be access or error")
		return
	}
	site := d
	if d.Kind == db.KindAlias && d.ParentID != nil {
		p, err := s.db.GetDomain(r.Context(), *d.ParentID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		site = p
	}
	c, err := s.acceptWS(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())
	out := wsWriter{ctx: ctx, c: c}
	switch kind {
	case "access":
		err = tailFile(ctx, filepath.Join(s.cfg.LogsDir(), site.Name, "access.log"), 64<<10, out)
	case "error":
		if s.orch == nil {
			err = errors.New("docker unavailable")
		} else {
			err = s.orch.FollowSiteLogs(ctx, site.ID, 200, out)
		}
	}
	if err != nil && ctx.Err() == nil {
		c.Close(websocket.StatusInternalError, truncate(err.Error(), 120))
		return
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// tailFile sends the last `back` bytes of path, then follows appended data,
// coping with truncation and rotation.
func tailFile(ctx context.Context, path string, back int64, w io.Writer) error {
	var f *os.File
	var off int64
	open := func(fromEnd bool) error {
		var err error
		f, err = os.Open(path)
		if err != nil {
			return err
		}
		st, err := f.Stat()
		if err != nil {
			return err
		}
		off = 0
		if fromEnd && st.Size() > back {
			off = st.Size() - back
		}
		_, err = f.Seek(off, io.SeekStart)
		return err
	}
	for {
		err := open(true)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select { // log not created yet (no traffic): wait for it
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
	defer func() { f.Close() }()
	buf := make([]byte, 32<<10)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		for {
			n, err := f.Read(buf)
			if n > 0 {
				off += int64(n)
				if _, werr := w.Write(buf[:n]); werr != nil {
					return nil
				}
			}
			if err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		cur, _ := f.Stat()
		if !os.SameFile(st, cur) || st.Size() < off { // rotated or truncated
			f.Close()
			if err := open(false); err != nil {
				return err
			}
		}
	}
}
