package alerts

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/update"
)

type outbox struct{ subjects []string }

func (o *outbox) send(_ context.Context, from string, to []string, msg []byte) error {
	for _, line := range strings.Split(string(msg), "\r\n") {
		if s, ok := strings.CutPrefix(line, "Subject: "); ok {
			o.subjects = append(o.subjects, s)
		}
	}
	return nil
}

func (o *outbox) take() []string {
	s := o.subjects
	o.subjects = nil
	return s
}

func setup(t *testing.T) (*Manager, *db.DB, *outbox, *[2]uint64, *update.Status) {
	t.Helper()
	ctx := context.Background()
	store := db.OpenMemory(t)
	st, err := store.GetAlertSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Events) != len(Kinds) {
		t.Fatalf("default events = %v", st.Events)
	}
	st.Enabled, st.Recipients = true, []string{"admin@example.com"}
	if err := store.SaveAlertSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	box := &outbox{}
	disk := &[2]uint64{50, 100}
	upd := &update.Status{Current: "0.20.0"}
	m := New(Options{
		DB: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Server: "panel.example.com", URL: "https://panel.example.com",
		CertDir:   t.TempDir(),
		Disk:      func() (uint64, uint64, bool) { return disk[0], disk[1], true },
		Update:    func(context.Context) *update.Status { return upd },
		LocalSend: box.send,
	})
	return m, store, box, disk, upd
}

func TestSiteDownAndRecovery(t *testing.T) {
	ctx := context.Background()
	m, store, box, _, _ := setup(t)
	u := &db.User{Username: "ana", PasswordHash: "x", Role: db.RoleCustomer}
	_ = store.CreateUser(ctx, u)
	d := &db.Domain{OwnerID: u.ID, Name: "shop.example", Kind: db.KindPrimary, Runtime: "php", DocumentRoot: "public"}
	if err := store.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	_ = store.SetDomainStatus(ctx, d.ID, db.StatusError, "the container keeps crashing", "")

	if err := m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := box.take(); len(got) != 0 {
		t.Fatalf("alerted before %s: %v", siteDownAfter, got)
	}
	m.pending["site:shop.example"] = time.Now().Add(-3 * time.Minute)
	_ = m.Check(ctx)
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "Website down: shop.example") {
		t.Fatalf("down alert: %v", got)
	}
	// A day later it is reminded.
	a, _ := store.GetAlert(ctx, "site:shop.example")
	old := time.Now().Add(-25 * time.Hour)
	a.LastSentAt = &old
	_ = store.PutAlert(ctx, a)
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "Still happening") {
		t.Fatalf("reminder: %v", got)
	}
	_ = store.SetDomainStatus(ctx, d.ID, db.StatusRunning, "", "")
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "Website back up: shop.example") {
		t.Fatalf("recovery: %v", got)
	}
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 0 {
		t.Fatalf("sent again: %v", got)
	}
}

func TestDiskBackupsAndUpdates(t *testing.T) {
	ctx := context.Background()
	m, store, box, disk, upd := setup(t)

	disk[0] = 95
	_ = m.Check(ctx)
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "Disk almost full on panel.example.com: 95%") {
		t.Fatalf("disk: %v", got)
	}
	disk[0] = 88 // between the thresholds: still firing, nothing new
	_ = m.Check(ctx)
	disk[0] = 70
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "back to normal") {
		t.Fatalf("disk recovery: %v", got)
	}

	r := &db.BackupRun{Kind: "backup", Trigger: "scheduled", Scope: "all"}
	_ = store.CreateBackupRun(ctx, r)
	_ = store.FinishBackupRun(ctx, r.ID, "error", "repository unreachable", 0)
	_ = m.Check(ctx)
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "Backup failed") {
		t.Fatalf("backup: %v", got)
	}

	upd.Available, upd.Latest = true, &update.Release{Version: "0.21.0", Notes: "notes"}
	upd.Last = &update.Outcome{From: "0.20.0", To: "0.20.9", Result: "rolled_back", Message: "did not start", Time: time.Now()}
	_ = m.Check(ctx)
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 2 {
		t.Fatalf("updates: %v", got)
	}

	// Turned off: nothing is checked or sent.
	st, _ := store.GetAlertSettings(ctx)
	st.Enabled = false
	_ = store.SaveAlertSettings(ctx, st)
	disk[0] = 99
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 0 {
		t.Fatalf("sent while off: %v", got)
	}
	if recent, _ := store.RecentAlerts(ctx, 10); len(recent) != 4 {
		t.Fatalf("history has %d alerts", len(recent))
	}
}

func writeCert(t *testing.T, dir, host string, notBefore, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: notBefore, NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "acme", host, host+".crt")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCertificates(t *testing.T) {
	ctx := context.Background()
	m, store, box, _, _ := setup(t)
	u := &db.User{Username: "ana", PasswordHash: "x", Role: db.RoleCustomer}
	_ = store.CreateUser(ctx, u)
	_ = store.CreateDomain(ctx, &db.Domain{OwnerID: u.ID, Name: "shop.example", Kind: db.KindPrimary, Runtime: "static", DocumentRoot: "public"})
	now := time.Now()
	writeCert(t, m.o.CertDir, "shop.example", now.Add(-85*24*time.Hour), now.Add(5*24*time.Hour))      // stuck: 5 of 90 days left
	writeCert(t, m.o.CertDir, "www.shop.example", now.Add(-10*24*time.Hour), now.Add(80*24*time.Hour)) // fine
	writeCert(t, m.o.CertDir, "gone.example", now.Add(-89*24*time.Hour), now.Add(24*time.Hour))        // not served any more
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "Certificate not renewing: shop.example") {
		t.Fatalf("certs: %v", got)
	}
	writeCert(t, m.o.CertDir+"/other-issuer", "shop.example", now, now.Add(90*24*time.Hour)) // renewed
	_ = m.Check(ctx)
	if got := box.take(); len(got) != 1 || !strings.Contains(got[0], "Certificate renewed") {
		t.Fatalf("renewal: %v", got)
	}
}

func TestValidateAndMessage(t *testing.T) {
	s := &db.AlertSettings{Enabled: true, Recipients: []string{" a@example.com", "a@example.com", ""}, Language: "fr", Transport: "local", Events: []string{KindDiskFull}}
	if err := Validate(s); err != nil || len(s.Recipients) != 1 || s.Language != "en" {
		t.Fatalf("validate: %v %+v", err, s)
	}
	for _, bad := range []*db.AlertSettings{
		{Enabled: true, Transport: "local"},
		{Recipients: []string{"x@example.com\r\nBcc: y@example.com"}, Transport: "local"},
		{Transport: "smtp", SMTPHost: "smtp example", SMTPPort: 587, SMTPSecurity: "starttls"},
		{Transport: "smtp", SMTPHost: "smtp.example.com", SMTPPort: 0, SMTPSecurity: "starttls"},
		{Transport: "local", Events: []string{"nope"}},
		{Transport: "local", FromAddress: "OpenPlex <x@example.com>"},
	} {
		if err := Validate(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	msg := string(message("openplex@panel.example.com", []string{"a@example.com"}, "Website down: x\r\nBcc: evil@example.com", "body ñ"))
	if strings.Contains(msg, "\r\nBcc:") || !strings.Contains(msg, "Subject: Website down: x Bcc: evil@example.com\r\n") {
		t.Fatalf("header injection:\n%s", msg)
	}
	if !strings.Contains(msg, "body =C3=B1") {
		t.Fatalf("body not quoted-printable:\n%s", msg)
	}
	m := New(Options{Server: "panel.example.com"})
	if err := m.Test(context.Background(), &db.AlertSettings{Recipients: []string{"a@example.com"}, Transport: "local"}); err == nil {
		t.Fatal("test without a mail service did not fail")
	}
}
