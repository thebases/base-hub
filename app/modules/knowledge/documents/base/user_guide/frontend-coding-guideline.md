# Base HUB — Frontend Coding Guideline

| Field | Value |
| --- | --- |
| Author | Base HUB Team |
| Status | Active |
| Version | 1.0 |
| Date | 2026-05-18 |
| Applies to | `app/frontend-admin/src/` — all TypeScript/React code |
| Audience | Frontend engineers contributing to Base HUB |

---

## 1. Purpose and scope

This guideline defines how frontend code is written, organized, and reviewed inside Base HUB. It covers the SPA shell, module components, field widgets, form arch rendering, API calls, state management, and styling.

**In scope:**

- TypeScript/React code under `app/frontend-admin/src/`
- Component structure, naming, and module ownership
- Form arch parsing and rendering pipeline
- Field widget contracts (many2one, x2many, selection, etc.)
- API layer and RPC call patterns
- Zustand store usage
- Tailwind/shadcn styling
- Responsive and mobile-first rules
- Known risks and areas requiring extra care

**Out of scope:**

- Backend Go code
- Vite/build configuration (unless it affects runtime behavior)
- CI/CD pipeline

---

## 2. Background and rationale

The frontend is a **metadata-driven SPA**: the backend sends XML arch descriptions and field metadata; the frontend parses and renders them dynamically. This means a small bug in the arch parser or a field widget contract break can corrupt every view that uses that field type — not just one screen.

The stack is: **React 19 · Vite · Tailwind 4 · shadcn · Zustand 5 · TypeScript 5.9 · lucide-react · zod 4**.

The architecture has two layers:

| Layer | Path | Role |
| --- | --- | --- |
| **Shared UI primitives** | `src/components/ui/` | shadcn-style atoms — never contain model or business logic |
| **Module components** | `src/modules/<module>/components/` | Feature-scoped views, fields, canvas widgets |

Module components must not reach into another module's component tree. Cross-module UI is promoted to `src/components/ui/` or coordinated through shared stores.

---

## 3. Definitions

| Term | Definition |
| --- | --- |
| `FormArchNode` | A parsed node from the backend XML arch — can be a field, group, button, container, etc. |
| `arch parser` | `form-arch-parser.ts` — converts raw XML arch string into a `FormArchNode` tree |
| `arch renderer` | `form-arch-renderer.tsx` → `form-renderer.tsx` — walks the node tree and renders React components |
| `*-tag.tsx` | One file per XML layout tag (e.g., `container-tag.tsx`, `sheet-tag.tsx`); the renderer delegates to these |
| `field state` | Invisible/readonly/required expressions evaluated per-field; owned by `form-arch-field-state.ts` |
| `many2one submit contract` | Many2one fields submit a scalar integer ID only; display labels stay client-local |
| `x2many save normalization` | One2many/many2many values must be collapsed to id-only arrays before create or write |
| `BaseView` | `src/modules/base/components/views/base-view/base-view.tsx` — the generic list/form/kanban host |
| `RemoteForm` | `remote-form.tsx` — a standalone form view opened inside a sheet, used for many2one create/readonly |

---

## 4. Guidelines

### 4.1 Component and module structure

**Rule 4.1.1 — Module components live under their module; shared primitives live in `src/components/ui/`**

Feature components belong in `src/modules/<module>/components/`. Only stateless, model-agnostic UI atoms belong in `src/components/ui/`. Do not add model-specific logic to `src/components/ui/`.

Why: Mixing feature and primitive layers makes primitives impossible to extract or test independently.

```tsx
// Good
// src/modules/base/components/fields/many2one/index.tsx

// Bad — model-aware code in shared primitives
// src/components/ui/partner-dropdown.tsx
```

---

**Rule 4.1.2 — One arch layout tag per `*-tag.tsx` file**

Each XML layout tag that the arch renderer supports must have its own `*-tag.tsx` file (e.g., `container-tag.tsx`, `sheet-tag.tsx`, `button-tag.tsx`). The renderer delegates to these files; it does not handle tag rendering inline.

Why: Inlining all tag rendering in the renderer produces a monolith that is hard to test and extend.

---

**Rule 4.1.3 — Field state logic is owned by `form-arch-field-state.ts`; do not duplicate it in field components**

Invisible, readonly, and required expression evaluation is centralized in `form-arch-field-state.ts`. Field components read the evaluated state; they must not re-evaluate arch expression strings.

Why: Duplicate evaluation creates divergence — two fields making different decisions for the same expression.

---

**Rule 4.1.4 — Container nodes must preserve original HTML tags and forward safe DOM attributes**

When a `FormArchContainerNode` renders, it must use the `tagName` from the parsed node and forward only safe, known DOM attributes. Do not replace the tag with `<div>` unconditionally.

Why: Backend arch can send semantic HTML tags (`<section>`, `<footer>`, `<header>`). Replacing them changes accessible and structural meaning.

---

**Rule 4.1.5 — Do not introduce CRM-specific naming in generic host components**

`BaseView` and `jsonv2.ts` use generic model-oriented names. Do not add legacy `CrmLead*`-style symbols to these files.

Why: `BaseView` is a generic host for all models; CRM-specific naming is a known technical debt that must not grow further.

---

### 4.2 Form arch parsing and rendering

**Rule 4.2.1 — Form save must return to readonly only after async create/write succeeds**

After a save action, the form must remain in edit mode until the backend confirms success. Only on a successful response does the form transition to readonly.

Why: Premature transition to readonly hides save failures from the user — they see a clean form but the data was never written.

```tsx
// Good
const result = await rpcWrite(model, id, payload)
if (result.error) { showError(result.error); return }
setMode("readonly")

// Bad
setMode("readonly")  // optimistic — wrong if save fails
await rpcWrite(model, id, payload)
```

---

**Rule 4.2.2 — `type="action"` buttons resolve through `sys.model.data`**

When a button has `type="action"`, resolve its action using the XML ID via `sys.model.data`. Do not hardcode action IDs.

---

**Rule 4.2.3 — `type="object"` buttons use `/rpc/{model}/{method}` for generic methods**

Only append `/{id}` to the path when the method is on the backend path-ID whitelist. After the RPC call, refresh the record.

---

**Rule 4.2.4 — XML `domain` is live client behavior**

`domain="..."` attributes on `<field>` nodes are evaluated live and affect both many2one search filters and dependent field clearing. They must be re-evaluated whenever the record changes.

---

**Rule 4.2.5 — `x2many` child invisibility evaluates with `parent` bound to the outer form**

When evaluating visibility expressions for rows inside a one2many or many2many table, `parent` must be bound to the current outer form record's values.

---

### 4.3 Field widget contracts

**Rule 4.3.1 — Many2one submit contract: scalar integer ID only**

Many2one field values submitted to the backend must be a plain integer (the record ID). Display labels are client-local and must be synced from server-returned `[id, name]` tuples. Never send a `[id, label]` tuple to the backend.

```tsx
// Good
payload[fieldName] = selectedId  // number

// Bad
payload[fieldName] = [selectedId, selectedLabel]  // tuple
```

---

**Rule 4.3.2 — Many2one domain changes must trigger combobox remount**

When the evaluated domain for a many2one field changes, remount the combobox root (via a `key` prop change) so stale cached options are discarded.

Why: Stale options from a previous domain can appear and be selected even though they no longer satisfy the current filter.

---

**Rule 4.3.3 — Many2one search limit is `MANY2ONE_SEARCH_LIMIT` (10)**

Do not hardcode `10` in many2one search calls. Use the `MANY2ONE_SEARCH_LIMIT` constant.

---

**Rule 4.3.4 — Many2one create permission depends on `check_access_rights("create")` and `no_create`**

Before showing the inline create option on a many2one dropdown, check both the backend `check_access_rights("create")` result and the `no_create` widget option. Both must permit creation.

---

**Rule 4.3.5 — Many2one create opens a right-side `Sheet` `RemoteForm`; after save, resolve with `name_get`**

The create flow for a new related record opens as a `Sheet` with a `RemoteForm`. On save, call `name_get` on the new record and select the returned `[id, name]` tuple.

---

**Rule 4.3.6 — Many2one readonly opens a read-only `Sheet` `RemoteForm` via `RelatedRecordLink`**

In readonly mode, many2one values render as a `RelatedRecordLink`. Clicking it opens a `Sheet` with a read-only `RemoteForm` for that record.

---

**Rule 4.3.7 — `fields/selection` owns selection-field detection; it reuses the `many2one` combobox**

Selection field type detection (`type`, `kind`, `backendType === "selection"`) and option resolution (`field.options` or `field.selection`) both live in `fields/selection`. This component reuses the `many2one` combobox implementation — do not duplicate the combobox UI.

---

**Rule 4.3.8 — One2many save normalization: collapse to id-only arrays; empty selection sends `false`**

Before calling create or write for a one2many field, normalize values to an array of integer IDs. An empty (cleared) selection must send boolean `false`, not `[]` or `null`.

---

**Rule 4.3.9 — One2many `Add row` injects parent `id` into the child inverse relation field**

When the parent record already exists (has an `id`), `Add row` must inject the parent `id` into the child's inverse `relationField` value in the row draft. That inverse field must be hidden in the drawer.

---

**Rule 4.3.10 — `x2many` `Add row` honors `default_<field>` context and child arch `default="..."` values**

Populate new x2many rows using `default_<field>` context entries and the child arch's `default="..."` attribute values before local draft storage. Do not ignore these defaults.

---

### 4.4 API and RPC layer

**Rule 4.4.1 — All RPC calls go through `src/api/jsonv2.ts`**

Do not call `fetch` or `axios` directly from component code. All model RPC calls, session calls, and database calls go through the typed helpers in `jsonv2.ts`.

Why: The shared client injects auth headers, the active DB name, and session context in one place. Bypassing it means these headers are silently missing.

---

**Rule 4.4.2 — Read `.result` for success and `.error` for application-level failure**

All responses are wrapped in `JsonV2Response<T>`. Always check `.error` before reading `.result`. Never assume `.result` is populated if `.error` is not checked.

```tsx
// Good
const res = await callKW(model, "search_read", ...)
if (res.error) { handleError(res.error); return }
const records = res.result

// Bad
const records = (await callKW(...)).result  // unchecked error
```

---

**Rule 4.4.3 — DB cookie handling: read `basehub_db_name` and `basehub_db_locked` on init**

When the backend pins a tenant with `X-Database-Name`, the SPA must read `basehub_db_name` and `basehub_db_locked` cookies at init time to lock RPC calls to that DB and hide the manual database field on the login screen.

---

### 4.5 State management

**Rule 4.5.1 — Global persistent state goes in Zustand stores under `src/stores/`**

Use Zustand `create` with `persist` middleware for state that survives page reload (auth, i18n locale). Use plain Zustand `create` for transient global state. Do not use `useState` or `useContext` for state that is shared across unrelated components.

---

**Rule 4.5.2 — Auth store is the single source for credentials and company context**

Read `accessToken`, `db`, `uid`, `currentCompanyId`, and `allowedCompanyIds` from `useAuthStore`. Do not store copies of these values in component state or other stores.

---

**Rule 4.5.3 — Keep `kwargs.context` synchronized from the auth store**

RPC calls that need company context or locale must forward `kwargs.context` derived from the auth store values. Do not build `kwargs.context` from stale local variables.

---

### 4.6 View mode and navigation

**Rule 4.6.1 — `BaseView` forces initial collection view by device class**

On mobile, `BaseView` starts in `kanban`. On non-mobile, it starts from the first declared non-`form` mode only when the current sidebar menu is an explicit window action; otherwise it starts in `list`. Explicit record routes always open `form`. Do not override this logic without understanding all four cases.

---

**Rule 4.6.2 — View mode order is preserved from the backend action; backfill adds, never reorders**

`view_mode` normalization must preserve the declared order from the action, then backfill missing `list`, `kanban`, and (for `base.workflow`) `canvas` and `form`. Never reorder declared modes.

---

**Rule 4.6.3 — Sidebar and menu state are URL-driven**

Do not store the active menu item in component state. Use URL params as the source of truth so deep links and browser back/forward work correctly.

---

### 4.7 Workflow canvas

**Rule 4.7.1 — Default inbound trigger is `webhook`; tolerate legacy `trigger:http` on load**

When creating a new workflow canvas, set the trigger node to `trigger:webhook`. When loading a saved canvas, also accept `trigger:http` as a legacy alias — do not reject or error on it.

---

**Rule 4.7.2 — New nodes inherit defaults from `/wf/node-specs`**

When a new action node is added to the canvas, populate its default field values from the spec returned by `GET /wf/node-specs`. Do not hardcode defaults in the canvas component.

---

**Rule 4.7.3 — Node deletion calls `base.workflow.action.unlink` immediately**

When a node is deleted from the canvas, call `base.workflow.action.unlink` immediately. Do not defer deletion to the canvas save.

---

**Rule 4.7.4 — Canvas edits save through `replace_action_sequence` then `write({layout})`**

Canvas saves use two calls in order: first `base.workflow.replace_action_sequence` to persist the action sequence, then `base.workflow.write({layout})` to persist the visual layout. The order matters.

---

**Rule 4.7.5 — `workflow_model_name` field uses `widget="workflow_model_name"` combobox**

The `model_name` field on `base.workflow.action` uses a dedicated `widget="workflow_model_name"` combobox that fetches options from `/wf/sys-models`. Do not replace this with a generic text input or selection field.

---

### 4.8 Styling

**Rule 4.8.1 — Tailwind utilities are the default; custom CSS is the exception**

Use Tailwind classes for all layout, spacing, typography, color, border, and state styling. Only add custom CSS for cases Tailwind cannot express: third-party overrides, advanced selectors, keyframes, browser-specific fixes, or tightly scoped rich-content styling.

---

**Rule 4.8.2 — Build mobile-first; enhance with `sm:`, `md:`, `lg:` breakpoints**

Every changed UI must work on phone, tablet, and desktop. Start with mobile layout, then add responsive enhancements. Mobile layouts must not clip content, overlap controls, or cause accidental horizontal scroll.

---

**Rule 4.8.3 — Use `svh` when tracking visible mobile viewport height; avoid casual `100vh`**

Use `svh` when the layout must track the visible mobile viewport. Prefer `w-full`, percentages, `max-w-*`, and `min-h-screen` when they already solve the layout. Avoid `100vh` and `100vw` on mobile — they do not account for the browser chrome.

---

**Rule 4.8.4 — Do not duplicate Tailwind utilities in custom CSS**

If a layout can be expressed with Tailwind utilities, use them. Any custom CSS rule that duplicates a Tailwind utility is a bug.

---

### 4.9 TypeScript

**Rule 4.9.1 — Use strict types for all API response shapes**

Define TypeScript interfaces for every API response payload consumed from `jsonv2.ts`. Do not use `any` for response results in component code; use the typed response interfaces from `jsonv2.ts`.

---

**Rule 4.9.2 — `zod` is available for runtime validation at system boundaries**

Use `zod` to validate data arriving from the backend at boundaries where shape cannot be guaranteed (e.g., dynamic widget options, arch attribute parsing). Do not use `zod` for purely internal type narrowing where TypeScript's type system is sufficient.

---

**Rule 4.9.3 — Icons come from `lucide-react`; do not add other icon libraries**

All icons must use `lucide-react`. Do not import from `react-icons`, `heroicons`, or any other icon library.

Why: Multiple icon libraries produce inconsistent visual weight and bloat the bundle.

---

### 4.10 Known risks — areas requiring extra care

These are live issues in the codebase. New code must not make them worse; fixes are welcome.

| Risk area | File | Issue |
| --- | --- | --- |
| `onRecordsLoaded` dependency | `base-view.tsx` | Can retrigger fetch unnecessarily — check deps when touching this |
| CRM hardcoding in generic host | `base-view.tsx` | Legacy `CrmLead*` references — do not add more; remove when touching nearby code |
| Missing `AbortController` | `base-view.tsx` | In-flight fetches are not cancelled on unmount — add when refactoring fetch logic |
| Client-only search filter | `list-view.tsx` | Filters the current page only, not the server — do not worsen; push term to server domain in new list features |
| Regex inside quoted strings | `many2one-field.tsx` | `parseMany2OneWidgetOptions` regex can fire inside quoted attribute values — test with complex domain strings |

---

## 5. Anti-patterns

| Anti-pattern | Why it is wrong | Correct approach |
| --- | --- | --- |
| Model-specific logic in `src/components/ui/` | Pollutes shared primitives | Move to `src/modules/<module>/components/` |
| Inlining tag rendering in the arch renderer | Monolithic renderer, hard to test | Delegate to `*-tag.tsx` files |
| `setMode("readonly")` before save resolves | Hides save failures | Wait for async success before transitioning |
| Sending `[id, label]` tuple as many2one value | Backend expects scalar integer | Send integer ID only |
| Hardcoding `10` as many2one search limit | Breaks if limit changes | Use `MANY2ONE_SEARCH_LIMIT` |
| Calling `fetch` directly from components | Bypasses auth headers and DB context | Use helpers from `jsonv2.ts` |
| Using `useState` for global auth/company state | Duplicate state, sync bugs | Use `useAuthStore` from Zustand |
| Re-evaluating arch expressions in field components | Divergence from `form-arch-field-state.ts` | Read evaluated state; never re-evaluate |
| Using `100vh` / `100vw` on mobile | Browser chrome causes clipping | Use `svh`, `w-full`, or `min-h-screen` |
| Adding a second icon library | Bundle bloat, visual inconsistency | Use `lucide-react` only |
| CamelCase event names or kebab-case component files | Inconsistent with React conventions | PascalCase components, camelCase props and handlers |

---

## 6. Examples

### Good example — many2one field submit

```tsx
// Good: scalar ID in payload
function buildWritePayload(fields: Record<string, FieldValue>) {
  const payload: Record<string, any> = {}
  for (const [key, value] of Object.entries(fields)) {
    // many2one: submit integer ID only, not [id, label] tuple
    payload[key] = Array.isArray(value) ? value[0] : value
  }
  return payload
}
```

### Bad example — many2one field submit

```tsx
// Bad: sends [id, label] tuple — backend ORM rejects or misinterprets
payload[fieldName] = [selectedRecord.id, selectedRecord.name]
```

---

### Good example — guarded RPC response

```tsx
async function loadRecord(model: string, id: number) {
  const res = await callKW(model, "read", [[id]], { fields: ["name", "state"] })
  if (res.error) {
    toast.error(res.error.message)
    return null
  }
  return res.result?.[0] ?? null
}
```

### Bad example — unchecked RPC response

```tsx
// Bad: .result may be undefined if .error is set
const record = (await callKW(model, "read", [[id]])).result[0]
```

---

## 7. Enforcement and tooling

| Enforcement method | Tool / Config | Mandatory? |
| --- | --- | --- |
| TypeScript strict mode | `tsconfig.json` — `"strict": true` | Yes |
| ESLint + typescript-eslint | `eslint.config.*` — run in CI | Yes |
| `tsc --noEmit` must pass | CI gate | Yes |
| Tailwind-only styling | Convention + code review | Recommended |
| Icon library restriction | Convention + code review | Recommended |

---

## 8. Exceptions and escalation

When following a rule would produce a demonstrably worse outcome:

1. Document the exception in the PR description with the rule number (e.g., "Exception to Rule 4.3.1: ...").
2. Get sign-off from the frontend tech lead or a senior engineer.
3. Record the decision in the PR description or an ADR under `.agents/memory/planning/`.

---

## 9. Changelog

| Version | Date | Author | Changes |
| --- | --- | --- | --- |
| 1.0 | 2026-05-18 | Base HUB Team | Initial version covering structure, arch pipeline, field contracts, API, state, view mode, canvas, styling, and TypeScript |
