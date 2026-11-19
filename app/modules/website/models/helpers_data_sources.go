package models

// File overview:
// - Responsibility: public, read-only data sources that builder blocks can show
//   (`data-bh-source="<key>"`). A module registers a Go function per key; visitors can only pick a
//   registered key, never a model, so nothing is readable that a module did not choose to publish.
// - Key symbols: `RegisterWebsiteDataSource`, `websiteDataSource`.

import (
	"context"
	"encoding/json"
	"regexp"
	"sync"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// WebsiteDataItem is the one shape every source returns; the behavior renders cards from it.
type WebsiteDataItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary,omitempty"`
	Image   string `json:"image,omitempty"`
	Date    string `json:"date,omitempty"`
}

// WebsiteDataSourceFunc lists up to `limit` items for one site, newest first.
type WebsiteDataSourceFunc func(ctx context.Context, siteID int64, limit int) ([]WebsiteDataItem, error)

var (
	dataSourceKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	dataSourcesMu   sync.RWMutex
	dataSources     = map[string]WebsiteDataSourceFunc{}
)

const (
	dataSourceDefaultLimit = 6
	dataSourceMaxLimit     = 24
)

// RegisterWebsiteDataSource publishes a source under `key`. Registering a key again replaces it (Bootstrap may run more
// than once); a bad key or nil function is returned as an error so Bootstrap can fail loudly.
func RegisterWebsiteDataSource(key string, fn WebsiteDataSourceFunc) error {
	if !dataSourceKeyRe.MatchString(key) || fn == nil {
		return errInvalidDataSource
	}
	dataSourcesMu.Lock()
	defer dataSourcesMu.Unlock()
	dataSources[key] = fn
	return nil
}

type dataSourceError struct{}

func (*dataSourceError) Error() string {
	return "website data source: key must match [a-z0-9_-], function required"
}

var errInvalidDataSource = &dataSourceError{}

func clampDataSourceLimit(raw any) int {
	n, ok := int64FromAny(raw)
	if !ok || n <= 0 {
		return dataSourceDefaultLimit
	}
	if n > dataSourceMaxLimit {
		return dataSourceMaxLimit
	}
	return int(n)
}

// websiteDataSource is the model-level `data_source` RuntimeMethod behind GET /website/runtime/data.
// Anonymous by design: it serves public site content, resolved from the Host like the contact form.
func websiteDataSource(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	request, ok := orm.AliasRequestFromKwargs(kwargs)
	if !ok {
		return nil, nil
	}
	if reply, closed := websiteAnonymousRouteClosed(ctx); closed {
		return reply, nil
	}
	key := request.Params["source"]
	dataSourcesMu.RLock()
	fn := dataSources[key]
	dataSourcesMu.RUnlock()
	if !dataSourceKeyRe.MatchString(key) || fn == nil {
		return contactReply(404, false, "unknown source"), nil
	}
	items, err := fn(ctx, contactSiteIDForHost(ctx, request.Headers["host"]), clampDataSourceLimit(request.Params["limit"]))
	if err != nil {
		log.Errorf("website: data source %s: %v", key, err)
		return contactReply(500, false, "could not load the data"), nil
	}
	if items == nil {
		items = []WebsiteDataItem{}
	}
	body, _ := json.Marshal(map[string]any{"ok": true, "items": items})
	return orm.AliasResponse{Status: 200, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "public, max-age=60"}, Body: string(body)}, nil
}
