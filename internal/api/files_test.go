package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/textproto"
	"testing"
)

func TestUploadPath(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"index.html", "index.html"},
		{"site/css/main.css", "site/css/main.css"},
		{`site\img\logo.png`, "site/img/logo.png"},
		{"/abs/./x.txt", "abs/x.txt"},
		{"../etc/passwd", ""},
		{"a/../../b", ""},
		{"", ""},
	} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="files"; filename="`+tc.name+`"`)
		pw, _ := mw.CreatePart(h)
		_, _ = pw.Write([]byte("x"))
		_ = mw.Close()
		part, err := multipart.NewReader(&buf, mw.Boundary()).NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if got := uploadPath(part); got != tc.want {
			t.Errorf("uploadPath(%q) = %q, want %q", tc.name, got, tc.want)
		}
		_, _ = io.Copy(io.Discard, part)
	}
}
