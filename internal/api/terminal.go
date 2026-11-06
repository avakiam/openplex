package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"openplex.dev/openplex/internal/db"
)

const (
	terminalIdleTimeout = 30 * time.Minute
	terminalMaxDuration = 4 * time.Hour
)

// terminalMsg is a client → server control message.
type terminalMsg struct {
	T    string `json:"t"` // "i" input, "r" resize, "p" ping
	Data string `json:"d,omitempty"`
	Cols uint   `json:"c,omitempty"`
	Rows uint   `json:"r,omitempty"`
}

// handleTerminalWS bridges a WebSocket to an interactive shell in the site's
// environment. Server → client frames are raw terminal bytes (binary).
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	d := s.loadDomain(w, r)
	if d == nil {
		return
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no environment; open the parent website")
		return
	}
	if !s.requireOrch(w) {
		return
	}
	a := actor(r)
	if d.Status == db.StatusDisabled && a.Role != db.RoleAdmin {
		writeErr(w, http.StatusForbidden, "disabled", "this website was disabled by your provider")
		return
	}
	asRoot := r.URL.Query().Get("root") == "1"
	if asRoot && a.Role != db.RoleAdmin {
		forbidden(w)
		return
	}
	cols, _ := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
	rows, _ := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)

	c, err := s.acceptWS(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)
	ctx, cancel := context.WithTimeout(r.Context(), terminalMaxDuration)
	defer cancel()

	sendText := func(msg string) { _ = c.Write(ctx, websocket.MessageBinary, []byte(msg)) }
	if d.Runtime == "static" {
		sendText("\x1b[2mStarting a shell environment for " + d.Name + "…\x1b[0m\r\n")
	}
	sh, err := s.orch.OpenShell(ctx, d, asRoot, uint(cols), uint(rows))
	if err != nil {
		sendText("\r\n\x1b[31m" + err.Error() + "\x1b[0m\r\n")
		c.Close(websocket.StatusInternalError, "shell unavailable")
		return
	}
	defer sh.Close()

	started := time.Now()
	who := "site user"
	if asRoot {
		who = "root"
	}
	s.audit(r, "terminal.opened", d.Name, who)
	defer func() {
		s.audit(r, "terminal.closed", d.Name, fmt.Sprintf("%s, %s", who, time.Since(started).Round(time.Second)))
	}()

	var lastInput atomic.Int64
	lastInput.Store(time.Now().UnixNano())

	// Shell → browser.
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := sh.Reader().Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Idle watchdog.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-outDone:
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastInput.Load())) > terminalIdleTimeout {
					sendText("\r\n\x1b[33mSession closed after 30 minutes of inactivity.\x1b[0m\r\n")
					cancel()
					return
				}
			}
		}
	}()

	// Browser → shell.
	inErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				inErr <- err
				return
			}
			var m terminalMsg
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			switch m.T {
			case "i":
				lastInput.Store(time.Now().UnixNano())
				if _, err := sh.Write([]byte(m.Data)); err != nil {
					inErr <- err
					return
				}
			case "r":
				_ = sh.Resize(ctx, m.Cols, m.Rows)
			}
		}
	}()

	select {
	case <-outDone: // shell exited
		c.Close(websocket.StatusNormalClosure, "session ended")
	case <-inErr: // browser went away
	case <-ctx.Done():
		c.Close(websocket.StatusNormalClosure, "session timed out")
	}
}
