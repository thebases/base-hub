package models

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestErrNoRateIsClassifiedAsBusinessRule(t *testing.T) {
	err := fmt.Errorf("%w: currency id(s) [7] on 2026-10-13", ErrNoRate)
	if !errors.Is(err, ErrNoRate) {
		t.Fatal("wrapped error must match ErrNoRate")
	}
	classified := orm.ClassifyError(err)
	if classified == nil || classified.Code != orm.ErrBusinessRule {
		t.Fatalf("expected a business-rule classification, got %#v", classified)
	}
}

func TestGetConversionRateSameOrMissingCurrencyIsIdentity(t *testing.T) {
	// Same-currency and unset-currency conversion is exactly 1 by definition and never touches rates.
	for _, args := range [][]any{{int64(3), int64(3)}, {int64(0), int64(3)}} {
		got, err := getConversionRateResCurrency(context.Background(), nil, nil, args, nil)
		if err != nil || got != float64(1) {
			t.Fatalf("args %v: got %v, %v", args, got, err)
		}
	}
}
