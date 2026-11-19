package orm

import "testing"

func TestEnvironmentCachedFieldsBatchAndInvalidate(t *testing.T) {
	env := NewEnvironment(nil, 1, nil, false)
	env.CacheField("m", "1", "name", "a")
	env.CacheField("m", "2", "name", nil)
	env.CacheField("other", "3", "name", "z")

	got := env.CachedFields("m", "name", []string{"1", "2", "3"})
	if len(got) != 2 || got["1"] != "a" {
		t.Fatalf("unexpected batch result: %#v", got)
	}
	if v, ok := got["2"]; !ok || v != nil {
		t.Fatalf("cached nil must be reported as present: %#v", got)
	}

	env.MarkPrefetched("m", "name", []string{"1", "2"})
	env.Invalidate("m", []string{"1"})
	if _, ok := env.CachedField("m", "1", "name"); ok {
		t.Fatal("record 1 should be invalidated")
	}
	if missing := env.MissingPrefetchIDs("m", "name", []string{"1", "2"}); len(missing) != 1 || missing[0] != "1" {
		t.Fatalf("unexpected missing ids: %#v", missing)
	}
	if got := (*Environment)(nil).CachedFields("m", "name", []string{"1"}); len(got) != 0 {
		t.Fatalf("nil env must return empty: %#v", got)
	}
}
