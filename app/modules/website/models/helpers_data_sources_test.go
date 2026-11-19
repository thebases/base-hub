package models

import (
	"context"
	"testing"
)

func TestClampDataSourceLimit(t *testing.T) {
	cases := map[any]int{nil: dataSourceDefaultLimit, "0": dataSourceDefaultLimit, "3": 3, "999": dataSourceMaxLimit, "-4": dataSourceDefaultLimit}
	for in, want := range cases {
		if got := clampDataSourceLimit(in); got != want {
			t.Errorf("%v: got %d want %d", in, got, want)
		}
	}
}

func TestRegisterWebsiteDataSource(t *testing.T) {
	fn := func(context.Context, int64, int) ([]WebsiteDataItem, error) { return nil, nil }
	if RegisterWebsiteDataSource("Bad Key", fn) == nil || RegisterWebsiteDataSource("ok-key", nil) == nil {
		t.Fatal("invalid registration accepted")
	}
	if err := RegisterWebsiteDataSource("unit-source", fn); err != nil {
		t.Fatal(err)
	}
}
