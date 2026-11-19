// Port of frontend-admin's modules/knowledge/pages/markdown-public-page.tsx.
// Unauthenticated, no Shell wrapper — matches the public /view-md/* route.

import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { fetchPublicMarkdownDocument } from "@/modules/knowledge/api.js";
import { extractMarkdownHeadings } from "@/modules/knowledge/markdown-headings.js";
import { MarkdownViewer } from "@/modules/knowledge/components/MarkdownViewer.js";

const template = await loadTemplate(new URL("./MarkdownPublicPage.xml", import.meta.url));

export class MarkdownPublicPage extends Component {
  static template = xml({ raw: [template] });
  static components = { MarkdownViewer };

  setup() {
    // The router's splat capture (router/match.js) hands back the raw
    // pathname segment, still percent-encoded (e.g. "docs/my%20file.md").
    // React's counterpart explicitly decodeURIComponent()s the full
    // pathname before use; without it, fetchPublicMarkdownDocument's own
    // encodePathSegments() would double-encode ("%2520") and 404.
    const rawPath = this.props.params?.path ?? "";
    let markdownPath = rawPath;
    try {
      markdownPath = decodeURIComponent(rawPath);
    } catch {
      // Malformed percent-encoding: fall back to the raw value rather than throw.
    }
    this.state = useState({
      status: markdownPath ? "loading" : "error",
      content: "",
      message: markdownPath ? "" : t("Markdown path is missing."),
    });

    onWillStart(async () => {
      if (!markdownPath) return;
      try {
        const content = await fetchPublicMarkdownDocument(markdownPath);
        this.state.status = "ready";
        this.state.content = content;
      } catch (error) {
        this.state.status = "error";
        this.state.message = error instanceof Error ? error.message : t("Failed to load markdown document");
      }
    });
  }

  get headings() {
    return extractMarkdownHeadings(this.state.content);
  }
}
