#!/usr/bin/env node
// base-hub-mcp-bridge.js
//
// Official stdio<->HTTP bridge for the Base Hub MCP gateway, served for
// download at GET /mcp/bridge. Only needed when an MCP client is configured
// to launch a local stdio server rather than add a remote URL (see the
// "Option A — remote URL" section of the Base MCP user guide, which needs
// no bridge at all). Requires Node.js 18+ (built-in fetch, no dependencies).
//
// Configuration (environment variables):
//   BASE_HUB_URL      required  e.g. https://your-company.basehub.example.com
//   BASE_HUB_MCP_KEY  required  your MCP API Key (Bearer token)
//   BASE_HUB_DB_NAME  optional  only for multi-database organizations
//
// Usage in an MCP client's stdio server config:
//   { "command": "node", "args": ["/path/to/base-hub-mcp-bridge.js"],
//     "env": { "BASE_HUB_URL": "...", "BASE_HUB_MCP_KEY": "..." } }

"use strict";

const BASE_HUB_URL = process.env.BASE_HUB_URL;
const BASE_HUB_MCP_KEY = process.env.BASE_HUB_MCP_KEY;
const BASE_HUB_DB_NAME = process.env.BASE_HUB_DB_NAME; // optional
const REQUEST_TIMEOUT_MS = 30000;

if (!BASE_HUB_URL || !BASE_HUB_MCP_KEY) {
  console.error("base-hub-mcp-bridge: BASE_HUB_URL and BASE_HUB_MCP_KEY must be set");
  process.exit(1);
}

const mcpEndpoint = new URL("/mcp", BASE_HUB_URL).toString();

let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  let newlineIndex;
  while ((newlineIndex = buffer.indexOf("\n")) >= 0) {
    const line = buffer.slice(0, newlineIndex).trim();
    buffer = buffer.slice(newlineIndex + 1);
    if (line) forward(line);
  }
});
process.stdin.on("end", () => process.exit(0));

// forward relays one newline-delimited JSON-RPC message from stdin to the
// gateway. Failures are reported as a JSON-RPC error on stdout (when the
// original message carried an id) so the client sees a clean error instead
// of hanging on a missing reply; the bearer token is never logged.
async function forward(line) {
  let id = null;
  try {
    id = JSON.parse(line).id ?? null;
  } catch {
    // Malformed input from the client; nothing meaningful to reply with.
  }

  const headers = {
    "Content-Type": "application/json",
    "Authorization": `Bearer ${BASE_HUB_MCP_KEY}`,
  };
  if (BASE_HUB_DB_NAME) headers["X-Database-Name"] = BASE_HUB_DB_NAME;

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const res = await fetch(mcpEndpoint, {
      method: "POST",
      headers,
      body: line,
      signal: controller.signal,
    });
    if (res.status === 202) return; // notification: no reply expected
    const text = await res.text();
    if (text) process.stdout.write(text.trim() + "\n");
  } catch (err) {
    console.error(`base-hub-mcp-bridge: request failed: ${err.message}`);
    if (id !== null) {
      process.stdout.write(
        JSON.stringify({
          jsonrpc: "2.0",
          id,
          error: { code: -32000, message: `bridge request failed: ${err.message}` },
        }) + "\n"
      );
    }
  } finally {
    clearTimeout(timeout);
  }
}
