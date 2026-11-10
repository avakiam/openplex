package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// pdns is a minimal PowerDNS Authoritative HTTP API client.
type pdns struct {
	base string
	key  string
	http *http.Client
}

type apiRecord struct {
	Content  string `json:"content"`
	Disabled bool   `json:"disabled"`
}

type apiRRSet struct {
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	TTL        int         `json:"ttl,omitempty"`
	ChangeType string      `json:"changetype,omitempty"`
	Records    []apiRecord `json:"records"`
}

type apiZone struct {
	ID         string     `json:"id,omitempty"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind,omitempty"`
	SOAEditAPI string     `json:"soa_edit_api,omitempty"`
	RRSets     []apiRRSet `json:"rrsets,omitempty"`
	Serial     uint32     `json:"serial,omitempty"`
}

func (p *pdns) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, method, p.base+"/api/v1/servers/localhost"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", p.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return fmt.Errorf("powerdns %s %s: %d %s", method, path, res.StatusCode, e.Error)
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

func zonePath(name string) string { return "/zones/" + url.PathEscape(name+".") }

func (p *pdns) ping(ctx context.Context) error {
	return p.do(ctx, http.MethodGet, "", nil, nil)
}

func (p *pdns) listZones(ctx context.Context) ([]string, error) {
	var zs []apiZone
	if err := p.do(ctx, http.MethodGet, "/zones", nil, &zs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(zs))
	for _, z := range zs {
		out = append(out, strings.TrimSuffix(z.Name, "."))
	}
	return out, nil
}

func (p *pdns) getZone(ctx context.Context, name string) (*apiZone, error) {
	var z apiZone
	if err := p.do(ctx, http.MethodGet, zonePath(name), nil, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

func (p *pdns) createZone(ctx context.Context, name string, sets []RRSet, soa string) error {
	z := apiZone{Name: name + ".", Kind: "Native", SOAEditAPI: "DEFAULT"}
	z.RRSets = append(z.RRSets, apiRRSet{Name: name + ".", Type: "SOA", TTL: DefaultTTL, Records: []apiRecord{{Content: soa}}})
	for _, s := range sets {
		z.RRSets = append(z.RRSets, toAPI(s, ""))
	}
	return p.do(ctx, http.MethodPost, "/zones", z, nil)
}

func (p *pdns) deleteZone(ctx context.Context, name string) error {
	return p.do(ctx, http.MethodDelete, zonePath(name), nil, nil)
}

func (p *pdns) patch(ctx context.Context, name string, changes []apiRRSet) error {
	if len(changes) == 0 {
		return nil
	}
	return p.do(ctx, http.MethodPatch, zonePath(name), apiZone{RRSets: changes}, nil)
}

func toAPI(s RRSet, change string) apiRRSet {
	a := apiRRSet{Name: s.Name, Type: s.Type, TTL: s.TTL, ChangeType: change, Records: []apiRecord{}}
	for _, c := range s.Records {
		a.Records = append(a.Records, apiRecord{Content: c})
	}
	return a
}

// diff computes the PATCH that turns current into desired. SOA is compared
// without its serial and replaced with serial+1 only when it changed.
func diff(zone string, current []apiRRSet, desired []RRSet, soa string) []apiRRSet {
	cur := map[string]RRSet{}
	var curSOA string
	for _, a := range current {
		if a.Type == "SOA" {
			if len(a.Records) > 0 {
				curSOA = a.Records[0].Content
			}
			continue
		}
		s := RRSet{Name: a.Name, Type: a.Type, TTL: a.TTL}
		for _, r := range a.Records {
			if !r.Disabled {
				s.Records = append(s.Records, r.Content)
			}
		}
		slices.Sort(s.Records)
		cur[s.key()] = s
	}
	var out []apiRRSet
	for _, d := range desired {
		c, ok := cur[d.key()]
		delete(cur, d.key())
		if ok && c.TTL == d.TTL && slices.Equal(c.Records, d.Records) {
			continue
		}
		out = append(out, toAPI(d, "REPLACE"))
	}
	for _, c := range cur {
		out = append(out, apiRRSet{Name: c.Name, Type: c.Type, ChangeType: "DELETE", Records: []apiRecord{}})
	}
	if soaWithoutSerial(curSOA) != soaWithoutSerial(soa) {
		serial := uint32(1)
		if f := strings.Fields(curSOA); len(f) == 7 {
			if n, err := strconv.ParseUint(f[2], 10, 32); err == nil {
				serial = uint32(n) + 1
			}
		}
		f := strings.Fields(soa)
		f[2] = strconv.FormatUint(uint64(serial), 10)
		out = append(out, apiRRSet{Name: zone + ".", Type: "SOA", ChangeType: "REPLACE", TTL: DefaultTTL, Records: []apiRecord{{Content: strings.Join(f, " ")}}})
	}
	slices.SortFunc(out, func(a, b apiRRSet) int { return strings.Compare(a.Name+a.Type, b.Name+b.Type) })
	return out
}

func soaWithoutSerial(s string) string {
	f := strings.Fields(s)
	if len(f) != 7 {
		return s
	}
	f[2] = ""
	return strings.Join(f, " ")
}
