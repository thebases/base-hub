---
name: base-mcp
version: 0.0.1
category: System
installable: true
depends: [base, base-auth]
auto_install: false
frontend_widgets: {}
---

# MCP

Model Context Protocol transport module for Base Hub.

This module owns the backend `/mcp` endpoint that AI agents can use to
discover and control the service capability surface over JSON-RPC.

The gateway implements the MCP Streamable HTTP transport: the `initialize`
handshake, `tools/list`, and `tools/call` over a single `/mcp` endpoint,
answers JSON-RPC notifications with a bare `202 Accepted`, and honors the
transport's optional `Mcp-Session-Id` (issued on `initialize`, validated only
if a client sends it back, terminable via `DELETE /mcp`) and
`MCP-Protocol-Version` headers. It only ever returns a single JSON response
(`GET /mcp` declines the transport's optional server-initiated SSE stream
with `405`), so it works with Claude's Messages API MCP connector and
OpenAI's Responses API `mcp` tool type directly over HTTPS. DeepSeek and
Kimi/Moonshot are not known to speak MCP natively as of this writing — they
still need an MCP-capable agent host in front of this gateway (see
`.agents/knowledge/mcp-cross-provider-requirements.md`).

Beyond the generic `base.*` tools, three optional tool groups are appended to
`tools/list` only when their matching module is enabled for the caller's
tenant DB (an unknown/disabled tool name and a missing tool look identical to
the caller): `website.*` (site/page/CMS CRUD, SEO, publish), `knowledge.*`
(the `user` namespace of the markdown doc tree — list/read/create/update/
rename/delete), and `workflow.*` (`base.workflow.*` model CRUD plus
export/import and canvas action/edge-replace wrappers).
