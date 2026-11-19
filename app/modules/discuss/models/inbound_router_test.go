package models

// T65/T73: follower user ids decode from [id, display_name]; auto-subscribe keeps an existing
// follower's subtypes; inbound email is idempotent on its Message-ID.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMatchingFollowerUserIDsReadsMany2OneTuple(t *testing.T) {
	defer orm.StubRuntimeRepository(DiscussFollowersModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(context.Context, orm.RecordMap, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(4), "user_id": [2]any{int64(7), "Bob"}}}, nil
		},
	})()
	previous := followerSubtypeRows
	t.Cleanup(func() { followerSubtypeRows = previous })
	followerSubtypeRows = func(context.Context, []int64) ([]discussFollowerSubtypeRow, error) { return nil, nil }

	got, err := matchingFollowerUserIDs(context.Background(), "tbl.partner", 1, nil)
	if err != nil || len(got) != 1 || got[0] != 7 {
		t.Fatalf("follower users = %v (err %v), want [7]", got, err)
	}
}

func TestSubscribeKeepsExistingSubtypesUnlessGiven(t *testing.T) {
	var writes []orm.RecordMap
	defer orm.StubRuntimeRepository(DiscussFollowersModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(context.Context, orm.RecordMap, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(4)}}, nil
		},
		Write: func(_ context.Context, _ []string, values orm.RecordMap) (bool, error) {
			writes = append(writes, values)
			return true, nil
		},
	})()

	// Auto-subscribe of a message recipient passes nil: the follower's restriction must survive.
	subscribeRecipients(context.Background(), "tbl.partner", 1, []int64{7})
	if len(writes) != 0 {
		t.Fatalf("auto-subscribe rewrote an existing follower: %v", writes)
	}
	// An explicit (even empty) list still replaces the restriction.
	if err := Subscribe(context.Background(), "tbl.partner", 1, 7, []int64{}); err != nil || len(writes) != 1 {
		t.Fatalf("explicit subscribe: err=%v writes=%v", err, writes)
	}
}

// stubInboundTx makes discussTx a pass-through (the in-memory stubs have no database).
func stubInboundTx(t *testing.T) {
	t.Helper()
	previous := discussTx
	t.Cleanup(func() { discussTx = previous })
	discussTx = func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
}

func TestRouteInboundEmailDuplicateCreatesNothing(t *testing.T) {
	stubInboundTx(t)
	creates := 0
	defer orm.StubRuntimeRepository(DiscussMessageModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			if filter["message_id_header"] == "<m1@relay>" {
				return []orm.RecordMap{{"id": int64(42), "res_model": "tbl.partner", "res_id": int64(3)}}, nil
			}
			return nil, nil
		},
		Create: func(context.Context, orm.RecordMap) (orm.RecordMap, error) {
			creates++
			return orm.RecordMap{"id": int64(43)}, nil
		},
	})()

	outcome, err := RouteInboundEmail(context.Background(), InboundEmail{MessageIDHeader: " <m1@relay> ", ToEmails: []string{"sales@x"}})
	if err != nil || outcome.Status != "duplicate" || outcome.MessageID != 42 || outcome.ResID != 3 || creates != 0 {
		t.Fatalf("outcome=%+v err=%v creates=%d, want duplicate of message 42 and no create", outcome, err, creates)
	}
}

func TestRouteInboundEmailLostRaceReportsDuplicate(t *testing.T) {
	stubInboundTx(t)
	stored := false
	defer orm.StubRuntimeRepository(DiscussMessageModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			switch {
			case filter["message_id_header"] == "<orig@x>":
				return []orm.RecordMap{{"res_model": "tbl.partner", "res_id": int64(3)}}, nil
			case filter["message_id_header"] == "<m2@relay>" && stored:
				return []orm.RecordMap{{"id": int64(50), "res_model": "tbl.partner", "res_id": int64(3)}}, nil
			}
			return nil, nil
		},
		// A concurrent delivery stores the same Message-ID first: this insert hits the unique index.
		Create: func(context.Context, orm.RecordMap) (orm.RecordMap, error) {
			stored = true
			return nil, &pgconn.PgError{Code: "23505"}
		},
	})()

	outcome, err := RouteInboundEmail(context.Background(), InboundEmail{MessageIDHeader: "<m2@relay>", InReplyTo: "<orig@x>"})
	if err != nil || outcome.Status != "duplicate" || outcome.MessageID != 50 {
		t.Fatalf("outcome=%+v err=%v, want duplicate of message 50", outcome, err)
	}
}
