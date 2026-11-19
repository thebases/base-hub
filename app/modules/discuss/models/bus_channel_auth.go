package models

// Service-bus channel authorization for discuss channels (plan T16).
//
// base-bus only grants its own global and per-user channels; discuss vouches for its two channel
// families here: "discuss:user:<id>" (notifications, WebRTC signalling) is readable only by that
// user, and "discuss:channel:<id>" (membership, read receipts, call lifecycle) only by a current
// member of the channel. Membership is re-checked on every poll, so a member who leaves stops
// receiving the channel's events on an open stream.

import (
	"context"
	"strconv"
	"strings"

	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
)

const (
	discussUserChannelPrefix    = "discuss:user:"
	discussChannelChannelPrefix = "discuss:channel:"
)

// discussChannelMemberLookup is a test seam over the membership query.
var discussChannelMemberLookup = findChannelMemberID

func init() {
	busmodels.RegisterServiceBusChannelAuthorizer(discussUserChannelPrefix, authorizeDiscussUserChannel)
	busmodels.RegisterServiceBusChannelAuthorizer(discussChannelChannelPrefix, authorizeDiscussChannelChannel)
}

func authorizeDiscussUserChannel(_ context.Context, userID int64, channel string) (bool, error) {
	return busmodels.ServiceBusOwnUserChannel(userID, strings.TrimPrefix(channel, discussUserChannelPrefix)), nil
}

func authorizeDiscussChannelChannel(ctx context.Context, userID int64, channel string) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	channelID, err := strconv.ParseInt(strings.TrimPrefix(channel, discussChannelChannelPrefix), 10, 64)
	if err != nil || channelID <= 0 {
		return false, nil
	}
	_, member, err := discussChannelMemberLookup(ctx, channelID, userID)
	return member, err
}
