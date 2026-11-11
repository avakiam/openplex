// Hello: a minimal OpenPlex extension. It shows a page (menu), a website tab,
// reads its settings and records the events it receives.
//
// The panel talks to it over HTTP on $PORT; it talks to the panel over the
// unix socket $OPENPLEX_SOCKET (/run/openplex/panel.sock).
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// panel is an HTTP client for the OpenPlex API over the extension's socket.
var panel = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", os.Getenv("OPENPLEX_SOCKET"))
	}},
}

// call does an API request; user is the panel user to act as ("" = the
// extension itself, with the permissions it was installed with).
func call(method, path, user string, out any) error {
	req, err := http.NewRequest(method, "http://openplex"+path, nil)
	if err != nil {
		return err
	}
	if user != "" {
		req.Header.Set("X-OpenPlex-User", user)
	}
	res, err := panel.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// self is GET /api/v1/extension: our settings and the token the panel sends us.
type self struct {
	Token    string            `json:"token"`
	Settings map[string]string `json:"settings"`
}

var (
	mu     sync.Mutex
	me     self
	events []string
)

func refresh() error {
	var s self
	if err := call("GET", "/api/v1/extension", "", &s); err != nil {
		return err
	}
	mu.Lock()
	me = s
	mu.Unlock()
	return nil
}

// fromPanel: every request must carry our token (only OpenPlex has it).
func fromPanel(r *http.Request) bool {
	mu.Lock()
	tok := me.Token
	mu.Unlock()
	return tok != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-OpenPlex-Token")), []byte(tok)) == 1
}

var texts = map[string]map[string]string{
	"es": {"Websites you can see": "Webs que puedes ver", "Recent events": "Eventos recientes", "DNS records of": "Registros DNS de", "None yet.": "Ninguno todavía."},
	"ca": {"Websites you can see": "Webs que pots veure", "Recent events": "Esdeveniments recents", "DNS records of": "Registres DNS de", "None yet.": "Encara cap."},
}

var page = template.Must(template.New("p").Funcs(template.FuncMap{
	"t": func(lang, s string) string {
		if v := texts[lang][s]; v != "" {
			return v
		}
		return s
	},
}).Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<style>
 body{font:14px/1.5 system-ui,sans-serif;margin:24px;color:#111;background:#fff}
 body.dark{color:#eee;background:#0b0b0c} h1{font-size:20px} li{margin:2px 0} code{font-size:12px}
</style></head>
<body class="{{.Theme}}">
<h1>{{.Greeting}}, {{.User}}!</h1>
{{if .Domain}}
 <h2>{{t .Lang "DNS records of"}} {{.Domain}}</h2>
 <ul>{{range .Records}}<li><code>{{.Name}} {{.Type}} {{.Content}}</code></li>{{else}}<li>{{t .Lang "None yet."}}</li>{{end}}</ul>
{{else}}
 <h2>{{t .Lang "Websites you can see"}}</h2>
 <ul>{{range .Domains}}<li>{{.Name}}</li>{{else}}<li>{{t .Lang "None yet."}}</li>{{end}}</ul>
 <h2>{{t .Lang "Recent events"}}</h2>
 <ul>{{range .Events}}<li><code>{{.}}</code></li>{{else}}<li>{{t .Lang "None yet."}}</li>{{end}}</ul>
{{end}}
</body></html>`))

type record struct{ Name, Type, Content string }

func view(w http.ResponseWriter, r *http.Request) {
	if !fromPanel(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	user := r.Header.Get("X-OpenPlex-User") // act as the signed-in user
	mu.Lock()
	data := map[string]any{
		"Greeting": me.Settings["greeting"], "User": r.Header.Get("X-OpenPlex-Username"),
		"Lang": r.Header.Get("X-OpenPlex-Lang"), "Theme": r.URL.Query().Get("theme"),
		"Events": append([]string(nil), events...),
	}
	mu.Unlock()
	if r.URL.Path == "/site" {
		id := r.Header.Get("X-OpenPlex-Domain")
		var zone struct {
			Zone *struct {
				Name string `json:"name"`
			} `json:"zone"`
			Records []record `json:"records"`
		}
		if err := call("GET", "/api/v1/domains/"+id+"/dns", user, &zone); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		data["Domain"], data["Records"] = id, zone.Records
		if zone.Zone != nil {
			data["Domain"] = zone.Zone.Name
		}
	} else {
		var domains []struct{ Name string }
		if err := call("GET", "/api/v1/domains", user, &domains); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		data["Domains"] = domains
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, data)
}

func event(w http.ResponseWriter, r *http.Request) {
	if !fromPanel(r) || r.Method != http.MethodPost {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var ev struct {
		Type string         `json:"type"`
		Time time.Time      `json:"time"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	if ev.Type == "settings.changed" {
		_ = refresh()
	}
	line := fmt.Sprintf("%s %s %v", ev.Time.Format(time.RFC3339), ev.Type, ev.Data)
	log.Println("event:", line)
	mu.Lock()
	events = append([]string{line}, events...)
	if len(events) > 20 {
		events = events[:20]
	}
	mu.Unlock()
	// /data survives restarts and upgrades.
	if f, err := os.OpenFile(filepath.Join(os.Getenv("OPENPLEX_DATA"), "events.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.WriteString(strings.TrimSpace(line) + "\n")
		f.Close()
	}
	w.WriteHeader(http.StatusNoContent)
}

func main() {
	for refresh() != nil { // the panel socket may not be up yet
		time.Sleep(2 * time.Second)
	}
	http.HandleFunc("/openplex/events", event)
	http.HandleFunc("/", view)
	log.Println("hello extension listening on :" + os.Getenv("PORT"))
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}
