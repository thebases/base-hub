package models

import (
	"context"
	"errors"
	"slices"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func busUserCtx(userID int64, sudo bool) context.Context {
	env := orm.NewEnvironment(nil, userID, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, sudo)
	return orm.ContextWithEnvironment(context.Background(), env)
}

func registerTestAuthorizer(t *testing.T, prefix string, fn ServiceBusChannelAuthorizer) {
	t.Helper()
	RegisterServiceBusChannelAuthorizer(prefix, fn)
	resetServiceBusAuthCache()
	t.Cleanup(func() {
		serviceBusChannelAuthorizersMu.Lock()
		delete(serviceBusChannelAuthorizers, prefix)
		serviceBusChannelAuthorizersMu.Unlock()
		resetServiceBusAuthCache()
	})
}

func resetServiceBusAuthCache() {
	serviceBusAuthCacheMu.Lock()
	serviceBusAuthCache = map[serviceBusAuthCacheKey]serviceBusAuthCacheEntry{}
	serviceBusAuthCacheMu.Unlock()
}

func TestAuthorizeServiceBusChannelsCachesDecisionsNotErrors(t *testing.T) {
	calls := 0
	fail := true
	registerTestAuthorizer(t, "test:cache:", func(_ context.Context, userID int64, channel string) (bool, error) {
		calls++
		if channel == "test:cache:err" && fail {
			return false, errors.New("db down")
		}
		return userID == 5, nil
	})
	ctx := busUserCtx(5, false)
	for range 3 {
		AuthorizeServiceBusChannels(ctx, []string{"test:cache:1"})
	}
	if calls != 1 {
		t.Fatalf("authorizer calls = %d, want 1 (cached)", calls)
	}
	if got := AuthorizeServiceBusChannels(busUserCtx(6, false), []string{"test:cache:1"}); len(got) != 0 {
		t.Fatalf("user 6 must not reuse user 5's decision, got %v", got)
	}
	if got := AuthorizeServiceBusChannels(ctx, []string{"test:cache:err"}); len(got) != 0 {
		t.Fatalf("error must deny, got %v", got)
	}
	fail = false
	if got := AuthorizeServiceBusChannels(ctx, []string{"test:cache:err"}); len(got) != 1 {
		t.Fatalf("error must not be cached, got %v", got)
	}
}

func TestAuthorizeServiceBusChannelsOwnAndGlobalOnly(t *testing.T) {
	requested := []string{"servicebus:global", "servicebus:user:5", "servicebus:user:6", "servicebus:user:5x", "discuss:user:6", "anything"}
	got := AuthorizeServiceBusChannels(busUserCtx(5, false), requested)
	want := []string{"servicebus:global", "servicebus:user:5"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAuthorizeServiceBusChannelsSudoAndAnonymous(t *testing.T) {
	requested := []string{"servicebus:global", "servicebus:user:6", "discuss:channel:9"}
	if got := AuthorizeServiceBusChannels(busUserCtx(5, true), requested); !slices.Equal(got, requested) {
		t.Fatalf("sudo: got %v, want all", got)
	}
	if got := AuthorizeServiceBusChannels(context.Background(), requested); !slices.Equal(got, []string{"servicebus:global"}) {
		t.Fatalf("no env: got %v, want global only", got)
	}
}

func TestAuthorizeServiceBusChannelsModuleAuthorizer(t *testing.T) {
	registerTestAuthorizer(t, "test:room:", func(_ context.Context, userID int64, channel string) (bool, error) {
		switch channel {
		case "test:room:1":
			return userID == 5, nil
		case "test:room:err":
			return true, errors.New("lookup failed")
		}
		return false, nil
	})
	// The longest prefix wins over a broader one.
	registerTestAuthorizer(t, "test:", func(context.Context, int64, string) (bool, error) { return true, nil })

	got := AuthorizeServiceBusChannels(busUserCtx(5, false), []string{"test:room:1", "test:room:2", "test:room:err", "test:other"})
	want := []string{"test:room:1", "test:other"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := AuthorizeServiceBusChannels(busUserCtx(6, false), []string{"test:room:1"}); len(got) != 0 {
		t.Fatalf("user 6: got %v, want none", got)
	}
}

func stubServiceBusAccess(t *testing.T, groups ...string) {
	t.Helper()
	prevGroups, prevDefault := serviceBusCallerGroupBIDs, serviceBusDefaultAccess
	serviceBusCallerGroupBIDs = func(context.Context) ([]string, error) { return groups, nil }
	serviceBusDefaultAccess = func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	}
	t.Cleanup(func() { serviceBusCallerGroupBIDs, serviceBusDefaultAccess = prevGroups, prevDefault })
}

func isForbidden(err error) bool {
	var ormErr *orm.Error
	return errors.As(err, &ormErr) && ormErr.Code == orm.ErrForbidden
}

func TestEnsureServiceBusPublishAllowed(t *testing.T) {
	stubServiceBusAccess(t, "base.group_user")
	if err := EnsureServiceBusPublishAllowed(busUserCtx(5, false)); !isForbidden(err) {
		t.Fatalf("internal user: err = %v, want forbidden", err)
	}
	if err := EnsureServiceBusPublishAllowed(context.Background()); !isForbidden(err) {
		t.Fatalf("no env: err = %v, want forbidden", err)
	}
	if err := EnsureServiceBusPublishAllowed(busUserCtx(5, true)); err != nil {
		t.Fatalf("sudo: %v", err)
	}
	stubServiceBusAccess(t, "base.group_user", "base.group_system")
	if err := EnsureServiceBusPublishAllowed(busUserCtx(5, false)); err != nil {
		t.Fatalf("group_system: %v", err)
	}
}

func TestServiceBusLogAccess(t *testing.T) {
	meta := &orm.ModelMetadata{Name: ServiceBusModelName}
	rpcEnv := orm.NewEnvironment(nil, 5, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)

	stubServiceBusAccess(t, "base.group_user")
	if err := serviceBusLogAccess(context.Background(), rpcEnv, meta, "read", nil, nil); !isForbidden(err) {
		t.Fatalf("internal user read: err = %v, want forbidden", err)
	}
	if err := serviceBusLogAccess(context.Background(), orm.NewEnvironment(nil, 0, nil, false), meta, "read", nil, nil); err != nil {
		t.Fatalf("server-side read: %v", err)
	}
	stubServiceBusAccess(t, "base.group_system")
	if err := serviceBusLogAccess(context.Background(), rpcEnv, meta, "read", nil, nil); err != nil {
		t.Fatalf("group_system read: %v", err)
	}
}

func TestServiceBusPublishIsInternal(t *testing.T) {
	for _, method := range ServiceBusModelSpec.RuntimeMethods {
		if method.Name == "publish" {
			if !method.Internal {
				t.Fatal("publish must be an Internal runtime method")
			}
			return
		}
	}
	t.Fatal("publish runtime method not found")
}
