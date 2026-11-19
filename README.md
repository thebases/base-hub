# Base Hub

> **English** | [Tiếng Việt](#tiếng-việt)

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-14%2B-336791?logo=postgresql)](https://www.postgresql.org/)
[![Release](https://img.shields.io/badge/release-v1.0.0-blue)](CHANGELOG.md)
[![License](https://img.shields.io/badge/license-proprietary-red)](#team)

Base Hub is a metadata-driven business platform from **The Base**. Models, views, menus, access rules and workflows are stored as metadata and executed by one Go runtime, so new business apps (projects, helpdesk, website, knowledge base) ship as installable modules rather than separate services.

It is built with Go 1.25, Fiber v3, PostgreSQL and Bun ORM. The admin UI is a no-bundler Owl + NodexUI single-page app that is embedded in the same binary.

---

## Table of Contents

- [Features](#features)
- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Project Structure](#project-structure)
- [Quick Start](#quick-start)
- [Build and Deploy](#build-and-deploy)
- [CLI Reference](#cli-reference)
- [Modules](#modules)
- [Adding a New Model](#adding-a-new-model)
- [API Overview](#api-overview)
- [Configuration](#configuration)
- [Documentation](#documentation)
- [Limitations](#limitations)
- [Contributing](#contributing)
- [Team](#team)

---

## Features

- **Metadata-driven ORM runtime**: CRUD, domain filters, relations, computed and related fields, sequences, constraints and record rules come from model metadata (`tbl.*`, `sys.*`, `ui.*`).
- **Server-Driven UI**: `ui.view` XML arches (list, form, kanban, calendar, gantt, grid, hierarchy, cohort, map, dashboard) are rendered by the embedded Owl SPA.
- **RBAC and multi-company**: group ACLs (`sys_model_access.csv`), record rules that deny access when no rule matches, company scoping, and OAuth 2.0 / OIDC authentication.
- **Multi-tenant**: one process serves many PostgreSQL databases, selected by host (`dbFilter`), `X-Database-Name` header or query parameter, and checked against the tenant registry.
- **Workflow engine**: a visual DAG canvas with HTTP, webhook, record-event, cron, Kafka, MQTT and raw TCP triggers, Lua actions, and retries backed by an outbox.
- **Real-time bus**: WebSocket push with an SSE fallback, plus an MQTT transport for browsers and devices.
- **Base MCP gateway**: Model Context Protocol tools so AI agents can discover models and read or write records under the caller's own permissions.
- **i18n**: `.po` catalogs per module, a shell language switcher, and API docs in English and Vietnamese.
- **Single binary**: the frontend, module assets, seed data and docs are all embedded in one executable.

---

## Architecture

```text
HTTP · WebSocket/SSE · MQTT · Kafka · TCP
  └─► routes / bus / triggers                 (internal/routes, modules/*/routes)
        └─► runtime methods & services        (internal/modelruntime, internal/services)
              └─► internal/orm                (registry · RBAC · record rules · domain · relations)
                    └─► Bun repository
                          └─► PostgreSQL      (home DB + per-tenant DBs)
```

| Subsystem | Location | Responsibility |
| --- | --- | --- |
| Metadata registry & ORM runtime | `app/internal/orm/` | Model definitions, CRUD, domains, RBAC, record rules, relations |
| Kernel models | `app/internal/models/`, `app/modules/base/models/` | `sys.*`, `ui.*` and core `tbl.*` models |
| Module system | `app/internal/modules/`, `app/modules/` | Discover, install, upgrade and seed addon modules |
| Master-data import | `app/internal/services/masterdata/` | JSON / CSV / XML seeds (security → data → views) |
| RPC transport | `app/internal/routes/rpc_route*.go` | JSON-RPC-style dispatch at `/rpc/{model}/{method}` |
| API docs | `app/internal/apidoc/`, `app/internal/documents/` | OpenAPI and helper docs served at `/doc` |
| Workflow transports | `app/internal/services/{kafka,mqtt,tcpserver}/` | Trigger listeners for the workflow engine |
| Frontend SPA | `app/frontend/` | Owl + NodexUI SPA served at `/` and `/web` |

---

## Prerequisites

| Tool | Version | Notes |
| --- | --- | --- |
| Go | 1.25+ | Required |
| PostgreSQL | 14+ | Primary data store |
| Node.js | 18+ | Only for `make build` at the repo root (stamps `frontend/version.json`) and the frontend check scripts |
| Kafka | Any modern | Only when `kafka.enable: true` |
| MQTT broker | Any | Only when `mqtt.enable: true` |

The frontend has no bundler. You do not need `pnpm install` or a frontend build step.

---

## Project Structure

```text
base-hub/
├── app/                        # Go service root (module git.thebase.vn/base_hub)
│   ├── main.go                 # Entrypoint, embeds frontend, modules and docs
│   ├── Makefile                # run / build / migrate / vet / test / deploy targets
│   ├── run.sh, init.sh         # Local dev helpers (start server, init a database)
│   ├── frontend/               # No-bundler Owl + NodexUI SPA
│   ├── internal/
│   │   ├── cmd/                # Cobra CLI (start, init-db, import-data, module, model, i18n, …)
│   │   ├── config/             # Config loader, config.yaml template
│   │   ├── orm/                # Runtime ORM
│   │   ├── models/             # Kernel models
│   │   ├── modules/            # Module loader and dependency resolver
│   │   ├── routes/             # HTTP handlers
│   │   ├── services/           # Application services
│   │   ├── apidoc/, documents/ # API and helper documentation
│   │   └── mcpkit/             # MCP tool framework
│   ├── modules/                # Addon modules (see Modules)
│   └── tests/                  # Conformance and k6 performance suites
├── scripts/                    # CI checks (UI pin, i18n msgids, preload graph)
├── Makefile                    # Root build (frontend version stamp + backend build)
└── .agents/                    # AI agent skills, knowledge and memory
```

---

## Quick Start

### 1. Configure

```bash
git clone https://git.thebase.vn/base_hub.git base-hub
cd base-hub/app
cp internal/config/config.yaml internal/config/config_dev.yaml
```

Edit the `postgresql` section of `config_dev.yaml` (see [Configuration](#configuration)) and set a new `app.encryptKey`. Don't commit real credentials.

### 2. Initialize a database

```bash
./init.sh base-home
# = go run ./main.go init-db --config internal/config/config_dev.yaml base-home
go run ./main.go reset-admin-password '<new-password>' -c internal/config/config_dev.yaml
```

`init-db` creates the database if it is missing, runs migrations and seeds master data for every installed module. Base Hub has no default admin password, so set one with `reset-admin-password`.

### 3. Run

```bash
./run.sh
# = go run ./main.go start --force-config -c internal/config/config_dev.yaml
```

On startup the server connects to PostgreSQL, registers the runtime models, auto-migrates tables, syncs `sys.module.module` from the embedded manifests, imports pending seed data, and then starts Fiber plus the optional Kafka, MQTT and TCP listeners.

Open `http://localhost:8382/web` (the port is `server.port`) and sign in as `admin` with the password you set.

### 4. Verify

```bash
curl http://localhost:8382/health
curl -X POST http://localhost:8382/oauth/token \
  -H "Content-Type: application/json" \
  -H "X-Database-Name: base-home" \
  -d '{"grant_type":"password","username":"admin","password":"<new-password>"}'
```

---

## Build and Deploy

```bash
# From the repo root: stamp frontend/version.json, then build the backend
make build

# Or backend only
cd app
make build                              # → ./build/base-hub  (CGO disabled, linux/amd64)
make build GOOS=windows GOARCH=amd64    # → ./build/base-hub.exe
```

`make build` regenerates the module registry (`go generate ./modules/all`), compiles the binary with all embedded assets, writes `./build/base-hub.service` (systemd) and copies `config.yaml` into `./build/`.

Other `app/Makefile` targets are `run`, `vet`, `test`, `conformance`, `perf`, `migrate` and `deploy`. `make deploy` uses `DEPLOY_HOST`, `DEPLOY_USER` and `DEPLOY_PATH`, the same variables as the `deploy` job in `.gitlab-ci.yml`.

CI (`.gitlab-ci.yml`) runs `build:backend` and `check:frontend` on each push. On `v*` tags it also runs `tag:build-and-release` and `sync-to-github`.

---

## CLI Reference

The root command is `base-hub` (or `go run ./main.go` from source). Most commands accept `-c/--config`, which defaults to `./build/config.yaml`.

| Command | Purpose |
| --- | --- |
| `start [--force-config] [--force-master-data]` | Start the HTTP server and runtime workers |
| `init-db <name>` | Create, migrate and seed a database |
| `reset-admin-password <password> [-d db]` | Set the `admin` password (there is no default) |
| `import-data [-p path] [-f] [--force-config]` | Import module seed data without starting the server |
| `module <name>` | Scaffold a new module under `./modules` and register it |
| `model <module> <entity> --fields "…"` | Scaffold a `tbl.<module>_<entity>` model (struct, menu, ACL, i18n, test) |
| `model check [module]` | Check module views, menus and ACL rows against registered models |
| `i18n extract <module>` | Rebuild `<module>.pot` and merge new msgids into each `.po` |
| `rotate-encrypt-key --new-config <file> [--apply]` | Re-encrypt module secrets under a new `app.encryptKey` (dry run by default) |
| `repair-attnum-slots <table> [--apply]` | Reclaim dropped-column attnum slots (dry run by default) |

Example model scaffold:

```bash
go run ./main.go model project milestone \
  --fields "name:char!,state:selection(draft|done),partner_id:m2o(tbl.partner)"
```

---

## Modules

Each module lives in `app/modules/<name>/` and declares its metadata and dependencies in the front matter of `manifest.md`.

| Module | Category | Depends on | Purpose |
| --- | --- | --- | --- |
| `base` | Hidden | — | Kernel: users, groups, companies, partners, menus, views, sequences, `base.kvt`, settings |
| `base-auth` | System | base | OAuth 2.0 / OIDC (`/oauth/token`, `/oauth/authorize`, `/oauth/revoke`) |
| `base-automation` | System | base | Scheduled jobs and saved automation (Lua) actions |
| `base-bus` | System | base, base-auth | Real-time bus (WebSocket, SSE fallback, MQTT) |
| `base-calendar` | System | base, base-automation | Calendar view and recurrence engine |
| `base-credential` | System | base, base-automation | Encrypted credential store for integrations |
| `base-mcp` | System | base, base-auth | MCP gateway for AI agents |
| `base-workflow` | System | base, base-automation, base-credential, base-bus | DAG workflow builder and runtime |
| `discuss` | Discuss | base, base-bus, base-credential | Chatter, followers, activities, canned responses, mail |
| `knowledge` | Utility | base | Markdown knowledge base with live collaborative editing |
| `website` | Utility | base | Website builder, publishing, domains, forms |
| `website-blog` | Website | base, website | Blogs, posts, tags, SEO, RSS and sitemap |
| `project` | Services | base, base-calendar, discuss | Projects, tasks, stages, dashboards |
| `helpdesk` | Support | base, base-auth, base-calendar, base-mcp, base-automation, base-bus, discuss | Tickets, teams, SLAs, KPIs |
| `project-helpdesk` | Services | project, helpdesk | Bridge between tickets and tasks |

### Manifest

```markdown
---
name: my-module
version: 0.1.0
category: MyCategory
installable: true
application: false
depends: [base]
auto_install: false
---

# My Module

What this module provides.
```

### Layout

| Path | Purpose |
| --- | --- |
| `manifest.md` | Module metadata and dependencies |
| `<name>.go` | Module entrypoint, registers models, routes and assets |
| `models/` | Model definitions (`orm.Define`) and the module `ModelSet` |
| `security/sys_model_access.csv` | Access rules |
| `data/` | Seed records (JSON / CSV / XML) |
| `views/<ui type>-<ui name>.xml` | `ui.view` arches and menus |
| `i18n/` | `.pot` / `.po` catalogs |
| `static/` | Frontend widgets, `register.js`, CSS |
| `mcp/` | MCP tools contributed by the module (optional) |

`app/modules/all/modules_gen.go` is generated. Don't edit it by hand. `make build` and the `module` command regenerate it, or you can run `go generate ./modules/all` yourself.

---

## Adding a New Model

The quickest way is `go run ./main.go model <module> <entity> --fields "…"`. To add one by hand:

```go
package models

import "git.thebase.vn/base_hub/internal/orm"

type InvoiceRecord struct {
    orm.BaseModel
    Number    string `bun:"number,notnull" json:"number" meta:"required,index,label:Number"`
    Amount    int64  `bun:"amount"         json:"amount" meta:"label:Amount"`
    PartnerID int64  `bun:"partner_id"     json:"partner_id" meta:"kind:many2one,model:tbl.partner,label:Partner"`
}

const InvoiceModelName = "tbl.account_invoice"

var InvoiceModel = orm.Define[InvoiceRecord](&Models, 100, orm.ModelSpec{
    Name:  InvoiceModelName,
    Label: "Invoice",
    RuntimeMethods: []orm.RuntimeMethod{
        orm.Typed(orm.RuntimeMethod{Name: "confirm", Public: true, UsesPathID: true}, confirmInvoice),
    },
})
```

`Models` is the module's `orm.ModelSet`, declared in `models/runtime_bootstrap.go`. The module boots it with `Models.Init()`. Then add ACL rows:

```csv
id,name,model_id,group_id,perm_read,perm_write,perm_create,perm_unlink
my_module.access_invoice_system,Invoice (System),tbl.account_invoice,base.group_system,1,1,1,1
my_module.access_invoice_user,Invoice (User),tbl.account_invoice,base.group_user,1,0,0,0
```

Run `go run ./main.go model check my_module` to catch missing views, menus or ACL rows.

---

## API Overview

| Path | Method | Description |
| --- | --- | --- |
| `/oauth/token` | POST | Token endpoint (`password`, `refresh_token`, `client_credentials`, `authorization_code`) |
| `/oauth/authorize`, `/oauth/revoke` | GET/POST | OAuth 2.0 authorize / revoke |
| `/rpc/{model}/{method}` | POST | Call any public runtime method on a model |
| `/rpc/{model}/{method}/{id}` | POST | Call a method on one record |
| `/base/<route>` | varies | REST-style aliases declared on runtime methods |
| `/bus/ws`, `/bus/sse` | GET | Real-time bus (WebSocket, SSE fallback) |
| `/mcp` | POST | Base MCP gateway |
| `/doc`, `/doc/openapi.json` | GET | API docs and OpenAPI descriptor (`?lang=vn` for Vietnamese) |
| `/health`, `/metrics` | GET | Health and metrics |

```bash
curl -X POST http://localhost:8382/rpc/tbl.partner/search_read \
  -H "Authorization: Bearer <access_token>" \
  -H "Content-Type: application/json" \
  -H "X-Database-Name: base-home" \
  -d '{"params":{"domain":[["active","=",true]],"fields":["id","name"],"limit":20}}'
```

The RPC and MCP transports ignore any `sudo`, `uid` or context override sent by the client, and methods marked `Internal` cannot be called from them. Secret fields are never returned to clients.

---

## Configuration

The template is at `app/internal/config/config.yaml`. These are the main keys:

```yaml
app:
  appName: BASE HUB
  encryptKey: "<64 hex chars>"   # file-only; rotate with `rotate-encrypt-key`
  use_multi_tenant: false
  supportApikey: false           # allow per-user X-API-Key generation
server:
  host: localhost
  port: 8382
  sharedFolder: ./data           # tenant files: <sharedFolder>/<db>/…
postgresql:
  dbHost: localhost
  dbPort: 5432
  dbUser: <user>
  dbPassword: <password>
  homeDbName: base-home          # control-plane / default database
  templateDbName: base-template  # clone source for new tenants
  dbFilter: '^hub([-.a-zA-Z0-9]*)\.thebase\.vn$|^home$'
authService:
  enable: false                  # external OIDC IdP
kafka:
  enable: false
mqtt:
  enable: false
tcp:
  useTcp: false                  # workflow raw TCP trigger transport
knowledge:
  publicDocsPath: public_docs
```

On startup, config values are synced into `sys.config_param`. Run `start --force-config` to overwrite existing rows from the file. Keep production secrets out of the repository.

---

## Documentation

| Document | Where |
| --- | --- |
| Changelog | [CHANGELOG.md](CHANGELOG.md) |
| Live API docs | `/doc` on a running instance |
| In-app instructions | **Instruction** page (per-module helper docs from `app/internal/documents/helper/`) |
| Module notes | each module's `manifest.md` |
| Requirements, research, design notes | [.agents/knowledge/](.agents/knowledge/) |

---

## Limitations

1. **PostgreSQL only.** The runtime assumes PostgreSQL + Bun.
2. **Code-defined fields.** Adding a stored field to a Go-backed model requires a code change and rebuild. Custom models created through MCP are the exception.
3. **Postgres column cap.** Repeated `ADD`/`DROP COLUMN` cycles consume attnum slots. Use `repair-attnum-slots` if a table nears 1600.
4. **Bypassing the runtime is unsafe.** Raw Bun queries in handlers skip RBAC, record rules and company scoping.

---

## Contributing

```bash
cd app
go vet ./...
go test ./...
go test -race ./internal/orm/...
make conformance        # Postgres-backed conformance suite
go generate ./modules/all
```

Conventions:

- New business models use the `tbl.<module>_<entity>` name and are registered with `orm.Define`.
- Go through `internal/orm` for CRUD and dispatch. Don't write raw Bun queries in route handlers.
- Pass `context.Context` through every service call, query and HTTP request. Wrap errors with `%w`.
- Wrap financial or balance mutations in explicit transactions, and make them idempotent when externally triggered.
- View files are named `<ui type>-<ui name>.xml`. User-facing strings need i18n msgids, which `scripts/check_i18n_msgids.mjs` checks in CI.

Commit subjects use a tag prefix, for example `[ADD] website-blog: RSS feed`, `[FIX] orm: …`, `[IMP] frontend: …`, `[UPDATE] …`.

---

## Team

Base Hub is developed and maintained by **The Base**. Proprietary, all rights reserved.

- Website: [thebase.vn](https://thebase.vn)
- Support: <community@thebase.vn>

---

---

## Tiếng Việt

**Base Hub** là nền tảng nghiệp vụ hướng metadata của **The Base**. Model, view, menu, quyền truy cập và workflow được lưu dưới dạng metadata và thực thi bởi một Go runtime duy nhất. Nhờ vậy, các ứng dụng nghiệp vụ mới (dự án, helpdesk, website, knowledge base) được phát hành dưới dạng module cài đặt được, thay vì là các service riêng.

Nền tảng xây dựng bằng Go 1.25, Fiber v3, PostgreSQL và Bun ORM. Giao diện quản trị là SPA Owl + NodexUI không cần bundler, được nhúng vào cùng một binary.

### Tính năng chính

- ORM runtime hướng metadata: CRUD, domain, relation, sequence, constraint, record rule.
- Server-Driven UI: `ui.view` XML (list, form, kanban, calendar, gantt, grid, dashboard, …).
- RBAC, đa công ty, OAuth 2.0 / OIDC.
- Đa tenant: một process phục vụ nhiều database PostgreSQL.
- Workflow DAG với trigger HTTP, webhook, record event, cron, Kafka, MQTT, TCP và action Lua.
- Bus real-time (WebSocket, SSE, MQTT), cổng MCP cho AI agent, đa ngôn ngữ (en, vi).

### Khởi động nhanh

```bash
git clone https://git.thebase.vn/base_hub.git base-hub
cd base-hub/app
cp internal/config/config.yaml internal/config/config_dev.yaml
# Sửa mục postgresql và app.encryptKey trong config_dev.yaml

./init.sh base-home                                              # Tạo + migrate + seed database
go run ./main.go reset-admin-password '<mật-khẩu>' -c internal/config/config_dev.yaml
./run.sh                                                         # Khởi động server
```

Truy cập `http://localhost:8382/web` và đăng nhập bằng `admin` với mật khẩu vừa đặt. Base Hub không có mật khẩu mặc định.

### Build và deploy

```bash
make build                     # Tại thư mục gốc: stamp version frontend + build backend
cd app && make build           # → ./build/base-hub (+ base-hub.service, config.yaml)
make deploy                    # Dùng DEPLOY_HOST / DEPLOY_USER / DEPLOY_PATH
```

### Lệnh CLI

```bash
go run ./main.go start                         # Khởi động server
go run ./main.go init-db <tên_db>              # Khởi tạo database
go run ./main.go import-data -p ./modules      # Import seed data
go run ./main.go module <tên_module>           # Tạo khung module mới
go run ./main.go model <module> <entity> --fields "name:char!"   # Tạo khung model
go run ./main.go i18n extract <module>         # Cập nhật file dịch .pot/.po
```

### Quy ước

- Model nghiệp vụ mới đặt tên `tbl.<module>_<entity>` và đăng ký bằng `orm.Define`.
- Dùng `internal/orm` cho mọi CRUD. Không viết Bun query trực tiếp trong route handler.
- Truyền `context.Context` qua mọi lời gọi. Mọi thay đổi tài chính phải chạy trong transaction tường minh và có idempotency.
- File view đặt tên theo dạng `<ui type>-<ui name>.xml`. Chuỗi hiển thị cho người dùng phải có msgid i18n.

### Tài liệu

- Lịch sử thay đổi: [CHANGELOG.md](CHANGELOG.md)
- API docs: `/doc` trên instance đang chạy (`?lang=vn` cho tiếng Việt)
- Tài liệu nghiên cứu và đặc tả: [.agents/knowledge/](.agents/knowledge/)

Base Hub được phát triển bởi **The Base**: [thebase.vn](https://thebase.vn)
