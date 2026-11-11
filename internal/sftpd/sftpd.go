// Package sftpd is OpenPlex's built-in SFTP server for website files. Users
// sign in with their panel username and one of their SSH keys or their SFTP
// password (never the panel password). The root directory lists the websites
// whose files they may change; every operation inside a website goes through
// an os.Root on its directory, so "..", absolute paths and symlinks cannot
// leave it, and new files belong to the website's own user.
package sftpd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
)

// Site is a website a user may reach over SFTP.
type Site struct {
	Name     string // directory name shown at the root (the website's hostname)
	Root     string // host directory of the website
	UID      int    // owner of new files
	ReadOnly bool   // shared read-only: no changes allowed
}

// Options configure the server.
type Options struct {
	Listen      string
	HostKeyPath string
	DB          *db.DB
	Log         *slog.Logger
	// Sites lists the websites u may reach (recomputed during a session, so
	// revoked access takes effect within seconds).
	Sites func(ctx context.Context, u *db.User) ([]Site, error)
}

// Limits.
const (
	maxConns      = 256
	maxConnsPerIP = 16
	idleTimeout   = 15 * time.Minute
	authTimeout   = 30 * time.Second
	failWindow    = 10 * time.Minute
	failLimit     = 10 // failed sign-ins per address before it is refused for failWindow
)

// Server is a running SFTP server.
type Server struct {
	opt    Options
	conf   *ssh.ServerConfig
	signer ssh.Signer

	mu    sync.Mutex
	conns int
	perIP map[string]int
	fails map[string][]time.Time
}

// New prepares the server and its host key (created on first use).
func New(opt Options) (*Server, error) {
	signer, err := hostKey(opt.HostKeyPath)
	if err != nil {
		return nil, err
	}
	s := &Server{opt: opt, signer: signer, perIP: map[string]int{}, fails: map[string][]time.Time{}}
	s.conf = &ssh.ServerConfig{
		MaxAuthTries:      6,
		ServerVersion:     "SSH-2.0-OpenPlex",
		PasswordCallback:  s.checkPassword,
		PublicKeyCallback: s.checkKey,
	}
	s.conf.AddHostKey(signer)
	return s, nil
}

// Port is the TCP port of the listen address.
func Port(listen string) (int, error) {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(p)
}

// hostKey loads the server's Ed25519 key, creating it the first time.
func hostKey(path string) (ssh.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "openplex sftp host key")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("write SFTP host key: %w", err)
	}
	return ssh.NewSignerFromKey(priv)
}

// Fingerprint is the SHA-256 fingerprint of the host key, shown in the panel
// so users can check it on first connection.
func (s *Server) Fingerprint() string {
	return ssh.FingerprintSHA256(s.signer.PublicKey())
}

// Run serves until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opt.Listen)
	if err != nil {
		return fmt.Errorf("SFTP listen: %w", err)
	}
	s.opt.Log.Info("listening", "addr", s.opt.Listen, "service", "sftp")
	return s.Serve(ctx, ln)
}

// Serve accepts connections on ln until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() { <-ctx.Done(); ln.Close() }()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		ip := remoteIP(c.RemoteAddr())
		if !s.admit(ip) {
			c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.release(ip)
			s.serveConn(ctx, c, ip)
		}()
	}
}

func remoteIP(a net.Addr) string {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.String()
	}
	host, _, _ := net.SplitHostPort(a.String())
	return host
}

func (s *Server) admit(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns >= maxConns || s.perIP[ip] >= maxConnsPerIP || s.blockedLocked(ip) {
		return false
	}
	s.conns++
	s.perIP[ip]++
	return true
}

func (s *Server) release(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns--
	if s.perIP[ip]--; s.perIP[ip] <= 0 {
		delete(s.perIP, ip)
	}
}

func (s *Server) blockedLocked(ip string) bool {
	cut := time.Now().Add(-failWindow)
	recent := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if t.After(cut) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(s.fails, ip)
		return false
	}
	s.fails[ip] = recent
	return len(recent) >= failLimit
}

// failed records a failed sign-in. The log line is the one the Fail2Ban
// "openplex" jail matches, so repeated failures ban the address.
func (s *Server) failed(ip, user string) {
	s.mu.Lock()
	s.fails[ip] = append(s.fails[ip], time.Now())
	s.mu.Unlock()
	s.opt.Log.Warn("authentication failed", "ip", ip, "user", user, "service", "sftp")
}

// idleConn closes connections without traffic for idleTimeout.
type idleConn struct{ net.Conn }

func (c idleConn) Read(b []byte) (int, error) {
	_ = c.Conn.SetDeadline(time.Now().Add(idleTimeout))
	return c.Conn.Read(b)
}

func (s *Server) serveConn(ctx context.Context, c net.Conn, ip string) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(authTimeout))
	sc, chans, reqs, err := ssh.NewServerConn(c, s.conf)
	if err != nil {
		return
	}
	defer sc.Close()
	_ = c.SetDeadline(time.Now().Add(idleTimeout))
	go ssh.DiscardRequests(reqs)

	uid, _ := strconv.ParseInt(sc.Permissions.Extensions["uid"], 10, 64)
	user, err := s.opt.DB.GetUser(ctx, uid)
	if err != nil {
		return
	}
	s.opt.Log.Info("sftp session", "user", user.Username, "ip", ip, "auth", sc.Permissions.Extensions["method"])
	_ = s.opt.DB.Audit(ctx, db.AuditEntry{UserID: &user.ID, Action: "sftp.login", Target: user.Username, Detail: sc.Permissions.Extensions["method"], IP: ip})

	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only SFTP sessions are available")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ctx, ch, creqs, user)
	}
}

// session serves one channel: only the "sftp" subsystem is accepted (no shell, no exec).
func (s *Server) session(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request, user *db.User) {
	defer ch.Close()
	for req := range reqs {
		if req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
			_ = req.Reply(true, nil)
			fs := newFS(ctx, user, s.opt.Sites)
			srv := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: fs, FilePut: fs, FileCmd: fs, FileList: fs})
			_ = srv.Serve()
			srv.Close()
			return
		}
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

func (s *Server) lookup(meta ssh.ConnMetadata) (*db.User, string) {
	ip := remoteIP(meta.RemoteAddr())
	s.mu.Lock()
	blocked := s.blockedLocked(ip)
	s.mu.Unlock()
	if blocked {
		return nil, ip
	}
	u, err := s.opt.DB.GetUserByUsername(context.Background(), meta.User())
	if err != nil || u.Disabled {
		return nil, ip
	}
	return u, ip
}

func perms(u *db.User, method string) *ssh.Permissions {
	return &ssh.Permissions{Extensions: map[string]string{"uid": strconv.FormatInt(u.ID, 10), "method": method}}
}

var errDenied = errors.New("access denied")

func (s *Server) checkPassword(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
	u, ip := s.lookup(meta)
	if u != nil {
		if h, err := s.opt.DB.SFTPPasswordHash(context.Background(), u.ID); err == nil && h != "" && auth.VerifyPassword(h, string(pw)) {
			return perms(u, "password"), nil
		}
	}
	s.failed(ip, meta.User())
	return nil, errDenied
}

func (s *Server) checkKey(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	u, _ := s.lookup(meta)
	if u != nil {
		keys, err := s.opt.DB.ListSSHKeys(context.Background(), u.ID)
		if err == nil {
			want := key.Marshal()
			for _, k := range keys {
				pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
				if err == nil && bytes.Equal(pk.Marshal(), want) {
					return perms(u, "key "+k.Fingerprint), nil
				}
			}
		}
	}
	// Clients offer several keys before trying a password: an unknown key is
	// not an attack by itself, so it is not counted as a failure.
	return nil, errDenied
}
