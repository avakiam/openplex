package ssl

import (
	"strings"
	"testing"
)

func TestRenderWebmail(t *testing.T) {
	out, err := Render(Config{MailHost: "mail.example.com", WebmailUpstream: "openplex-webmail:80", WebmailHosts: []string{"webmail.b.com", "webmail.a.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mail.example.com, webmail.a.com, webmail.b.com {") || !strings.Contains(out, "reverse_proxy openplex-webmail:80") || strings.Contains(out, "This is the mail server") {
		t.Errorf("webmail block:\n%s", out)
	}
	if _, err := Render(Config{WebmailUpstream: "openplex-webmail:80", WebmailHosts: []string{"x.com {\n}"}}); err == nil {
		t.Error("bad webmail host rendered")
	}
}
