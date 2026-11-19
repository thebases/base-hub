import { Component, loadTemplate, xml } from "nodexui/owl";

import { Avatar, Message, MessageScroller } from "nodexui/component";
import { formatChatterDate, idFromMany2One, initialsFromName, labelFromMany2One } from "./chatter-format.js";

const template = await loadTemplate(new URL("./MessageFeed.xml", import.meta.url));

// MessageFeed maps discuss.message rows onto base-ui's pure-Owl
// Message/MessageScroller components (per user requirement — reuse, don't
// hand-roll bubble markup). Messages arrive newest-first (chatter-rpc.js
// sorts "created_at DESC"), so the newest message is already row 0 at the
// top of the list — MessageScroller's autoscroll is disabled here since
// force-scrolling to the bottom would instead reveal the oldest message.
// side="outgoing" for the current viewer's own messages (avatar rendered on
// the left, per requirement), "incoming" otherwise (avatar on the right).
export class MessageFeed extends Component {
  static template = xml({ raw: [template] });
  static components = { Avatar, Message, MessageScroller };
  static props = {
    messages: { type: Array },
    currentUserId: { type: [Number, String], optional: true },
  };

  get rows() {
    return this.props.messages.map((message) => {
      const authorId = idFromMany2One(message.author_id);
      return {
        id: message.id,
        body: message.body ?? "",
        subject: message.subject ?? "",
        createdAt: formatChatterDate(message.createdAt),
        authorName: labelFromMany2One(message.author_id, t("Unknown")),
        authorInitials: initialsFromName(labelFromMany2One(message.author_id, "?")),
        side: this.props.currentUserId && authorId === this.props.currentUserId ? "outgoing" : "incoming",
      };
    });
  }
}
