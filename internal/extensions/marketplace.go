package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

// CatalogEntry is one extension offered by the marketplace.
type CatalogEntry struct {
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	Name        Text      `json:"name"`
	Description Text      `json:"description"`
	Publisher   Publisher `json:"publisher"`
	Homepage    string    `json:"homepage,omitempty"`
	Icon        string    `json:"icon,omitempty"` // https URL
	Permissions []string  `json:"permissions,omitempty"`
	Price       *Price    `json:"price,omitempty"` // nil = free
	BuyURL      string    `json:"buy_url,omitempty"`
	Download    string    `json:"download"` // https URL of the .opx
}

// Price of a paid extension (one-time payment).
type Price struct {
	Amount   string `json:"amount"`   // "29.00"
	Currency string `json:"currency"` // "EUR"
}

const catalogTTL = 10 * time.Minute

// ErrMarketplace: the marketplace could not be reached or answered badly.
var ErrMarketplace = errors.New("the extension marketplace is unavailable right now")

// Catalog returns the marketplace's extensions for this server (cached).
func (m *Manager) Catalog(ctx context.Context) ([]CatalogEntry, error) {
	m.mu.Lock()
	if m.catalog != nil && time.Since(m.catalogT) < catalogTTL {
		c := m.catalog
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()
	base := strings.TrimRight(m.cfg.MarketplaceURL, "/")
	if base == "" {
		return nil, errors.New("no marketplace is configured (OPENPLEX_MARKETPLACE_URL)")
	}
	q := url.Values{"openplex": {m.version}, "arch": {runtime.GOARCH}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/extensions?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := m.client.Do(req)
	if err != nil {
		m.log.Warn("marketplace", "err", err)
		return nil, ErrMarketplace
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		m.log.Warn("marketplace", "status", res.Status)
		return nil, ErrMarketplace
	}
	var out struct {
		Extensions []CatalogEntry `json:"extensions"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&out); err != nil {
		return nil, ErrMarketplace
	}
	list := out.Extensions[:0]
	for _, e := range out.Extensions {
		if idRe.MatchString(e.ID) && strings.HasPrefix(e.Download, "https://") {
			list = append(list, e)
		}
	}
	m.mu.Lock()
	m.catalog, m.catalogT = list, time.Now()
	m.mu.Unlock()
	return list, nil
}

// Download fetches a marketplace extension for review. Marketplace packages
// must be signed with a key this build trusts.
func (m *Manager) Download(ctx context.Context, id, license string) (*Pending, error) {
	cat, err := m.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	var entry *CatalogEntry
	for i := range cat {
		if cat[i].ID == id {
			entry = &cat[i]
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("the marketplace has no extension %q", id)
	}
	// Updating a paid extension: the licence it was installed with.
	if strings.TrimSpace(license) == "" {
		if e, err := m.db.GetExtension(ctx, id); err == nil {
			license = m.License(e)
		}
	}
	if entry.Price != nil && strings.TrimSpace(license) == "" {
		return nil, errors.New("this extension is paid: enter the licence key you received when buying it")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.Download, nil)
	if err != nil {
		return nil, err
	}
	if license != "" {
		req.Header.Set("X-License-Key", strings.TrimSpace(license))
	}
	res, err := m.client.Do(req)
	if err != nil {
		return nil, ErrMarketplace
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusPaymentRequired || res.StatusCode == http.StatusForbidden:
		return nil, errors.New("the marketplace did not accept this licence key")
	case res.StatusCode != http.StatusOK:
		return nil, ErrMarketplace
	}
	p, err := m.Receive(ctx, res.Body, "marketplace", strings.TrimSpace(license))
	if err != nil {
		return nil, err
	}
	if p.Signer == "" || p.Manifest.ID != id {
		m.discard(p)
		return nil, errors.New("the downloaded package is not signed by OpenPlex; it was not installed")
	}
	return p, nil
}

// Discard drops a received package that will not be installed.
func (m *Manager) Discard(token string) {
	m.mu.Lock()
	p := m.pending[token]
	m.mu.Unlock()
	if p != nil {
		m.discard(p)
	}
}

func (m *Manager) discard(p *Pending) {
	m.mu.Lock()
	delete(m.pending, p.Token)
	m.mu.Unlock()
	_ = os.RemoveAll(p.dir)
}
