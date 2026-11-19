package models

// T23: chatter access follows the target record; RPC callers cannot spoof authors or use raw CRUD.

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func discussUserContext(userID int64) context.Context {
	return orm.ContextWithEnvironment(context.Background(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID: userID, Login: "u", RoleID: 2, AuthMode: "bearer",
	}))
}

func isForbidden(err error) bool {
	var typed *orm.Error
	return errors.As(err, &typed) && typed.Code == orm.ErrForbidden
}

func TestPostMessageRefusesOtherAuthor(t *testing.T) {
	_, err := runtimePostMessage(discussUserContext(7), nil, nil, nil, map[string]any{
		"res_model": "tbl.partner", "res_id": 1, "author_id": 8, "is_note": true,
	})
	if !isForbidden(err) {
		t.Fatalf("spoofed author should be forbidden, got %v", err)
	}
}

func TestPostMessageNeedsTargetRead(t *testing.T) {
	previous := discussTargetAccess
	t.Cleanup(func() { discussTargetAccess = previous })
	var gotOperation string
	discussTargetAccess = func(_ context.Context, _ string, operation string, _ []string) error {
		gotOperation = operation
		return errors.New("denied")
	}
	_, err := runtimePostMessage(discussUserContext(7), nil, nil, nil, map[string]any{
		"res_model": "tbl.partner", "res_id": 1, "is_note": true,
	})
	if !isForbidden(err) || gotOperation != "read" {
		t.Fatalf("expected forbidden after read check, got %v (operation %q)", err, gotOperation)
	}
}

func TestFollowerChangeNeedsReadForSelfWriteForOthers(t *testing.T) {
	previous := discussTargetAccess
	t.Cleanup(func() { discussTargetAccess = previous })
	var gotOperation string
	discussTargetAccess = func(_ context.Context, _ string, operation string, _ []string) error {
		gotOperation = operation
		return nil
	}
	ctx := discussUserContext(7)
	if err := ensureFollowerChangeAllowed(ctx, "tbl.partner", 1, 7); err != nil || gotOperation != "read" {
		t.Fatalf("self follow: err=%v operation=%q", err, gotOperation)
	}
	if err := ensureFollowerChangeAllowed(ctx, "tbl.partner", 1, 9); err != nil || gotOperation != "write" {
		t.Fatalf("follow other user: err=%v operation=%q", err, gotOperation)
	}
	gotOperation = ""
	if err := ensureFollowerChangeAllowed(context.Background(), "tbl.partner", 1, 9); err != nil || gotOperation != "" {
		t.Fatalf("internal caller should skip checks: err=%v operation=%q", err, gotOperation)
	}
}

func TestChannelTargetNeedsMembership(t *testing.T) {
	previous := discussChannelMemberLookup
	t.Cleanup(func() { discussChannelMemberLookup = previous })
	discussChannelMemberLookup = func(context.Context, int64, int64) (int64, bool, error) { return 0, false, nil }
	if err := ensureDiscussTargetAccess(discussUserContext(7), DiscussChannelModelName, 3, "read"); !isForbidden(err) {
		t.Fatalf("non-member should be forbidden, got %v", err)
	}
}

func TestGuardedDiscussCRUDNeedsSystemGroup(t *testing.T) {
	previous := discussCallerGroupBIDs
	t.Cleanup(func() { discussCallerGroupBIDs = previous })
	methods := guardedDiscussCRUD(DiscussMessageModelName)
	if len(methods) != 3 {
		t.Fatalf("expected create/write/unlink, got %d", len(methods))
	}
	discussCallerGroupBIDs = func(context.Context) ([]string, error) { return []string{"base.group_user"}, nil }
	for _, method := range methods {
		if _, err := method.Call(discussUserContext(7), nil, []string{"1"}, nil, map[string]any{"values": map[string]any{}}); !isForbidden(err) {
			t.Errorf("%s by a plain user should be forbidden, got %v", method.Name, err)
		}
	}
}

func TestDiscussMessageRecordRule(t *testing.T) {
	prevModels, prevScope := discussMessageResModels, discussTargetReadScope
	t.Cleanup(func() { discussMessageResModels, discussTargetReadScope = prevModels, prevScope })
	discussMessageResModels = func(context.Context) ([]string, error) {
		return []string{"tbl.partner", "tbl.hidden", DiscussMessageModelName}, nil
	}
	discussTargetReadScope = func(_ context.Context, _ int64, resModel string) (orm.DomainNode, bool, error) {
		if resModel == "tbl.partner" {
			return orm.Where("res_model", "=", resModel), true, nil
		}
		return orm.DomainNode{}, false, nil
	}

	ctx := discussUserContext(7)
	domain, err := discussMessageRecordRule(ctx, orm.EnvironmentFromContext(ctx), nil, "read")
	if err != nil {
		t.Fatal(err)
	}
	if domain.Kind != orm.DomainKindOr || len(domain.Children) != 2 ||
		domain.Children[0].Field != "author_id" || domain.Children[1].Value != "tbl.partner" {
		t.Fatalf("expected own messages OR readable partner targets, got %#v", domain)
	}

	internal, err := discussMessageRecordRule(context.Background(), nil, nil, "read")
	if err != nil || internal.Kind != orm.DomainKindTrue {
		t.Fatalf("internal callers are unrestricted, got %#v %v", internal, err)
	}
}

func TestDiscussChannelMessagesNeedMembership(t *testing.T) {
	previous := discussMemberChannelIDs
	t.Cleanup(func() { discussMemberChannelIDs = previous })
	discussMemberChannelIDs = func(context.Context, int64) ([]any, error) { return []any{int64(3)}, nil }
	domain, ok, err := discussTargetReadDomain(discussUserContext(7), 7, DiscussChannelModelName)
	if err != nil || !ok || domain.Kind != orm.DomainKindAnd || domain.Children[1].Field != "res_id" {
		t.Fatalf("expected channel membership scope, got %#v ok=%v err=%v", domain, ok, err)
	}
	discussMemberChannelIDs = func(context.Context, int64) ([]any, error) { return nil, nil }
	if _, ok, _ := discussTargetReadDomain(discussUserContext(7), 7, DiscussChannelModelName); ok {
		t.Fatal("no membership should give no channel scope")
	}
}
