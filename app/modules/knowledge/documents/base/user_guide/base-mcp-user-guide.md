# Base HUB MCP Prompting Guide

| Field | Value |
|---|---|
| Version | 2.3 |
| Last updated | 2026-08-07 |
| Applies to | Base HUB MCP module (`mcp` v0.2.0) and the live Base HUB runtime models |
| Audience | End users, operators, and administrators using AI assistants through Base HUB MCP |

---

## Overview

This guide shows you how to use an AI assistant with **Base HUB** and **Base HUB MCP** to do real work by prompting: inspect models, create and update records, work with relations, attach customer documents, and render reports.

The step-by-step business examples are kept in a separate companion document:

- [Base HUB MCP Use-Case Examples](./base-mcp-user-guide-examples.md)

That companion file contains the two connected use cases:

1. Build and maintain a **customer phone book**
2. Build a **customer document management flow**

> **Important limitation**
>
> Base HUB MCP can create and edit **custom model metadata** with `base.model.create_custom`, `base.model.add_field`, `base.model.delete_field`, and `base.model.rename_field` — these tools only write catalog rows and never touch a live database table by themselves. Call `base.model.publish` once the fields you need exist to provision the physical Postgres table and register a queryable runtime model, so `base.record.*` and `base.model.create_ui` start working against it; `base.model.unpublish` takes it back offline (catalog and data are untouched either way). Field mutations are rejected while a model is already published (SRS reason: editing the catalog after publish would otherwise silently drift from the already-provisioned table) — unpublish first, make the change, then publish again to apply it (a field delete or rename does alter/drop the physical column on republish). `base.model.publish`/`base.model.unpublish` both require a superadmin identity; `base.model.publish` is not persisted across a server restart yet, so re-publish after one. For live CRUD without any of this setup, ask the AI to work with existing runtime models such as `tbl.partner`, `sys.attachment`, and `sys.report`.

> **Website module tools are conditional**
>
> The `website.*` tools (see [How to Prompt for the Website Module](#how-to-prompt-for-the-website-module)) only appear in `tools/list` — and only work when called — if the **website module is enabled for your tenant database**. If it is not enabled, `tools/call` on any `website.*` tool returns the same "unknown tool" error as a made-up tool name; an administrator must enable the website module for your database before these tools become usable.

---

## Prerequisites

Before you start, make sure you have:

- [ ] A working Base HUB user account
- [ ] An MCP API key for your Base HUB user
- [ ] An AI client connected to Base HUB MCP
- [ ] Permission to read and update the models you plan to use
- [ ] A clear business goal, such as "manage customer contacts" or "store customer contracts"

If your AI client is not connected yet, first configure it to access Base HUB's `/mcp` endpoint with your MCP API key.

---

## Choose Your AI Client and Connect Base HUB MCP

Use this section first when your goal is not only prompting, but also making sure the AI client is truly connected to Base HUB MCP.

### Connection details you will need in every client

Before configuring any client, collect these values:

| Setting | What you need |
|---|---|
| Base HUB MCP URL | Your Base HUB remote MCP endpoint, typically `https://<your-domain>/mcp` |
| OAuth access token | A Bearer token from `POST /oauth/token` — see "Getting an OAuth access token" below. The legacy dedicated `mcp_api_key` credential has been removed; every client authenticates through OAuth now. |
| Database name | Only if your Base HUB deployment requires `X-Database-Name` |
| Tool scope | Decide whether the client should have read-only usage or write capability |

### Getting an OAuth access token for any MCP client

Every option below needs a Bearer token issued by Base HUB's OAuth server (`modules/base-auth`). Pick the grant that matches who's actually using the client:

- **You, a human, using your own Base HUB login/password** — `grant_type=password`:

  ```bash
  curl -X POST https://<your-host>/oauth/token \
    -d grant_type=password -d username=<your-login> -d password=<your-password> -d db=<tenant-db-name>
  ```

  Master/seed users (`admin`, `base1`) already work this way out of the box — no extra setup needed. A token minted this way carries your own RBAC role; every MCP action is attributable to you personally.

- **A shared automation/service caller (a GPT, connector, AI agent, or script acting on its own identity, not yours)** — register a dedicated `sys.oauth_client` first (see the [Builtin client-credentials guide](./oauth-builtin-client-credentials-guide.md)), then `grant_type=client_credentials`:

  ```bash
  curl -X POST https://<your-host>/oauth/token \
    -d grant_type=client_credentials -d client_id=<client-id> -d client_secret=<client-secret> -d db=<tenant-db-name>
  ```

  This is the safer default for anything shared across a team: it's individually revocable and doesn't depend on anyone's personal password.

Either way, the response's `access_token` is short-lived (15 minutes by default, `server.accessTokenTtlMinutes`). Whichever client option below tells you to "paste the access token," that token **will expire** — repeat the `curl` call and re-paste when requests start failing with `401`, unless that option's instructions say otherwise (only the GPT Actions OAuth panel and MCP clients with a live `refresh_token` handle this for you automatically).

### Option 1: ChatGPT with Apps or custom MCP apps

Use this option when you want Base HUB inside ChatGPT.

#### Important rule

A **Custom GPT instruction alone does not create MCP access**.

If you only write instructions such as:

```text
Use Base HUB MCP to list the models I can access.
```

ChatGPT may still answer like a normal chatbot if the chat does not have a real Base HUB app or MCP app attached.

#### How to connect in ChatGPT

1. Confirm your ChatGPT plan and workspace support apps or MCP apps.
2. If your workspace uses admin controls, ask the admin or owner to enable developer mode or custom MCP apps.
3. Create or publish the Base HUB app or custom MCP app that points to your Base HUB remote MCP URL.
4. Enable the app for the users or roles that need it.
5. Start a new chat and make sure the Base HUB app is actually selected or available in that conversation.
6. Run a simple read-only test prompt before attempting writes.

#### Authenticating the Apps / custom MCP app connection

ChatGPT's native Apps / custom MCP connector (this option, distinct from GPT **Actions** below) authenticates via OAuth 2.0 discovery: it expects your server to publish metadata at `/.well-known/oauth-authorization-server` and, per OpenAI's own docs, "recommends" client registration via Client ID Metadata Documents or supports Dynamic Client Registration — there is no field to paste a static Bearer token the way Gemini CLI's config does.

**Base HUB does not publish that discovery metadata yet** — `modules/base-auth` implements `/oauth/authorize` and `/oauth/token` (the endpoints themselves), but not the `.well-known` document that lets a client find them automatically. Until that's built, expect ChatGPT's Apps/custom-MCP-connector auth step to fail to locate Base HUB's OAuth endpoints on its own.

If you need working ChatGPT integration today, use **GPT Actions** instead (below) — a different, non-MCP surface where you enter the Authorization/Token URLs manually, which Base HUB fully supports. Apps/custom-MCP-connector OAuth support would need a follow-up implementation (an `/.well-known/oauth-authorization-server` document, optionally Dynamic Client Registration) before it can work the way this option's UI expects.

#### How to make a Custom GPT for ChatGPT

Use this when you want a reusable ChatGPT experience with Base HUB-specific instructions, suggested prompts, and optional knowledge files.

##### What a Custom GPT can and cannot do

- A Custom GPT **can** package instructions, conversation starters, knowledge, and selected capabilities.
- A Custom GPT **cannot by itself** create live Base HUB MCP access.
- To make the GPT actually call Base HUB MCP, you still need the real Base HUB app or custom MCP app available to that GPT or to the chat where it runs.

##### Step-by-step: create the Custom GPT

1. Open **Explore GPTs** in ChatGPT web or go to `https://chatgpt.com/gpts`.
2. Select **Create** to open the GPT builder.
3. Choose either the conversational builder or the direct configuration view.
4. Set a clear GPT name such as `Base HUB Assistant`.
5. Add a short description such as:

```text
Helps users work with Base HUB models, records, attachments, and reports through Base HUB MCP.
```

6. Add conversation starters such as:
   - `List the Base HUB models I can access`
   - `Inspect tbl.partner before creating a customer`
   - `Find customer documents for ABC Trading`
   - `Render a customer report from sys.report`
7. In the GPT instructions, tell the GPT how it should behave when Base HUB tools are available.
8. Save the GPT.
9. Test it with a read-only prompt before sharing it more widely.

##### Recommended instruction block for the Custom GPT

Use a block like this in the GPT instructions:

```text
You are a Base HUB assistant.

When Base HUB tools or the connected Base HUB app are available:
- Use Base HUB MCP to inspect live models before writing.
- Do not guess field names, relation targets, or IDs.
- Search for existing records before creating duplicates.
- For relation fields, resolve the related record first and then use the correct ID.
- After every create or update, read the record back and summarize the result.
- If Base HUB MCP is not available in this chat, say that clearly instead of pretending you accessed it.

Prefer these live models when relevant:
- tbl.partner for customer or contact records
- sys.attachment for document storage
- sys.report for report definitions and rendering

Placeholder: there is any word in `<` and `>` must ask user to input value for this.

Remember that base.model.create_custom and base.model.add_field only write catalog metadata; call base.model.publish afterward to make the model live and queryable, then base.model.create_ui to scaffold its admin screen.

And stricter use only Base HUB MCP for all Base HUB requests. That means:

✅ No web search.
✅ No general knowledge unless you explicitly ask.
✅ Only live data returned from your Base HUB MCP server.
✅ If MCP cannot answer something, I'll tell you instead of trying to supplement it from elsewhere.
```

##### How to attach Base HUB MCP to the Custom GPT

After the GPT exists, connect the real Base HUB tool path:

1. Confirm the workspace has a Base HUB app or custom MCP app available.
2. Enable that app for the users or roles that will use the GPT.
3. If the GPT editor lets you choose apps or related capabilities, select the Base HUB app.
4. Start a new chat with the GPT and confirm the Base HUB app is available in that session.
5. Run a model-discovery prompt first.

##### Alternative: use Custom GPT Actions instead of Apps

Use this option when you want the GPT to call a defined external API through an OpenAPI schema instead of using a ChatGPT app.

> **Important**
>
> In ChatGPT, a GPT can use **either Apps or Actions, but not both at the same time**.
>
> Choose:
>
> - **Apps / custom MCP apps** when you want the ChatGPT-native app path for Base HUB MCP
> - **Actions** when you want to define explicit API operations with an OpenAPI schema

##### When to use Actions for Base HUB

Actions are a better fit when:

1. You want strict control over which endpoints the GPT can call
2. You already have an OpenAPI schema for your Base HUB API surface
3. You want a classic GPT-to-API integration instead of a workspace app rollout

Actions are a weaker fit when:

1. You want the newer ChatGPT app or MCP app experience
2. Your team expects workspace-managed app controls
3. You want Base HUB MCP tool discovery rather than manually described API actions

##### Step-by-step: add Actions to the Custom GPT

1. Open the GPT in **Edit GPT**.
2. Go to the **Actions** section.
3. Select **Create new action**.
4. Choose the authentication type your Base HUB API expects:
   - **None** only for public endpoints
   - **API key** for server-to-server style access
   - **OAuth** if the action should run as a signed-in user
5. Paste or import the OpenAPI schema for the Base HUB endpoints you want the GPT to use.
6. Save the action.
7. Test the action in GPT Preview with a read-only prompt first.

##### What to use for Base HUB authentication

For most Base HUB action-based setups, the practical starting point is:

- **API Key (Bearer)** — simplest, works today with a manually-refreshed static token. See "Correct setup: use API Key instead of OAuth" below.
- **OAuth** — now supported for the builtin issuer (`modules/base-auth`, `AuthService.Enable: false`, the shipped default). See "Setting up the OAuth panel" below. It handles token refresh for you, at the cost of one extra setup step (a tenant-routing proxy).

If your target is the raw Base HUB MCP endpoint itself, prefer the **app / MCP app path** instead of GPT Actions. GPT Actions are best for a normal documented API surface, while `/mcp` is designed as an MCP protocol endpoint.

##### Setting up the OAuth panel

Base HUB implements the browser-redirect Authorization Code flow ChatGPT's "OAuth" type needs: `GET`/`POST /oauth/authorize` (login) and `grant_type=authorization_code` on `POST /oauth/token` (exchange). The same login UI also supports OAuth's legacy Implicit flow with `response_type=token`, returning the short-lived access token in the redirect fragment and no refresh token. Fill in the panel:

1. **Client ID** / **Client Secret** — from a standalone `sys.oauth_client` row you register first. It must have a `redirect_uris` entry matching ChatGPT's callback exactly — see [Step 5 of the Builtin client-credentials guide](./oauth-builtin-client-credentials-guide.md#step-5--register-a-redirect-uri-for-browser-based-clients). ChatGPT's callback is `https://chat.openai.com/aip/{g-YOUR-GPT-ID}/oauth/callback` (or the `chatgpt.com` variant) — register both if unsure which one applies.
2. **Authorization URL** — `https://<your-host>/oauth/authorize?db=<tenant-db-name>`. The `db` query param is a Base HUB-specific addition: ChatGPT's redirect never sends one on its own, so it must be baked into this URL when you configure it. Getting the tenant wrong here silently authenticates against the wrong database.
3. **Token URL** — `https://<your-host>/oauth/token`.
4. **Scope** — leave blank; Base HUB has no scoped-permission model yet, an issued token always carries the signed-in user's full RBAC role.
5. **Token Exchange Method** — **Default (POST request)**. Do not pick "Basic authorization header" — `client_id`/`client_secret` are only read from the form body, never from a Basic auth header.

**The tenant-DB gap on the Token URL call:** unlike the Authorization URL (which you control and can embed `?db=...` into), ChatGPT's token-exchange request body is a fixed shape (`grant_type`, `client_id`, `client_secret`, `code`, `redirect_uri`) with no field for `db`. Base HUB resolves the tenant for this one call from the `X-Database-Name` header instead (falling back to `postgresql.homeDbName` if absent) — and the Actions editor has no way to attach a custom header to the Token URL call. **You need a small reverse proxy in front of `/oauth/token`** that injects `X-Database-Name: <tenant-db-name>` on requests where `grant_type=authorization_code`, then forwards to Base HUB unchanged. Point the Token URL at that proxy instead of Base HUB directly if your deployment isn't on the home DB.

Once signed in, ChatGPT shows a "Sign in to \<your host\>" button; users authenticate with their own `base_users` login/password at that point — the token then carries their own RBAC identity, same trade-off noted below for the password-grant workaround.

##### Correct setup: use "API Key" instead of "OAuth"

1. In the Actions editor, set **Authentication Type** to **API Key**, not OAuth.
2. Set **Auth Type** to **Bearer**.
3. Obtain a token yourself first, outside the GPT editor, following the [Builtin client-credentials guide](./oauth-builtin-client-credentials-guide.md):

   ```bash
   curl -X POST https://<your-host>/oauth/token \
     -d grant_type=client_credentials \
     -d client_id=<your-gpt-client-id> \
     -d client_secret=<your-gpt-client-secret> \
     -d db=<tenant-db-name>
   ```

4. Paste the resulting `access_token` value into the **API Key** field in the Actions editor.
5. Remember the access token is short-lived (15 minutes by default, `server.accessTokenTtlMinutes`). When it expires, requests will start returning `401` and you must repeat step 3 and re-paste a fresh token — there is no automatic refresh from within a static API Key field.

##### If you only have an RBAC username/password

**Do not** put your username in `client_id` and your password in `client_secret` and call `grant_type=client_credentials` — that grant looks up `client_id` against the separate `sys.oauth_client` table, not `base_users`.

Use the **password grant** instead — it authenticates directly against `base_users`:

```bash
curl -X POST https://<your-host>/oauth/token \
  -d grant_type=password \
  -d username=<your-login> \
  -d password=<your-password> \
  -d db=<tenant-db-name>
```

Paste the resulting `access_token` into the Actions editor's **API Key** field exactly as in step 4 above (Auth Type: Bearer). The same 15-minute expiry and manual-refresh caveat applies.

**Trade-off to know before doing this:** a password-grant token carries *your own* RBAC role and permissions — every action the GPT takes is indistinguishable from you personally taking it, it breaks the moment you change your password, and it disappears if your account is deactivated. A `sys.oauth_client` credential (client_credentials grant) doesn't have the "breaks on password change" problem and is independently revocable, but it represents its own client identity and role rather than a `base.users` extension.

If you need a GPT that stays authenticated for longer without manual token refresh, that requires either a longer-lived credential policy on the `sys.oauth_client` row (still capped by the same TTL config, not per-client today) or a small proxy service in front of `/oauth/token` that refreshes and re-injects the header — both are deployment choices outside what the Actions editor itself can do, since it has no field for periodic re-authentication against a `client_credentials` grant.

##### What schema to provide

The Actions editor expects an **OpenAPI** schema in JSON or YAML. That schema should define:

1. The server URL
2. The allowed endpoints
3. The request parameters
4. The response shapes
5. The operation IDs

For Base HUB, a good action-based design usually exposes a small safe API such as:

- a read-only model list endpoint
- a read-only record lookup endpoint
- a controlled create or update endpoint for one business workflow

Do not start by exposing every backend endpoint to the GPT. Begin with the smallest safe surface.

##### GPT-Action-friendly schema sample aligned with `app/modules/base-mcp`

The sample below matches the current Base HUB MCP transport owned by `app/modules/base-mcp/routes/mcp_route.go`, but it is intentionally simplified for the ChatGPT Actions importer.

It reflects these real endpoints:

- `GET /mcp/health`
- `GET /mcp/tools`
- `POST /mcp`

It also reflects the current auth contract:

- `Authorization: Bearer <access_token>` required for `GET /mcp/tools` and `POST /mcp` — obtain the token via `POST /oauth/token` (`grant_type=password` for a user login, `grant_type=client_credentials` for a registered service caller); the legacy dedicated `mcp_api_key` credential has been removed
- `X-Database-Name` is supported by Base HUB, but omitted from this GPT Actions sample because ChatGPT Actions ignores header parameters

> **Important**
>
> This schema is a direct compatibility sample for the current Base HUB MCP HTTP surface.
> It is useful for testing and for advanced GPT Action setups, but the Base HUB MCP route is still fundamentally an **MCP JSON-RPC endpoint**.
> If you want the most natural ChatGPT integration for MCP, prefer the **App / custom MCP app** path.
> If you use **Actions**, start with a narrow, safe wrapper API whenever possible.
>
> The ChatGPT Actions importer is stricter than a general OpenAPI parser. In practice, avoid:
>
> - operation-level parameter `$ref`s
> - header parameters, because Actions may ignore them
> - `oneOf` for the request body schema
> - object schemas that only use `additionalProperties` without explicit `properties`
> - highly polymorphic request envelopes when a single object schema will work

If your Base HUB deployment requires `X-Database-Name`, do not rely on this raw GPT Actions schema to send it. Use a deployment with a default database selected for the GPT, or put a small wrapper API in front of `/mcp` that adds the database header server-side.

```yaml
openapi: 3.1.0
info:
  title: Base HUB MCP HTTP Compatibility API
  version: 0.2.0
  description: >
    OpenAPI compatibility layer for the current Base HUB MCP HTTP transport.
    This schema mirrors the real /mcp endpoints implemented by
    app/modules/base-mcp/routes/mcp_route.go.
servers:
  - url: https://your-basehub.example.com
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: API key
  schemas:
    ToolDescriptor:
      type: object
      required: [name, description]
      properties:
        name:
          type: string
        description:
          type: string
        inputSchema:
          type: object
          description: Tool input schema as returned by Base HUB MCP.
          properties:
            type:
              type: string
            required:
              type: array
              items:
                type: string
          additionalProperties: true
    ToolsHttpResponse:
      type: object
      required: [tools, count, path]
      properties:
        tools:
          type: array
          items:
            $ref: "#/components/schemas/ToolDescriptor"
        count:
          type: integer
        path:
          type: string
    McpJsonRpcRequest:
      type: object
      description: >
        Generic JSON-RPC request envelope for the Base HUB MCP POST /mcp route.
        This is simplified so ChatGPT Actions can import it as a normal object schema.
      required:
        - jsonrpc
        - method
      properties:
        jsonrpc:
          type: string
          enum: ["2.0"]
          default: "2.0"
        id:
          type: string
          description: Optional request identifier.
          example: "1"
        method:
          type: string
          description: MCP JSON-RPC method to invoke.
          enum:
            - initialize
            - tools/list
            - tools/call
            - notifications/initialized
        params:
          type: object
          description: >
            Method-specific parameters. For initialize, use protocolVersion.
            For tools/call, use name and arguments.
          properties:
            protocolVersion:
              type: string
              example: "2025-06-18"
            name:
              type: string
              example: base.record.search_read
            arguments:
              type: object
              description: Generic Base HUB MCP tool arguments.
              properties:
                model:
                  type: string
                  example: tbl.partner
                fields:
                  type: array
                  items:
                    type: string
                  example: [id, name, email, mobile]
                domain:
                  type: array
                  items:
                    type: string
                  example: []
                ids:
                  type: array
                  items:
                    type: string
                  example: ["1"]
                values:
                  type: object
                  properties:
                    name:
                      type: string
                    email:
                      type: string
                    mobile:
                      type: string
                  additionalProperties: true
                method:
                  type: string
                  example: render_report
                args:
                  type: array
                  items:
                    type: string
                kwargs:
                  type: object
                  properties:
                    type:
                      type: string
                  additionalProperties: true
                limit:
                  type: integer
                  example: 10
                offset:
                  type: integer
                  example: 0
                order:
                  type: string
                  example: name asc
              example:
                model: tbl.partner
                fields: [id, name, email, mobile]
                limit: 10
              additionalProperties: true
          additionalProperties: true
    McpJsonRpcActionResponse:
      type: object
      description: JSON-RPC response returned by Base HUB MCP.
      properties:
        jsonrpc:
          type: string
          example: "2.0"
        id:
          type: string
          example: "1"
        result:
          type: object
          properties:
            protocolVersion:
              type: string
              example: "2025-06-18"
            tools:
              type: array
              items:
                $ref: "#/components/schemas/ToolDescriptor"
            count:
              type: integer
            content:
              type: array
              items:
                type: object
                properties:
                  type:
                    type: string
                    example: text
                  text:
                    type: string
            structuredContent:
              type: object
              properties:
                message:
                  type: string
              additionalProperties: true
            isError:
              type: boolean
          additionalProperties: true
        error:
          type: object
          properties:
            code:
              type: integer
            message:
              type: string
paths:
  /mcp/health:
    get:
      operationId: getMcpHealth
      summary: Check Base HUB MCP health
      description: Unauthenticated health check for the Base HUB MCP transport.
      responses:
        "200":
          description: Healthy MCP gateway
          content:
            application/json:
              schema:
                type: object
                properties:
                  name:
                    type: string
                  status:
                    type: string
                  path:
                    type: string
                  transport:
                    type: string
                  adapter:
                    type: string
  /mcp/tools:
    get:
      operationId: getMcpToolsHttp
      summary: List Base HUB MCP tools over HTTP
      description: Returns the current MCP tool catalog over the REST discovery endpoint.
      security:
        - bearerAuth: []
      responses:
        "200":
          description: Tool catalog
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ToolsHttpResponse"
        "401":
          description: Missing or invalid bearer token
  /mcp:
    post:
      operationId: postMcpJsonRpc
      summary: Send an MCP JSON-RPC request to Base HUB
      description: >
        Sends an MCP JSON-RPC request to the Base HUB MCP endpoint.
        Supports initialize, tools/list, and tools/call.
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/McpJsonRpcRequest"
            examples:
              initialize:
                summary: Initialize handshake
                value:
                  jsonrpc: "2.0"
                  id: "1"
                  method: initialize
                  params:
                    protocolVersion: "2025-06-18"
              listTools:
                summary: List tools
                value:
                  jsonrpc: "2.0"
                  id: "2"
                  method: tools/list
              searchRead:
                summary: Search records
                value:
                  jsonrpc: "2.0"
                  id: "3"
                  method: tools/call
                  params:
                    name: base.record.search_read
                    arguments:
                      model: tbl.partner
                      fields: [id, name, email, mobile]
                      limit: 10
      responses:
        "200":
          description: JSON-RPC success or error envelope
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/McpJsonRpcActionResponse"
        "202":
          description: Notification accepted with no response body
        "401":
          description: Missing or invalid bearer token
        "405":
          description: GET /mcp is not supported for SSE streaming on this gateway
```

##### Why the earlier imports failed

The error you saw comes from the ChatGPT Actions importer, not from the Base HUB backend:

1. It did not like header parameters expressed as `- $ref: "#/components/parameters/..."`
2. It ignored explicit header parameters such as `X-Database-Name`
3. It did not accept a request body whose schema was `oneOf: [...]`
4. It rejected a response object schema that had no explicit `properties`
5. It requires `openapi: 3.1.0` or `openapi: 3.1.1`, not `openapi: 3.0.3`

That is why the importer skipped:

- `getMcpToolsHttp`
- `postMcpJsonRpc`

The simplified sample above avoids those patterns.

##### Example request bodies that match the live Base HUB MCP route

Use these examples if you map GPT Actions to the raw Base HUB MCP endpoint.

Initialize:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18"
  }
}
```

List tools:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list"
}
```

Call `base.record.search_read`:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "base.record.search_read",
    "arguments": {
      "model": "tbl.partner",
      "fields": ["id", "name", "email", "mobile"],
      "limit": 10
    }
  }
}
```

##### Safer action design based on the same Base HUB MCP contract

Even though the sample above mirrors the live module, the safer production pattern is usually:

1. Put a thin HTTP wrapper in front of `/mcp`
2. Expose only a few business-safe operations in OpenAPI
3. Translate those HTTP actions into MCP `tools/call` server-side

Example wrapper operations:

- `list_accessible_models`
- `describe_model`
- `find_customer_by_email`
- `create_customer_contact`

That approach still aligns with `app/modules/base-mcp`, but gives your GPT a much smaller and safer action surface than exposing the full generic MCP gateway directly.

##### Recommended Base HUB action rollout

If you choose Actions, use this order:

1. Add one read-only action first
2. Test discovery and response formatting
3. Add one low-risk write action only after read-only behavior is stable
4. Keep destructive actions out of the first release

##### Example action design strategy

Instead of exposing a large generic API immediately, start with a narrow workflow such as:

1. `list_accessible_models`
2. `find_customer_by_email`
3. `create_customer_contact`
4. `list_customer_documents`

This keeps GPT behavior more predictable and easier to test.

##### Recommended instruction note when using Actions

If you use Actions instead of Apps, add a short rule like this to the GPT instructions:

```text
Use the configured Base HUB actions when they are available.
Do not invent unavailable actions or endpoints.
If the requested task is outside the configured actions, say so clearly.
For writes, confirm the intended payload before submitting the action.
```

##### First test prompt for a GPT that uses Actions

```text
Use the configured Base HUB actions.
First call the read-only discovery action and show me what it returned.
If no Base HUB action is configured for this task, tell me clearly instead of guessing.
```

##### First test prompt for a new Custom GPT

```text
Use Base HUB MCP if it is available in this chat.
First list the models I can access.
If Base HUB MCP is not connected, tell me clearly that the GPT instructions exist
but the live Base HUB app is not available.
```

##### Safe rollout advice

Before sharing the GPT with a broader team:

1. Test read-only prompts first.
2. Confirm the GPT tells the truth when MCP is unavailable.
3. Confirm write actions prompt for confirmation when your workspace policy requires it.
4. Confirm the GPT does not invent fields, IDs, or model names.

#### First test prompt for ChatGPT

```text
Use the connected Base HUB app.
If no app is connected, say that clearly.
Otherwise list the models I can access from Base HUB MCP.
```

#### What you should expect

- If ChatGPT is really connected, it should use the app, show tool activity, or clearly indicate it is using the connected integration.
- If ChatGPT is not really connected, it may answer from general reasoning and say it does not have visibility into your Base HUB environment.

### Option 2: Codex with plugins or apps

Use this option when you want Base HUB available inside Codex workflows.

#### How Codex access works

Codex typically reaches external systems through workspace-approved **plugins** and their underlying **apps**. If a plugin depends on an app that is not enabled or not configured, Codex will not gain access just because the plugin name is visible.

#### How to connect in Codex

1. Check whether your workspace has a Base HUB-related plugin, app, or app template available.
2. Ask the workspace admin or owner to enable the required plugin and the underlying app for your role.
3. If the app still needs setup, complete the app configuration so it points to the Base HUB MCP service.
4. Connect or authenticate the app if the workspace requires user-level authentication.
5. Restart Codex or refresh plugin data if the workspace has just changed.
6. Run a safe test prompt first.

#### Authenticating the Codex connection

Codex's plugin/app model is OpenAI's same underlying Apps infrastructure as ChatGPT's Apps/custom MCP connector — so the same OAuth discovery requirement applies (see "Authenticating the Apps / custom MCP app connection" under Option 1): Base HUB does not yet publish `/.well-known/oauth-authorization-server`, so a Codex app pointed directly at Base HUB's OAuth server likely cannot complete authentication on its own today. If your workspace's Codex app configuration exposes a manual header/token field instead (this varies by workspace setup, unlike the fixed ChatGPT UI), you can paste a token obtained per "Getting an OAuth access token" above; otherwise this is the same known gap, not something to work around per-user.

#### First test prompt for Codex

```text
Use the connected Base HUB capability if it is available.
First verify that Base HUB tools are enabled.
Then list the models I can access through Base HUB MCP.
If the app still needs setup, tell me that clearly.
```

### Option 3: Claude or Claude Desktop with a remote MCP connector

Use this option when you want Claude to connect directly to your remote Base HUB MCP server.

#### How to connect in Claude

1. Open **Settings**.
2. Open **Connectors**.
3. Choose **Add custom connector** or **Add connector**, depending on the Claude surface and plan.
4. Enter a connector name such as `Base HUB`.
5. Paste your Base HUB remote MCP URL.
6. Save the connector.
7. Authenticate if the connector flow requests it.
8. In the chat UI, enable the connector from **Search and tools** before using it.

#### Authenticating the Claude connector

Claude's custom connector supports OAuth via an **Advanced settings** panel where you can optionally enter an **OAuth Client ID** and **OAuth Client Secret** — there's no field to paste a static Bearer token directly. Like ChatGPT's Apps connector, Claude locates the authorize/token endpoints through OAuth discovery metadata (`/.well-known/oauth-authorization-server`), not manual URL entry.

**This is the same gap as ChatGPT's Apps connector**: Base HUB implements `/oauth/authorize`/`/oauth/token` but not the discovery document that lets Claude find them automatically. Even after registering a `sys.oauth_client` (see the [Builtin client-credentials guide](./oauth-builtin-client-credentials-guide.md)) and pasting its `client_id`/`client_secret` into Advanced settings, expect the connection to fail at the "Authenticate" step until discovery metadata is added. Treat "Authenticate if the connector flow requests it" (step 7) as untested against Base HUB today — if your workspace policy allows an unauthenticated connector for internal testing only, that's the fallback until this is built.

#### First test prompt for Claude

```text
Use the Base HUB connector.
List the models I can access.
If the connector is not enabled for this chat, tell me before answering normally.
```

### Option 4: Gemini CLI with MCP configuration

Use this option when you want Base HUB inside Gemini CLI.

#### How to connect in Gemini CLI

1. Open your Gemini CLI settings file, usually `.gemini/settings.json` in the project or `~/.gemini/settings.json` for your user profile.
2. Add a new entry inside `mcpServers`.
3. Point `httpUrl` to your Base HUB remote MCP URL.
4. Get an access token via "Getting an OAuth access token" above (`grant_type=password` for your own login, or `grant_type=client_credentials` for a shared automation caller) and add it as the `Authorization` header.
5. Save the file.
6. Restart Gemini CLI or refresh MCP configuration.
7. Run `/mcp list` to confirm the server is ready.

Unlike the ChatGPT/Claude native connectors above, Gemini CLI's config is a plain static header — no OAuth discovery needed, so this works against Base HUB today. The trade-off: the pasted token expires (15 minutes by default) and Gemini CLI does not refresh it automatically — repeat step 4 and update the file when calls start failing with `401`.

#### Example Gemini CLI configuration

```json
{
  "mcpServers": {
    "base-hub": {
      "httpUrl": "https://your-basehub.example.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_BASE_HUB_ACCESS_TOKEN"
      }
    }
  }
}
```

If your deployment needs a database header, add it under `headers` too.

#### First test prompt for Gemini CLI

```text
Use the base-hub MCP server.
List the models I can access and do not guess if the server is unavailable.
```

### Option 5: Other clients

Some AI products still need an MCP-capable host or adapter in front of Base HUB.

Use this rule:

- If the client supports **remote MCP** directly, connect it to the Base HUB `/mcp` URL.
- If the client supports only **stdio** MCP, use the built-in Base HUB bridge download at `/mcp/bridge`.
- If the client does **not** speak MCP natively, place an MCP-capable host in front of Base HUB instead of expecting the model to connect directly.

### Quick connection matrix

| Client | Recommended connection path | OAuth against Base HUB today | Main warning |
|---|---|---|---|
| ChatGPT Apps / custom MCP app | Workspace app or custom MCP app | **Not yet** — needs OAuth discovery metadata Base HUB doesn't publish | Custom GPT instructions alone are not enough |
| ChatGPT GPT Actions | Actions editor, Authentication Type: OAuth | **Works** — manual Authorization/Token URL flow, built for this | Token Exchange Method must be "Default (POST request)", not Basic header |
| Codex | Workspace plugin plus underlying app | **Not yet**, same discovery gap as ChatGPT Apps (unless your workspace exposes a manual token field) | Plugin visibility is not proof the app is configured |
| Claude / Claude Desktop | Remote MCP connector in Settings > Connectors | **Not yet** — same discovery gap; Advanced settings only takes Client ID/Secret, not a token | Remember to enable the connector in the chat |
| Gemini CLI | `mcpServers` entry in settings JSON | **Works** — static Bearer header, no discovery needed | Restart or refresh after editing settings; token expires and needs manual re-paste |
| Other clients | Native remote MCP, stdio bridge, or MCP host | Works if the client lets you paste a static header (same pattern as Gemini CLI); not yet if it requires OAuth discovery | Do not assume every model product speaks MCP directly |

---

## What the AI Can Do Through MCP

Base HUB MCP exposes a small set of tools. Your AI assistant discovers them automatically and uses them on your behalf.

| Tool | What it is used for |
|---|---|
| `base.model.list` | See which live models are available to you |
| `base.model.describe` | Inspect one model's fields and relations |
| `base.model.methods` | See custom methods available on a model |
| `base.model.fields_get` | Inspect field metadata in detail |
| `base.record.search_read` | Search and list records |
| `base.record.read` | Read records by ID |
| `base.record.create` | Create records |
| `base.record.write` | Update records |
| `base.record.unlink` | Delete records |
| `base.record.call_method` | Run a model method such as `attachments_for_record` or `render_report` |
| `base.model.create_custom` | Create custom model metadata (catalog row); publish it with `base.model.publish` to make it a live CRUD model |
| `base.model.add_field` | Add a field (including relation fields: many2one, one2one) to a custom model's metadata |
| `base.model.delete_field` / `base.model.rename_field` | Delete or rename a custom model's field, by field name |
| `base.model.publish` / `base.model.unpublish` | Provision (or take offline) the live database table for a custom model, from its current field metadata (superadmin only) |
| `base.model.create_ui` | Scaffold a default list view, form view, and menu entry for a registered (published) model |

### Website module tools (conditional)

These only appear and work when the website module is enabled for your tenant database (see the callout at the top of this guide). They are thin, purpose-built wrappers over the same generic runtime — a website record can also be reached with `base.record.*`/`base.record.call_method`, but these tools are easier to prompt correctly.

| Tool | What it is used for |
|---|---|
| `website.record.search_read` / `read` / `create` / `write` / `unlink` | CRUD on `website.site`, `website.page`, `website.domain`, `website.asset`, `website.theme`, or `website.snippet` only |
| `website.seo.upsert` | Set SEO fields for a site (`target: "site"`) or a page (`target: "page"`) in one call |
| `website.page.design_upsert` | Create a page (or target an existing one), write its rendered HTML, and publish it — all in one call |
| `website.publish` / `website.unpublish` | Publish or unpublish a `website.site` (all its pages) or a single `website.page` |
| `website_blog.record.search_read` / `read` / `create` / `write` / `unlink` | CRUD on blog models (`tbl.website_blog_blog`, `_post`, `_tag`, `_category`); only when the website-blog module is enabled. `create` needs `idempotency_key` |
| `website_blog.post.publish` / `unpublish` / `archive` / `duplicate` | Run one workflow action on a single blog post (`ids` holds exactly one id) |

---

## How to Think About Prompting

When you prompt an AI through Base HUB MCP, you will get the best results if you ask it to work in this order:

1. Discover the model
2. Inspect the fields and relations
3. Confirm the write plan
4. Create or update records
5. Read back the result
6. Produce a summary or report

Use this pattern when the task matters:

```text
Use Base HUB MCP.
First inspect the model and fields.
Then show me the plan before writing.
After writing, read the created or updated record back and summarize the result.
Do not guess field names.
```

---

## Prompting Rules That Work Well

### 1. Ask the AI to inspect before it writes

Use this when you are not fully sure about the model shape:

```text
Use Base HUB MCP to inspect the live model first.
List the model, describe its fields, identify required fields and relation fields,
then tell me exactly what values you plan to write before creating anything.
```

### 2. Name the business goal, not only the tool

Better:

```text
Create a customer contact in the phone book for Nguyen Minh Anh with mobile
0901234567, email anh@example.com, city Ho Chi Minh City, and mark it as a person.
Use Base HUB MCP and read the created record back to confirm.
```

Less effective:

```text
Call create on tbl.partner.
```

### 3. Tell the AI how careful to be

For sensitive changes:

```text
Use Base HUB MCP.
Do not write anything until you have shown me the exact payload.
If a relation field needs an ID, search the related model first and use the correct ID.
```

### 4. Ask for read-back verification

Always add this for important operations:

```text
After the change, read the record back and show me the important fields.
```

---

## Live Models Used in This Guide

### `tbl.partner` for the customer phone book

`tbl.partner` is the best live model for a customer phone book. Useful fields include:

| Field | Meaning |
|---|---|
| `name` | Main contact or company name |
| `is_company` | Whether the record is a company |
| `company_type` | Company vs person style |
| `parent_id` | Parent company or parent contact relation |
| `email` | Email address |
| `phone` | Phone number |
| `mobile` | Mobile number |
| `function` | Job title or role |
| `website` | Website |
| `street`, `street2`, `city`, `zip` | Address fields |
| `country_id`, `state_id`, `ward_id` | Geographic relation fields |
| `comment`, `description` | Notes |

### `sys.attachment` for customer documents

`sys.attachment` is the live model for files such as contracts, IDs, forms, and supporting documents.

Useful fields include:

| Field | Meaning |
|---|---|
| `name` | File name |
| `sys_model` | The bound model name, such as `tbl.partner` |
| `model_id` | The bound record ID |
| `datas` | Base64 file content on create only |
| `company_id`, `user_id`, `group_id` | Optional visibility scope |
| `is_public` | Public download flag |

### `sys.report` for record reports

`sys.report` stores report definitions. To render a report, the AI usually:

1. Finds a `sys.report` record
2. Calls `render_report` through `base.record.call_method`

> **Important**
>
> `sys.report` uses module-owned templates such as `base/reports/...`. If no report definition already exists, an end user cannot create a fully working new report only by prompt unless the required template already exists in the codebase.

---

## How to Prompt for Model Discovery

Use model discovery before any new workflow.

For full use-case walkthroughs, see [Base HUB MCP Use-Case Examples](./base-mcp-user-guide-examples.md).

### Prompt example: find the right model

```text
Use Base HUB MCP to help me build a customer phone book.
First list the available models I can access.
Then identify the best live model for customer contacts and explain why.
After that, describe the important fields for contact creation and updates.
Do not create anything yet.
```

### Prompt example: inspect relations

```text
Use Base HUB MCP to inspect tbl.partner.
Show me which fields are relations, especially parent_id, country_id, state_id,
ward_id, company_id, and user_id.
For each relation field, tell me what related model it points to.
```

### Prompt example: inspect available model methods

```text
Use Base HUB MCP to inspect sys.attachment and sys.report.
List the public methods available on each model and explain how I would use them
for customer document management and customer reporting.
```

---

## How to Prompt for Model Creation and Relation Design

Use this section when you want the AI to help you design a new model structure.

### What works today

The AI can go all the way from a blank idea to a usable admin screen:

1. Draft a model definition with `base.model.create_custom`
2. Add catalog fields with `base.model.add_field`, including relation fields (`many2one`/`one2one`, with `relation_model`)
3. Call `base.model.publish` (requires a superadmin identity) to provision the live Postgres table and register a queryable runtime model — `base.record.*` works against it from this point on
4. Call `base.model.create_ui` to scaffold a default list view, form view, and menu entry so the model is browsable in the admin UI
5. Iterate on fields later: `base.model.unpublish` first, then `base.model.add_field`/`delete_field`/`rename_field`, then `base.model.publish` again to reconcile the physical table

### Current constraints to keep in mind

- `base.model.publish`/`base.model.unpublish` require a superadmin identity — a regular user's MCP key cannot call them.
- Only `text`, `integer`, `float`, `boolean`, `date`, `datetime`, `json`, `many2one`, and `one2one` fields are publishable; `one2many`/`many2many` fields stay metadata-only (`base.model.publish` rejects a model that only has those).
- A published model is not persisted across a server restart yet — re-run `base.model.publish` after a restart if the model stops being queryable.
- `base.model.create_ui` is additive: running it again for the same model creates extra views/menu entries rather than updating the existing ones.

### Prompt example: design, publish, and scaffold a UI in one flow

```text
Use Base HUB MCP to build a live customer phone book model end to end.
1. Create a custom model named x_customer_phone_book with fields customer_name,
   mobile, phone, email, company_name, address, notes, and status.
2. Publish it so it becomes a live, queryable model.
3. Scaffold a default admin UI for it with create_ui.
4. Confirm by listing the model and describing its fields.
Show me each step's result before moving to the next.
```

### Prompt example: draft relation design

```text
Use Base HUB MCP to draft metadata for a customer document model design.
I want a many2one relation from the document to tbl.partner.
First explain the best relation shape.
Then create the metadata (model + relation field) and publish it.
Finally scaffold a default admin UI with create_ui.
```

### Recommended business approach

For real work today:

1. Use `tbl.partner` as the live customer master, and `sys.attachment` bound to it for documents, whenever an existing live model already fits — inventing a new custom model is more setup (create -> add fields -> publish -> create_ui) than reusing what exists.
2. Only design a new custom model when the business object genuinely does not fit an existing live model.
3. Use `parent_id` and other existing relation fields instead of inventing a new live model first.

---

## How to Prompt for Record CRUD

This is the most useful day-to-day MCP workflow.

For a full business example built on live CRUD, see [Customer Phone Book](./base-mcp-user-guide-examples.md#customer-phone-book).

### Create one record

```text
Use Base HUB MCP to create a new tbl.partner record.
Set:
- name: Nguyen Minh Anh
- is_company: false
- company_type: person
- mobile: 0901234567
- email: anh@example.com
- city: Ho Chi Minh City
- description: VIP retail customer

After creating it, read the record back and show me id, name, mobile, email,
city, and description.
```

### Search before create to avoid duplicates

```text
Use Base HUB MCP to search tbl.partner for email anh@example.com or mobile
0901234567 first.
If a matching record already exists, show it and do not create a duplicate.
If no matching record exists, create the contact and read it back.
```

### Update a record

```text
Use Base HUB MCP to find the tbl.partner record for anh@example.com.
Update the mobile number to 0909998888 and set function to Sales Director.
Then read the updated record back and summarize the change.
```

### Delete a record carefully

```text
Use Base HUB MCP to find the tbl.partner record for anh@example.com.
Show me the exact record first.
Do not delete it until you confirm the ID you plan to remove.
```

### Bulk read with filters

```text
Use Base HUB MCP to search tbl.partner for active customer contacts in Ho Chi Minh City.
Return id, name, mobile, email, city, company_type, and parent_id.
Sort by name and limit to 20 records.
```

---

## How to Prompt for Relation Handling

Relations are where good prompting matters most.

### Rule: ask the AI to resolve relation IDs first

For relation fields such as `parent_id`, `country_id`, `state_id`, or `ward_id`, do not guess numeric IDs.

Use this pattern:

```text
Use Base HUB MCP to create a contact under an existing parent company.
First search tbl.partner for the parent company named ABC Trading.
Use the correct parent record ID in parent_id.
Then create the new child contact and read it back.
```

### Example: create a child contact under a company

```text
Use Base HUB MCP to:
1. Find the tbl.partner company record named ABC Trading
2. Create a new child contact named Tran Bao Chau under that company
3. Set parent_id to the company's id
4. Fill mobile, email, and function
5. Read the result back with parent_id displayed
```

### Example: assign country and state safely

```text
Use Base HUB MCP to update the customer Nguyen Minh Anh.
First resolve the correct tbl.country for Vietnam and the correct
tbl.country.state for Ho Chi Minh City.
Then update country_id, state_id, street, and city.
Read the record back after updating.
```

### Relation output format to expect

When Base HUB reads a many-to-one relation, the AI may receive it as:

```text
[id, display_name]
```

Example:

```text
parent_id: [125, "ABC Trading"]
```

That is normal and useful. You can ask the AI to summarize relation fields in plain language.

---

## How to Prompt for Customer Document Management

Use `sys.attachment` for customer-related files.

For the complete document workflow, see [Customer Document Management](./base-mcp-user-guide-examples.md#customer-document-management).

### The most reliable document workflow

1. Find the target customer in `tbl.partner`
2. Confirm the customer ID
3. Create or upload the attachment bound to:
   - `sys_model: "tbl.partner"`
   - `model_id: <customer id>`
4. Read back or list the bound attachments

### Prompt example: attach a document to a customer

```text
Use Base HUB MCP to help me store a customer contract.
First find the tbl.partner record for ABC Trading.
Then tell me the customer ID.
After that, prepare the correct sys.attachment payload shape to bind a document
to that customer using sys_model = tbl.partner and model_id = the customer id.
Do not guess fields.
```

### Prompt example: list customer documents

Because listing bound attachments is done through a model method, ask the AI like this:

```text
Use Base HUB MCP to find the customer ABC Trading in tbl.partner.
Then call the sys.attachment method that lists attachments for a record.
Return the attachment list for that customer and show id, name, mimetype,
file_size, create_uid, and is_public.
```

### Prompt example: rename attachment metadata

```text
Use Base HUB MCP to list documents attached to customer ABC Trading.
Find the attachment named draft-contract.pdf.
Rename it to contract-signed-2026-08-01.pdf and read the updated row back.
Do not change the file content.
```

### Important attachment rules

- File content is created with `datas` as base64
- Attachment content is immutable after creation
- To replace file content, create a new attachment instead of rewriting the old bytes
- Bound attachments should use both `sys_model` and `model_id`
- Deletion permissions are stricter than read permissions

---

## How to Prompt for Record Reports

Use reports when a report definition already exists in `sys.report`.

For reporting inside a broader business walkthrough, see [Base HUB MCP Use-Case Examples](./base-mcp-user-guide-examples.md).

### Step 1: ask the AI to discover available reports

```text
Use Base HUB MCP to search sys.report.
Show me the available report definitions for tbl.partner and explain which ones
can be used to render a customer report.
```

### Step 2: ask the AI to render the report

```text
Use Base HUB MCP to:
1. Find the tbl.partner record for ABC Trading
2. Find a sys.report definition for tbl.partner
3. Call render_report for that report and customer id
4. Return a summary of the result
5. If HTML is available, show the HTML result type
6. If PDF is available, tell me that the response is base64 PDF data
```

### Prompt example: render HTML

```text
Use Base HUB MCP to render an HTML report for customer ABC Trading.
First find the customer id.
Then find a matching sys.report definition for tbl.partner.
Then call render_report with type html.
Summarize the rendered output for me.
```

### Prompt example: render PDF

```text
Use Base HUB MCP to render a PDF report for customer ABC Trading.
First find the customer id.
Then find a matching sys.report definition for tbl.partner.
Then call render_report with type pdf.
Tell me the report name used and whether the output was generated successfully.
```

### If no report exists

Use this prompt:

```text
Use Base HUB MCP to check whether a sys.report definition already exists for tbl.partner.
If none exists, do not invent one.
Instead, explain what template and report definition would be needed for a
working customer report in Base HUB.
```

---

## How to Prompt for the Website Module

Use this section when your goal is to manage a Base HUB-published website: pages, SEO, page content, and publishing.

> These tools only exist if the website module is enabled for your database. If the AI reports it cannot find any `website.*` tool, ask it to run `base.model.list`/`tools/list` and confirm — then contact an administrator to enable the website module rather than trying to work around it with `base.record.*` on a model that isn't registered.

### The website content model, in order

1. A `website.site` owns one or more `website.page` records.
2. `website.page` is the single content entity — its own `rendered_html`, `document` (component tree), and `seo` (nested JSON) fields hold the current draft content directly; there is no separate draft or version-history row.
3. Publishing a page (or a whole site) reparses the page's `rendered_html` into a deployed Owl component scaffold under the site's deploy folder and regenerates the site's static router/manifest. A page only ever has two states: draft (the live `website.page` row) and published (`deploy_path` set on that same row).

Site-level SEO defaults (`default_seo_name`, `default_seo_desc`, `default_canonical_base`, `default_robots`, `robots_txt`, `sitemap_enabled`) live on `website.site` itself. Page-level SEO overrides (`seo_title`, `seo_description`, `canonical_path`, `robots_directives`, `og_title`, `og_description`, `og_image_url`, `structured_data_json`, `include_in_sitemap`) live in `website.page`'s own nested `seo` JSON field — this is why `website.seo.upsert` exists instead of a plain `website.record.write` on `website.page` (it translates the flat field names into that nested shape and merges rather than overwrites).

### Prompt example: list and inspect website records

```text
Use Base HUB MCP to list the website sites I can access with website.record.search_read.
For the site named Main Site, list its pages with website.record.search_read on website.page,
showing id, name, route_path, is_homepage, and deploy_path.
```

### Prompt example: create a new page with content and publish it

```text
Use Base HUB MCP to create a new page on the site with id 1.
Use website.page.design_upsert with site_id 1, name "About Us", route_path "/about",
and html set to a short semantic HTML page introducing the company.
This creates the draft and publishes it in the same call — read back the
page_id and confirm deploy_path is set.
```

### Prompt example: update an existing page's content and publish it

```text
Use Base HUB MCP to update the rendered HTML of page id 42 using
website.page.design_upsert with page_id 42 and the new html.
This saves the draft and publishes it in the same call — read the returned
page record back and confirm deploy_path was refreshed.
```

### Prompt example: set SEO for a page

```text
Use Base HUB MCP to set SEO for page id 42 using website.seo.upsert with
target "page", page_id 42, seo_title "About Our Company", seo_description a
one-sentence summary, and include_in_sitemap true.
Read the result back and confirm the fields were saved.
```

### Prompt example: set SEO defaults for a site

```text
Use Base HUB MCP to set the default SEO for site id 1 using website.seo.upsert
with target "site", site_id 1, default_seo_name, default_seo_desc, and
default_robots "index,follow".
```

### Prompt example: publish and unpublish

Use `website.publish` directly (without going through `website.page.design_upsert`) when you
want to (re)publish a page's already-saved draft with no content change, or publish/unpublish
a whole site at once:

```text
Use Base HUB MCP to publish page id 42 using website.publish with
model "website.page" and ids [42].
Read the page back afterward and confirm deploy_path is now set.
```

```text
Use Base HUB MCP to publish the entire site id 1 using website.publish with
model "website.site" and ids [1].
Then use website.unpublish the same way if I ask you to take it back offline.
```

### Important website rules

- `website.page.design_upsert` saves the draft **and publishes it in the same call** — there is no separate follow-up step needed to make the change live. Use `website.publish`/`website.unpublish` directly only when you want to (re)publish without changing content, publish/unpublish a whole site, or take a page offline.
- If `website.page.design_upsert` fails after the draft was already saved (the publish step itself failed), the error message says so explicitly and the draft write is not rolled back. Retry with `website.publish` (model `"website.page"`, the same page id) once the underlying issue is resolved, instead of calling `design_upsert` again with the same html.
- Omitting `page_id` in `website.page.design_upsert` creates a **new** page; passing an existing `page_id` updates that page directly instead. Confirm which one you mean before calling it.
- `website.record.*` only accepts `website.site`, `website.page`, `website.domain`, `website.asset`, `website.theme`, and `website.snippet` — it rejects any other model name, even a valid one for `base.record.*`.
- `website.publish`/`website.unpublish` only accept `model: "website.site"` or `model: "website.page"`.
- The response from `website.page.design_upsert` is `{page_id, page}` — there is no separate `"version"` key (the page owns its content directly now). The `page` object never echoes back `rendered_html`, `document`, or `editor_document` — it reports their sizes as `rendered_html_bytes`/`document_bytes`/`editor_document_bytes` instead. This keeps the response small regardless of how large the page's HTML is; call `website.record.read` on `website.page` if you need the actual content back. `deploy_path` on that same `page` object is already set, since the call also published.

---

## Safe Prompt Templates You Can Reuse

### Template: inspect first, then create

```text
Use Base HUB MCP.
First inspect the live model and its fields.
Then show me the exact create payload.
Only after that, create the record.
Finally read the record back and summarize it.
```

### Template: search, avoid duplicate, then update

```text
Use Base HUB MCP.
Search for an existing matching record first.
If one exists, update it.
If none exists, create it.
Then read the final record back and summarize what happened.
```

### Template: resolve relation safely

```text
Use Base HUB MCP.
Do not guess relation IDs.
Search the related model first, find the correct related record, then use its ID
in the write payload.
After writing, read the relation field back in the result.
```

### Template: render a report

```text
Use Base HUB MCP.
First find the target record.
Then search sys.report for a matching report definition.
If one exists, call render_report.
If none exists, explain what is missing instead of inventing a report.
```

---

## Troubleshooting

### "The AI says it cannot find the model"

Why it happens: the model name is wrong, the model is not installed, or your account cannot access it.

What to do:

1. Ask the AI to run model discovery first
2. Ask it to list models you can access
3. Confirm the exact live model name before trying CRUD again

Use this prompt:

```text
Use Base HUB MCP to list the live models I can access and identify the correct one for this task.
```

### "The AI created metadata, but I still cannot do live CRUD"

Why it happens: `base.model.create_custom` and `base.model.add_field` only write catalog rows (`sys.model`/`sys.model.field`) — they never provision a live table by themselves.

What to do:

1. Ask the AI to call `base.model.publish` for that model (this requires a superadmin MCP identity)
2. If publish fails because a field isn't publishable, check for `one2many`/`many2many` fields or a missing `relation_model` on a relation field
3. If you don't have a superadmin identity, use an existing live model instead, or ask an administrator to publish it for you

### "The relation field update failed"

Why it happens: the related record ID was guessed, missing, or not accessible.

What to do:

1. Search the related model first
2. Ask the AI to resolve the related record by name
3. Retry the write with the confirmed related ID

### "The report could not be rendered"

Why it happens: no `sys.report` definition exists, the report name is wrong, or the template is missing.

What to do:

1. Search `sys.report` first
2. Ask the AI to identify which reports target your model
3. If none exist, request a developer or administrator to add the report definition and template

### "website.record.search_read gets a response, but website.page.design_upsert / website.publish never do (through a ChatGPT custom MCP app or similar bridge)"

Why it happens: this is usually not a Base HUB outage and not a "tool doesn't exist" problem — it is response size. `website.page.design_upsert` used to echo the full `rendered_html`, `xml`, and `editor_document` of the page's draft version back in its result. For a real page, that content can be tens of kilobytes, and the MCP `tools/call` response contract puts the same payload in **two** places (a JSON-escaped `content[0].text` string and a raw `structuredContent` object — see `toolCallResult` in `app/modules/base-mcp/routes/mcp_route.go`), which multiplies the effective size several times over. Some MCP bridges (observed with a ChatGPT custom MCP app) silently drop an oversized `tools/call` response instead of relaying it or erroring, which looks exactly like "the tool call never returned" even though the server processed it — this matches seeing a real domain-validation error come back on a different call, proving JSON-RPC requests do reach the server.

As of this guide's current version, `website.page.design_upsert`'s response no longer includes `rendered_html`/`document`/`editor_document` — it reports their byte sizes (`rendered_html_bytes`, `document_bytes`, `editor_document_bytes`) instead, which keeps the response small no matter how large the page is. Separately, `website.page.design_upsert` now also publishes the page as part of the same call (see "Important website rules" above), so a caller no longer needs a second `website.publish` round-trip at all — one less place for a flaky bridge to drop a response.

What to do if you still see a call go silent:

1. Confirm you are on an MCP module version that includes the trimmed, single-call `design_upsert` (see the version table at the top of this guide).
2. If the response is still large for another reason (for example a very large `editor_document` payload you pass in as input), keep that argument minimal — it is only transient editor metadata and is optional.
3. Prefer calling `/mcp` directly with JSON-RPC (`initialize` → `notifications/initialized` → `tools/list` → `tools/call`) instead of relying on a chat client's bridge, if you need to rule out the bridge itself as the point of failure.
4. If a call still appears to hang or errors with "draft was saved but publish failed", retry `website.publish` on its own for that page id — publishing only reads the already-saved draft and returns the (small) `website.page`/`website.site` record, so it is not subject to the same size issue and does not resend the html.

### "The AI says website.* tools do not exist"

Why it happens: the website module is not enabled for your tenant database. `website.*` tools are hidden from `tools/list` and rejected by `tools/call` exactly like an unknown tool name when the module is off.

What to do:

1. Ask the AI to run `tools/list` and confirm no `website.*` tool is present
2. Ask an administrator to enable the website module for your database
3. Retry `tools/list` after it is enabled — the tools should now appear

### "The attachment exists but is not visible"

Why it happens: the attachment scope or the underlying record permissions may block access.

What to do:

1. Ask the AI to inspect attachment metadata
2. Confirm `sys_model`, `model_id`, `company_id`, `user_id`, `group_id`, and `is_public`
3. Confirm that your account can read the bound record

---

## Frequently Asked Questions

**Q: What is the best live model for a customer phone book?**  
A: `tbl.partner`.

**Q: Can I create a brand-new live business model only by prompt through MCP?**  
A: Yes, with a superadmin MCP identity: `base.model.create_custom` + `base.model.add_field` define it, `base.model.publish` provisions the live table, and `base.model.create_ui` scaffolds a default admin screen. Only `text`, `integer`, `float`, `boolean`, `date`, `datetime`, `json`, `many2one`, and `one2one` fields are publishable today (`one2many`/`many2many` stay metadata-only), and a published model needs re-publishing after a server restart.

**Q: What is the best live model for customer documents?**  
A: `sys.attachment`, usually bound to `tbl.partner`.

**Q: How do I ask the AI to work with relations correctly?**  
A: Tell it to search the related model first and never guess relation IDs.

**Q: How do I get a customer report?**  
A: Ask the AI to search `sys.report` for an existing report definition and then call `render_report`.

**Q: Can I overwrite the bytes of an existing attachment?**  
A: No. Create a new attachment instead.

**Q: Can I manage my website (pages, SEO, publishing) by prompt?**  
A: Yes, if the website module is enabled for your database. Use `website.record.*` for CRUD, `website.page.design_upsert` to create or update a page's rendered HTML **and publish it in the same call**, `website.seo.upsert` to set site- or page-level SEO, and `website.publish`/`website.unpublish` to (re)publish content-unchanged or take a site or page offline. See [How to Prompt for the Website Module](#how-to-prompt-for-the-website-module).

**Q: Why is there a separate `website.seo.upsert` instead of just writing SEO fields with `website.record.write`?**  
A: Site-level SEO fields are flat columns on `website.site`, but page-level SEO fields live inside `website.page`'s own nested `seo` JSON field, not as flat columns. `website.seo.upsert` translates the flat field names you pass into that nested shape and shallow-merges them in, so you don't have to read-modify-write the JSON yourself.

---

## Reference

### Prompt checklist

Before sending a prompt, check that you included:

- The business goal
- The target live model, if you know it
- The important fields
- A request to inspect first if uncertain
- A request to resolve relation IDs safely
- A request to read the result back after writing

### High-value model references

| Model | Best use in this guide |
|---|---|
| `tbl.partner` | Customer phone book |
| `sys.attachment` | Customer documents |
| `sys.report` | Existing report definitions |
| `website.site` / `website.page` | Website content, SEO, and publishing (conditional — see [Website module tools](#website-module-tools-conditional)) |

### High-value methods through `base.record.call_method`

| Model | Method | Use |
|---|---|---|
| `sys.attachment` | `attachments_for_record` | List attachments bound to one record |
| `sys.report` | `render_report` | Render HTML or PDF report output |

Website publish/unpublish is also reachable this way (`website.site`/`website.page`, method `action_publish`/`action_unpublish`), but prefer the dedicated `website.publish`/`website.unpublish` tools — same effect, simpler arguments.

---

## Getting Help

If you are not sure how to phrase a task, start with:

```text
Use Base HUB MCP.
First inspect the live model and explain the safest way to complete this task.
Do not write anything until you have shown me the plan.
```

If the AI tells you a live model or report does not exist, treat that as a real product limitation, not a prompting failure. In that case, ask for:

1. A safer workaround using existing live models
2. A metadata draft only
3. A developer handoff for the missing live capability
