# `<xpath>` tag logic for backend and frontend

This document explains how inherited `ui.view` XML currently works in this codebase, with a review-first lens.

## Scope

The relevant implementation is split across:

- Backend import and storage:
  - `app/internal/services/masterdata/view_xml.go`
  - `app/internal/models/ui_view.go`
- Frontend consumption:
  - `app/frontend-admin/src/api/jsonv2.ts`
  - `app/frontend-admin/src/components/views/control-panel/search-arch-parser.ts`
  - `app/frontend-admin/src/components/views/base-view.tsx`

There is no frontend-side `<xpath>` executor in the current repo. The backend resolves inherited views first, then the frontend receives final merged XML.

## Review findings

### Logic

`app/internal/models/ui_view.go`, `resolveStoredUIView` / `resolvePrimaryUIView`

The current resolver does not fully implement the documented `mode="primary"` inheritance behavior described on the `ui.view.mode` field. The field help says a primary inherited view should resolve the closest primary parent first, then apply this view's inheritance specs (`<xpath/>`) on top. The code does not do that.

What happens now:

- If `mode="extension"`, the resolver walks up `inherit_id` until it finds a non-extension ancestor.
- If `mode="primary"`, the resolver stops immediately and treats the current row's own `arch` as the base arch.

Why this matters:

- A primary view that inherits another primary view and contains only `<xpath>` operations will not be merged against its parent the way the help text suggests.
- In that case the view can behave like a standalone root arch instead of a parent-plus-patch view.

Concrete fix:

- Update `resolveStoredUIView` so `mode="primary"` with `inherit_id` first resolves the parent primary arch, then applies the current row's extension arch through `mergeUIViewArch`.
- Keep the current `extension` chain logic for standard inherited extension views.

### Logic

`app/internal/models/ui_view.go`, `parseSelector`

The selector parser only supports a very small XPath subset, but the error messages and naming make it look more general than it is.

What is actually supported:

- `//tag`
- `//tag[@attr='value']`
- shorthand non-`<xpath>` operation nodes such as `<field name="x" position="after">...</field>`

What is not supported:

- Nested paths like `//group/field[@name='x']`
- Relative paths beyond trimming a leading `.`
- Multiple predicates
- Index predicates like `[1]`
- Functions such as `contains(...)`
- Attribute existence predicates like `[@name]`
- Unions, axes, or namespace-aware matching

Why this matters:

- Any extension XML copied from fuller XPath usage elsewhere can silently become incompatible with this backend subset.
- The code will fail with `ui.view xpath "..." did not match any node`, even though the expression may be valid XPath in general.

Concrete fix:

- Either document this clearly as a selector subset, or replace `parseSelector` with a real XPath-capable evaluator.
- If keeping the lightweight parser, reject unsupported syntax explicitly with a clearer validation error before matching.

## Backend logic

## 1. Import phase

View XML is loaded by `loadXMLImportRecords` and `xmlFieldValue` in `app/internal/services/masterdata/view_xml.go`.

Important behavior:

- `<field name="arch" type="xml">...</field>` is stored as raw inner XML string.
- `<field name="arch_base" type="xml">...</field>` is treated the same way.
- `<field name="inherit_id" ref="base.some_view"/>` is converted into the qualified external id string during import.

At this stage the backend does not execute `<xpath>`. It only stores the view definition.

## 2. View normalization

`normalizeUIViewValues` in `app/internal/models/ui_view.go` prepares stored rows.

Important behavior:

- `arch`, `arch_base`, and `arch_db` are normalized.
- If only one arch source is provided, the others are backfilled.
- `type` may be inferred from the root XML tag, for example `form`, `list`, `kanban`, `search`.

This means an extension view may store an XML payload whose root is `<data>` and whose children are `<xpath>` operations.

## 3. Runtime resolution entry points

The main runtime entry points are:

- `get_combined_arch`
- `get_view`
- `get_views`

These eventually call `resolveStoredUIView`.

`get_combined_arch` returns only the final merged XML string.

`get_view` returns:

- `arch`: final merged XML
- `pattern`: backend-parsed tree representation of the final XML
- `meta.primary_view_id`
- `meta.extension_ids`

Important: the `pattern` is generated after merge. It does not preserve `<xpath>` operations as operations.

## 4. Primary view resolution

`resolvePrimaryUIView` walks up the inheritance chain only while the current row has:

- `inherit_id != nil`
- `mode == "extension"`

So the effective base arch is:

- the current row itself, if it is primary
- the closest non-extension ancestor, if the requested row is an extension

Cycle protection exists through a `seen` map keyed by view id.

## 5. Extension collection order

`loadUIViewExtensions(parentID, modelName)` loads rows where:

- `inherit_id = parentID`
- `active = true`
- `model = modelName` or model is empty

Then it sorts by:

- `priority ASC`
- `id ASC`

Then it recurses into children, so merge order is depth-first in priority order.

Only rows with `mode="extension"` are added to the applied extension list.

## 6. Merge engine overview

`mergeUIViewArch(baseArch, extensionArch)` does the actual work:

1. Parse base XML into a custom tree of `uiXMLNode`
2. Parse extension XML into another `uiXMLNode` tree
3. Extract view operations with `viewOperations`
4. Apply each operation to the base tree with `applyViewOperation`
5. Render the modified tree back to XML with `renderArchXML`

The merge engine is custom. It does not use a standard XPath library.

## 7. How operations are discovered

`viewOperations(root)` interprets the extension arch like this:

- If the root itself is `<xpath>`, it is treated as one operation.
- If the root has a `position` attribute, it is also treated as one operation.
- Otherwise, each element child of the root is treated as one operation.

This supports both forms:

```xml
<data>
  <xpath expr="//field[@name='name']" position="after">
    <field name="email"/>
  </xpath>
</data>
```

and shorthand selector form:

```xml
<field name="name" position="after">
  <field name="email"/>
</field>
```

## 8. How the target selector is built

`operationExpr(op)` resolves the selector in this order:

1. Use the explicit `expr` attribute if present.
2. Else, if the operation node has `name`, build `//<tag>[@name='...']`
3. Else, if it has `id`, build `//<tag>[@id='...']`
4. Else, fall back to `//<tag>`

Examples:

- `<xpath expr="//form">` -> `//form`
- `<field name="name" position="after">` -> `//field[@name='name']`
- `<page id="extra_tab" position="replace">` -> `//page[@id='extra_tab']`

## 9. Supported selector subset

`parseSelector` reduces the selector into:

- `Tag`
- optional `AttrKey`
- optional `AttrVal`

Supported examples:

- `//form`
- `//sheet`
- `//field[@name='name']`
- `//page[@id='details']`
- `.//field[@name='email']`

Matching is global descendant search through `findTargets`, not true path navigation.

That means:

- `//field[@name='name']` works
- `//group/field[@name='name']` does not work in the current parser

## 10. How node matching works

`findTargets` walks every element node in the base tree.

`nodeMatches` checks:

- tag equality, unless selector tag is `*`
- attribute equality, if an attribute predicate was parsed

The first match is not special. All matching nodes are collected and the operation is applied to all of them.

## 11. Supported `position` behaviors

`applyViewOperation` supports these positions:

### `inside` or empty position

Payload nodes are cloned and appended as children of every target node.

Effect:

- This is append-at-end, not insert-at-start.

### `attributes`

Only child nodes named `<attribute>` are processed.

Example:

```xml
<xpath expr="//form" position="attributes">
  <attribute name="string">Partner Card</attribute>
</xpath>
```

Behavior:

- `name` attribute selects which target attribute to modify.
- `innerText(child)` becomes the new value.
- Empty values remove the attribute through `setNodeAttr`.

### `replace`

The target node is replaced in its parent with cloned payload nodes.

### `before`

Cloned payload nodes are inserted immediately before the target in the parent's children list.

### `after`

Cloned payload nodes are inserted immediately after the target in the parent's children list.

## 12. Payload rules

`payloadNodes(op)` ignores:

- text-only child nodes
- `<attribute>` children when `position="attributes"`

Everything else is cloned with `cloneNode` before insertion, so one payload can be applied to multiple targets safely.

## 13. Error conditions

Current merge errors include:

- `ui.view extension node requires expr or a matching element selector`
- `empty xpath expr`
- `ui.view xpath "..." did not match any node`
- `ui.view cannot apply replace/before/after on root node`
- `ui.view unsupported xpath position "..."`

These are backend merge-time errors. If they occur, the frontend never receives a final arch.

## Frontend logic

## 1. Frontend does not execute `<xpath>`

The frontend fetches already-resolved XML:

- `fetchUIViewCombinedArch(id)` calls `ui.view.get_combined_arch`
- `fetchCrmLeadSingleView(...)` loads both the `ui.view` row and the merged `arch`

So the contract is:

- backend owns inheritance resolution
- frontend consumes the final merged arch only

This is the most important architectural point.

## 2. What the frontend receives

The frontend view payload contains:

- `arch`: final XML string
- `fields`: field metadata
- `statusbar_visible` and other view metadata

Once the arch arrives in the browser, it is treated as plain XML for parsing and rendering configuration.

## 3. Search view parsing

`parseSearchArch` in `app/frontend-admin/src/components/views/control-panel/search-arch-parser.ts`:

- parses the XML with `DOMParser`
- requires root tag `search`
- reads direct child `<field>` nodes
- reads direct child `<filter>` nodes
- reads `<group>` children containing group-by filters

There is no logic for `<xpath>` here. If an unresolved extension arch containing `<xpath>` ever reached the frontend, this parser would not apply it.

## 4. List view parsing

`parseListFieldNodes` in `app/frontend-admin/src/api/jsonv2.ts`:

- parses the XML with `DOMParser`
- queries `list > field`
- reads attributes such as `name`, `string`, `optional`, `invisible`, `readonly`, `required`

Again, this assumes the backend already returned a concrete final `<list>` arch.

## 5. Kanban view parsing

`parseKanbanArchConfig` in `app/frontend-admin/src/components/views/base-view.tsx`:

- parses final `kanban` XML
- reads `default_group_by`
- inspects `templates > t[t-name="card"]`
- extracts field declarations from the resolved template

No inheritance merge happens in the browser.

## 6. Form rendering path

`BaseView` passes `viewData.arch` directly into the form rendering layer.

So for forms too:

- frontend consumes final XML
- frontend does not patch parent XML with child `<xpath>` specs

## End-to-end flow

The full lifecycle is:

1. Module XML imports store `arch`, `arch_base`, and `inherit_id`
2. A view request reaches `ui.view.get_combined_arch`, `get_view`, or `get_views`
3. Backend resolves the base view and extension chain
4. Backend applies each `<xpath>` or shorthand operation with the custom merge engine
5. Backend returns final merged XML
6. Frontend parses that final XML for search, list, kanban, and form rendering

## Practical rules for writing extension views in this repo

To stay compatible with the current implementation:

- Prefer `//tag[@name='value']` or `//tag[@id='value']`
- Avoid deep path XPath such as `//group/field[...]`
- Use only `inside`, `attributes`, `replace`, `before`, or `after`
- Assume matching applies to all matching nodes, not only the first
- Treat the backend as the only place where inheritance is resolved

## Short summary

In this repo, `<xpath>` is a backend-only view inheritance mechanism implemented by a custom lightweight merge engine, not by a full XPath library. The frontend never resolves `<xpath>` itself; it only parses the final merged arch returned by the backend. The two main caveats today are the limited selector subset and the mismatch between documented and actual `mode="primary"` inheritance behavior.
