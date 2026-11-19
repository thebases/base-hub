package models

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestValidateFrontendBIDConstraintRequiresPrefixedBIDOnlyForFrontendCreate(t *testing.T) {
	baseEnv := orm.NewEnvironment(nil, 0, nil, false)
	records := orm.NewRecordSet(nil, baseEnv, nil)

	writeCtx := orm.ContextWithEnvironment(context.Background(), baseEnv.WithContext(map[string]any{
		frontendBIDContextKey:       frontendBIDContextValue,
		frontendBIDMethodContextKey: "write",
	}))
	if err := validateFrontendBIDConstraint(writeCtx, records, orm.RecordMap{"bid": ""}); err != nil {
		t.Fatalf("write validation returned error: %v", err)
	}

	createCtx := orm.ContextWithEnvironment(context.Background(), baseEnv.WithContext(map[string]any{
		frontendBIDContextKey:       frontendBIDContextValue,
		frontendBIDMethodContextKey: frontendBIDCreateMethod,
	}))
	if err := validateFrontendBIDConstraint(createCtx, records, orm.RecordMap{"bid": ""}); err == nil {
		t.Fatalf("expected create validation error for empty bid")
	}
	if err := validateFrontendBIDConstraint(createCtx, records, orm.RecordMap{"bid": "x-demo"}); err != nil {
		t.Fatalf("create validation rejected prefixed bid: %v", err)
	}
}
