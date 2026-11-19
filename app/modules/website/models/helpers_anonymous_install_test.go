package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// Plan T36 / SEC-D-20: the website anonymous aliases answer 404 on a DB without `website`
// (or when the catalog cannot be read) before parsing the body or touching their tables.
func TestWebsiteAnonymousRoutesClosedWhenNotInstalled(t *testing.T) {
	restore := websiteInstalledInRequestDB
	t.Cleanup(func() { websiteInstalledInRequestDB = restore })

	handlers := map[string]orm.RuntimeMethodFunc{
		"data_source":    websiteDataSource,
		"contact submit": submitWebsiteContactRequest,
		"form submit":    submitWebsiteForm,
	}
	request := &orm.AliasRequest{Body: []byte(`{}`), Params: map[string]string{"source": "x", "limit": "1"}}
	kwargs := map[string]any{orm.AliasRequestKwarg: request}

	for _, check := range []struct {
		name string
		fn   func(context.Context) (bool, error)
	}{
		{"not installed", func(context.Context) (bool, error) { return false, nil }},
		{"catalog error", func(context.Context) (bool, error) { return false, errors.New("boom") }},
	} {
		websiteInstalledInRequestDB = check.fn
		for name, handler := range handlers {
			got, err := handler(context.Background(), nil, nil, nil, kwargs)
			if err != nil {
				t.Fatalf("%s/%s: unexpected error %v", check.name, name, err)
			}
			response, ok := got.(orm.AliasResponse)
			if !ok || response.Status != 404 {
				t.Fatalf("%s/%s: want 404 AliasResponse, got %#v", check.name, name, got)
			}
		}
	}

	// Installed: the gate lets the handler run its own validation (here: the empty body is
	// rejected as a bad request, the unknown source as 404 "unknown source").
	websiteInstalledInRequestDB = func(context.Context) (bool, error) { return true, nil }
	got, err := submitWebsiteContactRequest(context.Background(), nil, nil, nil, kwargs)
	if err != nil {
		t.Fatal(err)
	}
	if response := got.(orm.AliasResponse); response.Status != 400 {
		t.Fatalf("installed: want the handler's own 400, got %d", response.Status)
	}
}
