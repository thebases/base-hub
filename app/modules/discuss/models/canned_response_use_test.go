package models

// T91: tbl.discuss_canned_response.use runs through orm.Typed + Call.ID.

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestUseCannedResponseMethod(t *testing.T) {
	var wroteIDs []string
	var wroteValues orm.RecordMap
	restore := orm.StubRuntimeRepository(DiscussCannedResponseModelName, orm.RuntimeRepositoryStub{
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			wroteIDs, wroteValues = ids, values
			return true, nil
		},
	})
	t.Cleanup(restore)
	method := orm.Typed(orm.RuntimeMethod{Name: "use", Public: true, UsesPathID: true}, useCannedResponse)

	got, err := method.Call(context.Background(), nil, []string{"7"}, nil, nil)
	if err != nil || got != true {
		t.Fatalf("use = %v, %v; want true, nil", got, err)
	}
	if len(wroteIDs) != 1 || wroteIDs[0] != "7" || wroteValues["last_used_at"] == nil {
		t.Fatalf("write ids=%v values=%v, want id 7 with last_used_at", wroteIDs, wroteValues)
	}

	for _, ids := range [][]string{nil, {"0"}, {"1", "2"}} {
		_, err := method.Call(context.Background(), nil, ids, nil, nil)
		var ormErr *orm.Error
		if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrInvalidParams {
			t.Fatalf("ids %v: err = %v, want invalid params", ids, err)
		}
	}
}
