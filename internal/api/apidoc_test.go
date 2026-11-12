package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The API reference (docs/en/api.md) lists every endpoint as `METHOD /path`;
// this keeps it and the router in step.
func TestAPIReferenceListsEveryRoute(t *testing.T) {
	h := newHarness(t)
	routes := map[string]bool{}
	err := chi.Walk(h.srv.routes().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path, ok := strings.CutPrefix(route, "/api/v1")
		if !ok || strings.HasSuffix(path, "/*") {
			return nil
		}
		if len(path) > 1 {
			path = strings.TrimSuffix(path, "/")
		}
		routes[method+" "+path] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../docs/en/api.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `(GET|POST|PUT|PATCH|DELETE) (/[^`]*)`").FindAllStringSubmatch(string(b), -1) {
		documented[m[1]+" "+m[2]] = true
	}
	var missing, stale []string
	for r := range routes {
		if !documented[r] {
			missing = append(missing, r)
		}
	}
	for d := range documented {
		if !routes[d] {
			stale = append(stale, d)
		}
	}
	slices.Sort(missing)
	slices.Sort(stale)
	if len(missing) > 0 {
		t.Errorf("endpoints missing from docs/en/api.md:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("docs/en/api.md lists endpoints that don't exist:\n  %s", strings.Join(stale, "\n  "))
	}
}

// The field tables of docs/en/api.md ("Provisioning fields") name JSON
// fields: each must exist in the API or its models.
func TestAPIReferenceFieldsExist(t *testing.T) {
	b, err := os.ReadFile("../../docs/en/api.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	start, end := strings.Index(doc, "## Provisioning fields"), strings.Index(doc, "## Endpoints")
	if start < 0 || end < start {
		t.Fatal("docs/en/api.md has no Provisioning fields section before Endpoints")
	}
	var src strings.Builder
	for _, dir := range []string{".", "../db"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			c, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src.Write(c)
		}
	}
	code := src.String()
	cell := regexp.MustCompile("`([a-z_]+)`")
	n := 0
	for _, line := range strings.Split(doc[start:end], "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		first := strings.SplitN(line[1:], "|", 2)[0]
		for _, m := range cell.FindAllStringSubmatch(first, -1) {
			f := m[1]
			n++
			if !strings.Contains(code, `json:"`+f+`"`) && !strings.Contains(code, `json:"`+f+`,`) && !strings.Contains(code, `"`+f+`":`) {
				t.Errorf("docs/en/api.md documents field %q, which no API struct or answer has", f)
			}
		}
	}
	if n < 30 {
		t.Fatalf("only %d documented fields found: has the table format changed?", n)
	}
}
