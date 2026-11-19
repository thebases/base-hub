# Base module: user and administrator guide

## 1. Purpose and who uses it

`base` is the kernel module of Base HUB. Every other module depends on it. It provides:

- Master data: companies, partners (contacts) and partner bank accounts, countries, states, wards, country groups, currencies and languages.
- Identity and access: users, groups (roles), group privileges, model access rules and record rules.
- The system catalogue (the `sys.*` models): modules, models, fields, external identifiers, config parameters, sequences, attachments, reports, change log.
- The UI catalogue (the `ui.*` models): menus, views, window actions, client actions and view types.
- The shared Settings, Admin and Document menus, plus the MCP tools used to inspect and edit data programmatically.

| Audience | What they do here |
| --- | --- |
| Internal users (`Internal User`) | Read partners, companies, countries, currencies, languages and the menu/view definitions. |
| Settings administrators (`Settings`) | Manage users, groups, access, rules, sequences, reports, attachments, UI definitions and models. |
| Super administrators (`Technical Features`) | Full access to every model and every company. |
| Integrators and AI agents | Use the `base.*` MCP tools or the JSON-RPC API. |

## 2. Prerequisites and installation

| Item | Value |
| --- | --- |
| Module name | `base` (version 0.1.0, category Hidden) |
| Dependencies | None |
| Installation | Installed automatically as the kernel module; it is not installed from the Modules screen. |
| Seed data | Loaded on install and upgrade from the `data/` folder (see section 6). |

## 3. Key concepts

| Concept | Model | Plain meaning |
| --- | --- | --- |
| Company | `res.company` | A legal entity or branch. A company can have a parent company; `Is Branch` marks a branch. Each company is linked to a partner record. |
| Partner | `res.partner` | Any contact: person or company (`Is Company`), with address, country, notes, bank accounts and attachments. |
| Partner bank account | `res.partner.bank` | Bank account attached to a partner. |
| User | `base.users` | A person who logs in. Has a login, a partner, a main group, a main company, allowed companies, language and timezone. |
| Group | `res.groups` | A role. Groups can imply other groups. |
| Group privilege | `base.groups.privilege` | A named set of privileges that groups can reference. |
| Model access | `sys.model.access` | Per group and model: read, write, create, delete permission. |
| Record rule | `sys.rule` | Row-level filter (a domain) on a model. It is either global or applies to chosen groups. |
| Country, State, Ward, Country Group | `res.country`, `res.country.state`, `res.country.ward`, `res.country.group` | Geographic reference data. |
| Currency and rates | `res.currency`, `res.currency.rate` | Currencies and their exchange rates. |
| Language | `res.lang` | UI languages with date, time and number formats. |
| Sequence | `sys.sequence` | Generator of document numbers (prefix, suffix, padding, increment, next number). |
| Config parameter | `sys.config_param` | Key and value system setting. |
| Key value | `base.kvt` | Shared lookup lists, for example timezones. |
| Attachment | `sys.attachment` | Uploaded file linked to a record. |
| Report | `sys.report` | A printable report definition linked to a model and a template. |
| External identifier | `sys.model.data` | Stable name (such as `base.group_user`) that points to a record. |
| UI Menu, View, Actions, View type | `ui.menu`, `ui.view`, `ui.actions.window`, `ui.actions.client`, `ui.view.type` | Definitions that build the screens you see. |

### Built-in groups

| Group | Label | Meaning |
| --- | --- | --- |
| `base.group_no_one` | Technical Features | Super admin: all models, all records, all companies. |
| `base.group_user` | Internal User | Default application user. |
| `base.group_multi_company` | Multi Companies | Internal User, extended to every company in the user's allowed companies, not only the main one. |
| `base.group_system` | Settings | Multi Companies, plus full create/edit/delete on `ui.*` and `sys.*` models. |

## 4. Where to find it

| Menu (EN / VI) | Path | Notes |
| --- | --- | --- |
| Main > Company / Công ty | `/web/tbl.company` | Visible to Multi Companies or Technical Features. |
| Main > Partners / Liên hệ | `/web/tbl.partner` | |
| Main > Bank Accounts / Tài khoản ngân hàng | `/web/tbl.partner.bank` | |
| Settings > Group Privileges / Đặc quyền nhóm | `/web/base.groups.privilege` | Settings menu: Settings group or Technical Features only. |
| Settings > Users / Người dùng | `/web/base.users` | |
| Settings > Countries / Quốc gia | `/web/tbl.country` | |
| Settings > Country Groups / Nhóm quốc gia | `/web/tbl.country.group` | |
| Settings > Country States / Tỉnh/Bang | `/web/tbl.country.state` | |
| Settings > Country Wards | `/web/tbl.country.ward` | |
| Settings > Languages / Ngôn ngữ | `/web/tbl.lang` | |
| Settings > Groups / Nhóm | `/web/tbl.groups` | |
| Settings > Attachments / Tệp đính kèm | `/web/sys.attachment` | |
| Settings > Reports / Báo cáo | `/web/sys.report` | |
| Settings > Sequences / Chuỗi định danh | `/web/sys.sequence` | |
| Admin > System Config Params / Tham số cấu hình hệ thống | `/web/sys.config_param` | |
| Admin > Key Values | `/web/base.kvt` | |
| Admin > Model Access / Quyền truy cập mô hình | `/web/sys.model.access` | |
| Admin > Record Rules / Các quy tắc bản ghi | `/web/sys.rule` | |
| Admin > Models, Model Fields, Modules, Module Categories | `/web/sys.model`, `/web/sys.model.field`, `/web/sys.module.module`, `/web/sys.module.category` | |
| Admin > External Identifiers, Model Relations, Operations, Constraints, Actions | `/web/sys.model.data`, `.relation`, `.operation`, `.constraint`, `.action` | |
| Admin > UI Client Actions, UI Window Actions, UI Menus, UI Views, UI View Types, UI View Custom | `/web/ui.actions.client`, `/web/ui.actions.window`, `/web/ui.menu`, `/web/ui.view`, `/web/ui.view.type`, `/web/ui.view.custom` | |
| Admin > Service Health Check | `/web/system-health` | |
| Admin > Model Change Log | `/web/sys.model.log` | |
| Document > API Documentation, Instruction | `/web/apidoc`, `/web/instruction` | `Instruction` shows this guide. |

## 5. Step-by-step tasks

### Create a partner
1. Open Main > Partners and click New.
2. Fill in the name, tick `Is Company` if it is an organisation, then the Business and Profile groups (country, company, contact data).
3. Use the tabs for Relations, Bank Account, Notes and Attachments, then Save.

### Create a user and set the password
1. Open Settings > Users and click New.
2. Under Identity set Name, Login, Email and the linked Partner.
3. On the Access tab set the main Group, the allowed Companies and the main Company.
4. On the Preferences tab set Language and Timezone.
5. Save, then click Change Password (the button appears once the record is saved) and enter the new password.
6. Optional: click Generate API Key. The key is shown once; copy it immediately, only a hash is stored.

### Deactivate a user
Open the user, clear `Active` and save. Use the `Not Active` filter to find inactive users.

### Add a company or branch
Main > Company > New. Set the name, optionally the `Parent Company` and tick `Is Branch`. Branches show on the Branches tab of the parent. A matching partner is kept in sync automatically.

### Create a sequence
Settings > Sequences > New: set Name, Code, Prefix (for example `DEMO/%(year)s/`), Suffix, Padding, Increment, Next Number and Implementation (for example `no_gap`).

### Define a record rule
Admin > Record Rules > New: pick the model, enter the domain, choose the groups (leave empty for a global rule) and the permissions the rule applies to.

### Grant model access
Admin > Model Access > New: choose group and model, tick read, write, create, delete as needed.

### Find records
Every list has a search box, filters (for example Active, Is Company), and group-by options. Multi-edit is available on lists.

## 6. Administration and configuration

- Access control has two layers: Model Access (can this group touch this model) and Record Rules (which rows).
- `Internal User` gets read-only access to master data and UI definitions. `Settings` gets full access to them. Internal users cannot read `sys.model`, `sys.model.field` or `sys.model.data`.
- Seeds loaded by the module:

| Seed | Content |
| --- | --- |
| Groups | The four groups in section 3. |
| Users | `admin` (Technical Features, companies `company.main` and `company.base1`), `base1`, plus inactive `User template` and `BaseBot` records. Change the default passwords on first login. |
| Reference data | Countries, states, wards, currencies, 90+ languages and timezones. |
| Sequences | `demo.document` (prefix `DEMO/%(year)s/`, padding 5). |
| Scheduled jobs | Platform Autovacuum (daily), Idempotency Store GC (every 30 minutes), Attachment Blob GC (daily). |
| Reports | `Demo User Summary` (A4, model `base.users`). |
| Record rules | Rules granting the `Settings` group access to the `sys.*` metadata models. |

- Translations: Vietnamese UI labels come from `i18n/vi_vn.po`. A user sees the UI in the language set in their preferences.

## 7. API and automation touchpoints

The module registers MCP tools. They honour the same access and record rules as the UI.

| Tool | Purpose |
| --- | --- |
| `base.model.list`, `base.model.describe`, `base.model.fields_get`, `base.model.methods` | Discover models, fields and callable methods. |
| `base.record.search_read`, `base.record.read` | Query and read records. |
| `base.record.create`, `base.record.write`, `base.record.unlink` | Create, update, delete records. |
| `base.record.call_method` | Call a model method. |
| `base.model.create_custom`, `add_field`, `rename_field`, `delete_field`, `create_ui`, `publish`, `unpublish` | Build and publish custom models. |
| `base.batch.execute` | Run several tool calls in one transaction; all roll back on failure. It cannot be nested. |

Users also expose password and API-key actions (`action_set_password`, `action_generate_api_key`) through the model methods. The built-in API documentation page lists all endpoints.

## 8. Troubleshooting and FAQ

| Problem | Likely cause and fix |
| --- | --- |
| A menu is missing | Settings and Admin need `Settings` or `Technical Features`; Company needs `Multi Companies`. Ask an administrator to add the group. |
| Cannot save a user | A login, an active password and a group are required when the user is active. Set a password with Change Password. |
| Change Password or Generate API Key button not visible | Save the record first. |
| API key lost | It cannot be recovered. Generate a new one. |
| User cannot see another company's data | Add the company to the user's allowed Companies and grant `Multi Companies`. |
| Access denied on a model | Add or fix a row in Model Access, or check Record Rules. |
| UI is not in Vietnamese | Set Language on the Preferences tab of the user, then reload. |

## 9. Limits and notes

- Editing `sys.*` and `ui.*` records changes the whole system. Test on a non-production database first.
- Seeded `admin` credentials are for first setup only; change them before going live.
- The module has category Hidden and is the base of every other module.
- Seed records flagged for forced update may be reset on upgrade; keep custom configuration in your own records.
