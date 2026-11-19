import { Component, loadTemplate, xml } from "nodexui/owl";

import { Button, Icon, Popover, PopoverContent, PopoverTrigger } from "nodexui/component";
import { idFromMany2One, labelFromMany2One } from "./chatter-format.js";
import { UserPicker } from "./UserPicker.js";

const template = await loadTemplate(new URL("./FollowerButton.xml", import.meta.url));

// FollowerButton is a Follow/Unfollow toggle (for the current viewer) plus a
// popover listing every follower on the record, with a UserPicker to add any
// other base.users as a follower and a remove control per follower row.
// Every mutation runs through ChatterPanel.js's toggleFollow/addFollower/
// removeFollower (the backend's subscribe/unsubscribe RuntimeMethods), never
// a raw create/write/unlink on discuss.followers.
export class FollowerButton extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Icon, Popover, PopoverContent, PopoverTrigger, UserPicker };
  static props = {
    followers: { type: Array },
    isFollowing: { type: Boolean },
    onToggle: { type: Function },
    onAddFollower: { type: Function },
    onRemoveFollower: { type: Function },
  };

  followerName(follower) {
    return labelFromMany2One(follower.user_id, t("User #{id}", { id: follower.user_id }));
  }

  removeFollower(follower) {
    this.props.onRemoveFollower(idFromMany2One(follower.user_id));
  }

  // followerUsers reshapes the current follower rows into UserPicker's
  // {id, name} shape so newly-added users are excluded from its search.
  get followerUsers() {
    return this.props.followers.map((follower) => ({ id: idFromMany2One(follower.user_id), name: this.followerName(follower) }));
  }
}
