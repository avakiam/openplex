package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
)

type ctxKey int

const principalKey ctxKey = iota

// Principal is the authenticated caller.
type Principal struct {
	User     *db.User
	APIKeyID int64 // non-zero when authenticated by API key
}

func principalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

func actor(r *http.Request) *db.User { return principalFrom(r.Context()).User }

// clientIP returns the caller's IP. X-Forwarded-For is trusted only for
// requests arriving over the edge's unix socket.
func clientIP(r *http.Request) string {
	if r.RemoteAddr == "@" || r.RemoteAddr == "" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		return "unix"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// authenticate resolves a bearer JWT, API key or WebSocket ticket.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var userID, keyID int64
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch {
		case tok != "" && tok != r.Header.Get("Authorization") && auth.IsAPIKey(tok):
			k, err := s.db.GetAPIKeyByHash(ctx, auth.HashToken(tok))
			if err != nil || (k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt)) {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid API key")
				return
			}
			userID, keyID = k.UserID, k.ID
			if k.LastUsedAt == nil || time.Since(*k.LastUsedAt) > time.Minute {
				_ = s.db.TouchAPIKey(ctx, k.ID)
			}
		case tok != "" && tok != r.Header.Get("Authorization"):
			id, _, err := s.signer.Verify(tok)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "token_invalid", "access token invalid or expired")
				return
			}
			userID = id
		case r.URL.Query().Get("ticket") != "":
			id, ok := s.tickets.redeem(r.URL.Query().Get("ticket"))
			if !ok {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or expired ticket")
				return
			}
			userID = id
		default:
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		u, err := s.db.GetUser(ctx, userID)
		if err != nil || u.Disabled {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "account unavailable")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, principalKey, &Principal{User: u, APIKeyID: keyID})))
	})
}

// requireRole allows only the listed roles.
func requireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := actor(r)
			for _, role := range roles {
				if u.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			forbidden(w)
		})
	}
}

// requestLogger logs one line per request.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if r.URL.Path == "/api/v1/system/health" {
			return
		}
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"bytes", ww.BytesWritten(), "dur", time.Since(start).Round(time.Microsecond), "ip", clientIP(r))
	})
}

// securityHeaders sets conservative defaults for the panel.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// ---------- rate limiting ----------

// limiter is a fixed-window counter keyed by string.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	n     int
}

func newLimiter(limit int, per time.Duration) *limiter {
	return &limiter{limit: limit, window: per, hits: map[string]*window{}}
}

// allow records a hit and reports whether key is under its limit.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 10000 {
		for k, w := range l.hits {
			if now.Sub(w.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) > l.window {
		l.hits[key] = &window{start: now, n: 1}
		return true
	}
	w.n++
	return w.n <= l.limit
}

// ---------- WebSocket tickets ----------

// ticketStore issues single-use, 30-second tickets so browsers can open
// authenticated WebSockets without putting long-lived tokens in URLs.
type ticketStore struct {
	mu sync.Mutex
	m  map[string]ticket
}

type ticket struct {
	userID int64
	exp    time.Time
}

func (t *ticketStore) issue(userID int64) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]ticket{}
	}
	now := time.Now()
	for k, v := range t.m {
		if now.After(v.exp) {
			delete(t.m, k)
		}
	}
	if len(t.m) > 10000 {
		return "", errors.New("too many outstanding tickets")
	}
	t.m[id] = ticket{userID: userID, exp: now.Add(30 * time.Second)}
	return id, nil
}

func (t *ticketStore) redeem(id string) (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.m[id]
	delete(t.m, id)
	if !ok || time.Now().After(v.exp) {
		return 0, false
	}
	return v.userID, true
}
