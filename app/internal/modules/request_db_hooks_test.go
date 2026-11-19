package modules

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
)

func TestCheckRequestDBName(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
		RegisterRequestDBValidator(nil)
	})
	config.Update(func(c *config.AppConfig) {
		c.PostgresqlDb.HomeDBName = "home_db"
		c.PostgresqlDb.TemplateDBName = "tpl_db"
	})

	stub := func(_ context.Context, name string) (bool, error) {
		switch name {
		case "tenant_running":
			return true, nil
		case "tenant_broken":
			return false, errors.New("relation base_database does not exist")
		}
		return false, nil
	}

	cases := []struct {
		name      string
		validator RequestDBValidator
		db        string
		want      error
	}{
		{"home without validator", nil, "home_db", nil},
		{"template without validator", nil, "tpl_db", ErrRequestDBNotAllowed},
		{"other name without validator keeps old behaviour", nil, "anything", nil},
		{"home skips validator", stub, "home_db", nil},
		{"template rejected before validator", func(context.Context, string) (bool, error) { return true, nil }, "tpl_db", ErrRequestDBNotAllowed},
		{"registered running tenant", stub, "tenant_running", nil},
		{"unregistered or stopped tenant", stub, "tenant_stopped", ErrRequestDBNotAllowed},
		{"lookup error fails closed", stub, "tenant_broken", ErrRequestDBUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			RegisterRequestDBValidator(tc.validator)
			err := CheckRequestDBName(context.Background(), tc.db)
			if tc.want == nil && err != nil {
				t.Fatalf("CheckRequestDBName(%q) = %v, want nil", tc.db, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("CheckRequestDBName(%q) = %v, want %v", tc.db, err, tc.want)
			}
		})
	}
}
