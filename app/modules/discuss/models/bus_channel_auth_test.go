package models

import (
	"context"
	"testing"
)

func TestAuthorizeDiscussUserChannel(t *testing.T) {
	ctx := context.Background()
	if ok, _ := authorizeDiscussUserChannel(ctx, 5, "discuss:user:5"); !ok {
		t.Fatal("own discuss user channel must be allowed")
	}
	for _, channel := range []string{"discuss:user:6", "discuss:user:", "discuss:user:5:x"} {
		if ok, _ := authorizeDiscussUserChannel(ctx, 5, channel); ok {
			t.Fatalf("%s must be denied for user 5", channel)
		}
	}
	if ok, _ := authorizeDiscussUserChannel(ctx, 0, "discuss:user:0"); ok {
		t.Fatal("anonymous caller must be denied")
	}
}

func TestAuthorizeDiscussChannelChannelNeedsMembership(t *testing.T) {
	prev := discussChannelMemberLookup
	t.Cleanup(func() { discussChannelMemberLookup = prev })
	discussChannelMemberLookup = func(_ context.Context, channelID, userID int64) (int64, bool, error) {
		return 1, channelID == 9 && userID == 5, nil
	}
	ctx := context.Background()
	if ok, err := authorizeDiscussChannelChannel(ctx, 5, "discuss:channel:9"); err != nil || !ok {
		t.Fatalf("member: ok=%v err=%v, want allowed", ok, err)
	}
	if ok, _ := authorizeDiscussChannelChannel(ctx, 6, "discuss:channel:9"); ok {
		t.Fatal("non-member must be denied")
	}
	if ok, _ := authorizeDiscussChannelChannel(ctx, 5, "discuss:channel:abc"); ok {
		t.Fatal("malformed channel id must be denied")
	}
}
