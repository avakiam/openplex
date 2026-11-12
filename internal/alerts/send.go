package alerts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"slices"
	"strconv"
	"strings"
	"time"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/msg"
)

// Kinds of alerts, in display order.
const (
	KindBackupFailed    = "backup_failed"
	KindSiteDown        = "site_down"
	KindDiskFull        = "disk_full"
	KindDatabaseFull    = "database_full"
	KindCertExpiring    = "cert_expiring"
	KindUpdateAvailable = "update_available"
	KindUpdateFailed    = "update_failed"
)

// Kinds lists every alert kind.
var Kinds = []string{KindBackupFailed, KindSiteDown, KindDiskFull, KindDatabaseFull, KindCertExpiring, KindUpdateAvailable, KindUpdateFailed}

// Languages the e-mails are written in.
var Languages = []string{"en", "es", "ca"}

var hostRe = func(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if l == "" || len(l) > 63 || strings.Trim(l, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
			return false
		}
	}
	return true
}

func validAddress(a string) bool {
	p, err := mail.ParseAddress(a)
	return err == nil && p.Address == a && len(a) <= 254 && !strings.ContainsAny(a, "\r\n,")
}

// Validate checks and normalizes settings.
func Validate(s *db.AlertSettings) error {
	var rcpt []string
	for _, r := range s.Recipients {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !validAddress(r) {
			return msg.New("{address} is not a valid e-mail address", "address", r)
		}
		if !slices.Contains(rcpt, r) {
			rcpt = append(rcpt, r)
		}
	}
	if len(rcpt) > 10 {
		return errors.New("at most 10 recipients")
	}
	s.Recipients = rcpt
	if s.Enabled && len(rcpt) == 0 {
		return errors.New("add at least one recipient to turn alerts on")
	}
	if !slices.Contains(Languages, s.Language) {
		s.Language = "en"
	}
	var ev []string
	for _, k := range s.Events {
		if !slices.Contains(Kinds, k) {
			return fmt.Errorf("unknown alert %q", k)
		}
		if !slices.Contains(ev, k) {
			ev = append(ev, k)
		}
	}
	s.Events = ev
	s.FromAddress = strings.TrimSpace(s.FromAddress)
	if s.FromAddress != "" && !validAddress(s.FromAddress) {
		return errors.New("the sender address is not a valid e-mail address")
	}
	switch s.Transport {
	case "local":
	case "smtp":
		s.SMTPHost = strings.TrimSpace(strings.ToLower(s.SMTPHost))
		if !hostRe(s.SMTPHost) {
			return errors.New("the SMTP server must be a host name such as smtp.example.com")
		}
		if s.SMTPPort < 1 || s.SMTPPort > 65535 {
			return errors.New("the SMTP port must be between 1 and 65535")
		}
		if s.SMTPSecurity != "starttls" && s.SMTPSecurity != "tls" && s.SMTPSecurity != "none" {
			return errors.New("the SMTP security must be starttls, tls or none")
		}
		if len(s.SMTPUser) > 254 || strings.ContainsAny(s.SMTPUser, "\r\n") {
			return errors.New("the SMTP user is not valid")
		}
	default:
		return errors.New("transport must be local or smtp")
	}
	return nil
}

// message builds a plain-text e-mail.
func message(from string, to []string, subject, body string) []byte {
	subject = strings.Join(strings.Fields(subject), " ") // never a line break in a header
	host := "openplex"
	if _, h, ok := strings.Cut(from, "@"); ok {
		host = h
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", (&mail.Address{Name: "OpenPlex", Address: from}).String())
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), host)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\nX-Auto-Response-Suppress: All\r\n\r\n")
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n")))
	_ = w.Close()
	return b.Bytes()
}

// sendSMTP delivers msg through an SMTP server.
// helo is the name the panel introduces itself with (servers reject "localhost").
func sendSMTP(ctx context.Context, s *db.AlertSettings, helo, password, from string, to []string, msg []byte) error {
	addr := net.JoinHostPort(s.SMTPHost, strconv.Itoa(s.SMTPPort))
	d := net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if s.SMTPSecurity == "tls" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: s.SMTPHost, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	c, err := smtp.NewClient(conn, s.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	defer c.Close()
	if err := c.Hello(helo); err != nil {
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	if s.SMTPSecurity == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server does not offer STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if s.SMTPUser != "" {
		if err := c.Auth(smtp.PlainAuth("", s.SMTPUser, password, s.SMTPHost)); err != nil {
			return fmt.Errorf("SMTP sign-in: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP sender: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("SMTP recipient %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP message: %w", err)
	}
	return c.Quit()
}
