# Knowledge: user and administrator guide

## 1. Purpose and who uses it

`knowledge` is the markdown documentation module of Base HUB. It shows built-in product documentation, and lets administrators write and organise their own markdown documents, all inside the app.

| Audience | What they do with it |
| --- | --- |
| Administrators (superadmin, `base.group_system`) | See the KnowLedge sidebar section, read documents, create, edit, rename and delete their own documents |
| Integrators and AI agents | Read, search and manage documents through the `knowledge.*` MCP tools or the `/md` routes |

## 2. Prerequisites and install

- Depends only on `base`. Not a core module: `installable: true`, `auto_install: false`. Install it from the Apps list.
- If the module is not installed for a tenant, the document menu is simply empty (no error).
- Writable documents are stored as files on the server, under the tenant's shared folder. The sub-folder name comes from config key `knowledge.publicDocsPath` (default `public_docs`).

## 3. Key concepts

| Concept | Plain-words meaning |
| --- | --- |
| Document | A `.md` markdown file. Only the `.md` extension is accepted |
| Logical path | Every document path starts with its namespace, for example `public/notes/todo.md` |
| `base` namespace | Built-in documentation bundled with the product. Read-only. Shown only on the Home tenant |
| `user_guide` namespace | Built-in user guides, nested inside the bundle. Read-only, shown to every tenant |
| `public` namespace | The only writable area. Separate per tenant. Folders are allowed |
| Name / label | Optional display label shown in the document menu |
| Search index | A full-text index of document content used by content search |

## 4. Where to find it

| Location | What it gives you |
| --- | --- |
| Sidebar section "KnowLedge" | A tree of folders and documents; click one to read it. Visible to superadmin users only |
| Document viewer/editor page | Render a document; create, edit, rename, delete in the `public` namespace |
| `GET /md/menu.json` | The document tree as JSON |
| `GET /md/<namespace>/<path>.md` | The raw markdown of a document |

## 5. Step-by-step tasks

### Read a document
1. Open the KnowLedge section in the sidebar.
2. Expand folders and click a document.

### Create a document
1. In the editor, choose New document.
2. Enter a path or folder and file name ending in `.md`. The app places it under `public/` automatically.
3. Write the markdown and save. It fails with "already exists" if the path is taken.

### Edit a document
Open a `public` document, change the content, save. Built-in `base` and `user_guide` documents cannot be edited.

### Rename or move a document
Use rename and give the new path. It fails if the target already exists.

### Delete a document
Delete a `public` document from the editor. This removes the file and its search entry.

### Find a document by topic (agents)
Use `knowledge.doc.search` for content relevance, or `knowledge.doc.find` for a name or keyword.

## 6. Admin and configuration

- Access: the sidebar menu is hidden from every authenticated user who is not a superadmin. This only hides the menu; it is not a permission check on the `/md/*` document routes.
- Storage: set `knowledge.publicDocsPath` (default `public_docs`) to change the folder name under each tenant's shared folder. Back up that folder to back up the documents.
- Seeds: no data seeds. The `base` and `user_guide` documents ship inside the application.
- Search: uses Postgres full-text search when available. If the database does not allow it (for example no permission to create an index), it automatically falls back to a JSON index file in the tenant's shared folder (`knowledge-search-index.json`).
- Index freshness: a full sync runs once per process and tenant on the first content search; later creates, edits, renames and deletes update the index immediately.

## 7. API and automation touchpoints

| Touchpoint | Purpose |
| --- | --- |
| `GET /md/menu.json` | Document tree (empty for non-superadmin users and uninstalled tenants) |
| `GET /md/*` | Raw markdown of one document |
| `POST /md/api/files` | Create a `public` document (201, or 409 if it exists) |
| `PUT /md/api/files/*` | Overwrite a `public` document (404 if missing) |
| `POST /md/api/files/rename` | Move/rename (404 missing, 409 target exists) |
| `DELETE /md/api/files/*` | Delete a `public` document (404 if missing) |
| MCP `knowledge.doc.list` | List all documents visible to the tenant |
| MCP `knowledge.doc.find` | Find by name or keyword |
| MCP `knowledge.doc.search` | Rank by content relevance |
| MCP `knowledge.doc.read` | Read one document by logical path |
| MCP `knowledge.doc.create` | Create; requires `idempotency_key` so retries are safe |
| MCP `knowledge.doc.update` / `rename` / `delete` | Manage `public` documents |

Successful write calls return `{"result":{"path": ...}}`.

## 8. Troubleshooting and FAQ

| Problem | Check |
| --- | --- |
| The KnowLedge section is missing | You are not a superadmin, or the module is not installed for this tenant |
| "markdown files must use the .md extension" | Add `.md` to the file name |
| 409 on create or rename | A document already exists at that path; pick another |
| Cannot edit or delete a document | It is in `base` or `user_guide`, which are read-only. Copy the content to `public/` |
| Search misses a new document | Use `knowledge.doc.find` for name matches; content search updates on write and on the next sync |
| `base` documents not visible | They are shown on the Home tenant only |

## 9. Limits and notes

- Only markdown files; no attachments or images are managed by this module.
- Writes are limited to the `public` namespace. A path outside it is rejected.
- Documents are plain files, not records: there is no per-document permission, history or versioning.
- The Postgres index table `tbl_knowledge_document` is internal and is not a user-facing model.
