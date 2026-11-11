package sftpd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"openplex.dev/openplex/internal/auth"
	"openplex.dev/openplex/internal/db"
)

type fixture struct {
	addr    string
	sites   string // directory holding the websites
	outside string // a directory no website may reach
	key     ssh.Signer
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	store := db.OpenMemory(t)
	u := &db.User{Username: "ana", PasswordHash: "x", Role: db.RoleCustomer}
	if err := store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	h, _ := auth.HashPassword("sftp-Secret-2026")
	if err := store.SetSFTPPassword(ctx, u.ID, h); err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	norm, fp, err := auth.ParseSSHPublicKey(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSSHKey(ctx, &db.SSHKey{UserID: u.ID, Name: "laptop", PublicKey: norm, Fingerprint: fp}); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	f := fixture{sites: filepath.Join(base, "sites"), outside: filepath.Join(base, "secret"), key: signer}
	for _, d := range []string{"blog.example/public", "shop.example/public"} {
		_ = os.MkdirAll(filepath.Join(f.sites, d), 0o755)
	}
	_ = os.MkdirAll(f.outside, 0o755)
	_ = os.WriteFile(filepath.Join(f.outside, "key.pem"), []byte("private"), 0o600)
	_ = os.WriteFile(filepath.Join(f.sites, "blog.example/public/index.html"), []byte("<h1>hi</h1>"), 0o644)
	_ = os.Symlink(f.outside, filepath.Join(f.sites, "blog.example/public/escape"))

	srv, err := New(Options{
		HostKeyPath: filepath.Join(base, "host_key"), DB: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sites: func(_ context.Context, u *db.User) ([]Site, error) {
			if u.Username != "ana" {
				return nil, nil
			}
			return []Site{
				{Name: "blog.example", Root: filepath.Join(f.sites, "blog.example"), UID: os.Getuid()},
				{Name: "shop.example", Root: filepath.Join(f.sites, "shop.example"), UID: os.Getuid(), ReadOnly: true},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.addr = ln.Addr().String()
	go func() { _ = srv.Serve(ctx, ln) }()
	return f
}

func dial(t *testing.T, addr string, am ssh.AuthMethod) (*sftp.Client, error) {
	t.Helper()
	conn, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "ana", Auth: []ssh.AuthMethod{am}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		return nil, err
	}
	c, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	t.Cleanup(func() { c.Close(); conn.Close() })
	return c, nil
}

func TestAuthentication(t *testing.T) {
	f := setup(t)
	if _, err := dial(t, f.addr, ssh.Password("wrong-password-123")); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := dial(t, f.addr, ssh.Password("sftp-Secret-2026")); err != nil {
		t.Fatalf("password: %v", err)
	}
	if _, err := dial(t, f.addr, ssh.PublicKeys(f.key)); err != nil {
		t.Fatalf("key: %v", err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	stranger, _ := ssh.NewSignerFromKey(other)
	if _, err := dial(t, f.addr, ssh.PublicKeys(stranger)); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestFiles(t *testing.T) {
	f := setup(t)
	c, err := dial(t, f.addr, ssh.PublicKeys(f.key))
	if err != nil {
		t.Fatal(err)
	}
	root, err := c.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range root {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"blog.example", "shop.example"}) {
		t.Fatalf("root lists %v", names)
	}

	// Read, write, mkdir, rename, remove inside a website.
	r, err := c.Open("/blog.example/public/index.html")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "<h1>hi</h1>" {
		t.Fatalf("read %q", b)
	}
	if err := c.Mkdir("/blog.example/public/img"); err != nil {
		t.Fatal(err)
	}
	w, err := c.Create("/blog.example/public/img/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("hello"))
	w.Close()
	if err := c.Rename("/blog.example/public/img/a.txt", "/blog.example/public/b.txt"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.sites, "blog.example/public/b.txt")); string(got) != "hello" {
		t.Fatalf("uploaded content %q", got)
	}
	if st, _ := os.Stat(filepath.Join(f.sites, "blog.example/public/b.txt")); st.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", st.Mode())
	}
	if err := c.Chmod("/blog.example/public/b.txt", 0o4755); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(f.sites, "blog.example/public/b.txt")); st.Mode()&fs.ModeSetuid != 0 {
		t.Fatal("setuid bit set")
	}
	if err := c.Remove("/blog.example/public/b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDirectory("/blog.example/public/img"); err != nil {
		t.Fatal(err)
	}

	// Nothing escapes a website.
	for _, p := range []string{"/blog.example/public/escape/key.pem", "/blog.example/../secret/key.pem", "/blog.example/public/../../shop.example/x"} {
		if r, err := c.Open(p); err == nil {
			b, _ := io.ReadAll(r)
			r.Close()
			if strings.Contains(string(b), "private") {
				t.Fatalf("%s escaped the website", p)
			}
		}
	}
	if _, err := c.ReadDir("/blog.example/public/escape"); err == nil {
		t.Fatal("listed a directory outside the website through a symlink")
	}
	if err := c.Symlink("/etc/passwd", "/blog.example/public/link"); err == nil {
		t.Fatal("symlink created")
	}
	if _, err := c.Create("/other.example/x"); err == nil {
		t.Fatal("wrote to a website that is not listed")
	}
	if _, err := c.Create("/x.txt"); err == nil {
		t.Fatal("wrote to the root")
	}
	if err := c.RemoveDirectory("/blog.example"); err == nil {
		t.Fatal("removed a website's directory")
	}
	if err := c.Rename("/blog.example/public/index.html", "/shop.example/public/index.html"); err == nil {
		t.Fatal("moved a file across websites")
	}

	// Read-only website.
	if _, err := c.ReadDir("/shop.example/public"); err != nil {
		t.Fatalf("read-only list: %v", err)
	}
	if _, err := c.Create("/shop.example/public/x"); !errors.Is(err, os.ErrPermission) && err == nil {
		t.Fatal("wrote to a read-only website")
	}
}
