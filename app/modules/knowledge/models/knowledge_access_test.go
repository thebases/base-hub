package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestEnsureKnowledgeWriteAllowed(t *testing.T) {
	previous := knowledgeCallerGroupBIDs
	t.Cleanup(func() { knowledgeCallerGroupBIDs = previous })

	userCtx := func(sudo bool) context.Context {
		env := orm.NewEnvironmentWithDBName(nil, "tenant", 7, map[string]any{}, sudo)
		return orm.ContextWithEnvironment(context.Background(), env)
	}
	lookupErr := errors.New("auth db down")

	cases := []struct {
		name    string
		ctx     context.Context
		groups  []string
		err     error
		allowed bool
	}{
		{name: "no environment", ctx: context.Background(), allowed: false},
		{name: "sudo", ctx: userCtx(true), allowed: true},
		{name: "group_system", ctx: userCtx(false), groups: []string{"base.group_user", KnowledgeWriterGroupBID}, allowed: true},
		{name: "plain user", ctx: userCtx(false), groups: []string{"base.group_user"}, allowed: false},
		{name: "group lookup error", ctx: userCtx(false), err: lookupErr, allowed: false},
	}
	for _, tc := range cases {
		knowledgeCallerGroupBIDs = func(context.Context) ([]string, error) { return tc.groups, tc.err }
		err := EnsureKnowledgeWriteAllowed(tc.ctx)
		if tc.allowed {
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: write allowed, want refused", tc.name)
		}
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Fatalf("%s: err = %v, want the lookup error", tc.name, err)
			}
			continue
		}
		if typed := orm.ErrorFrom(err); typed.Code != orm.ErrForbidden {
			t.Fatalf("%s: code = %d, want %d", tc.name, typed.Code, orm.ErrForbidden)
		}
	}
}
