# base-credential: User and Administrator Guide

## 1. Purpose and who uses it

`base-credential` stores the credentials Base HUB uses to sign in to other systems. It keeps the secrets encrypted, calls the remote auth endpoint to obtain an access token, and refreshes that token automatically before it expires. Workflows reference a credential by name (`@cred:<name>`) and always get a live token instead of a static secret.

| Audience | What they do here |
| --- | --- |
| System administrator | Creates and maintains credentials, tests logins, checks refresh status |
| Workflow author | References `@cred:<name>` in workflow action parameters |

## 2. Prerequisites and installation

- Depends on `base` and `base-automation` (the refresh job is dispatched by the automation scheduler).
- Always installed (`auto_install`); it cannot be disabled or uninstalled. The `workflow` module depends on it.
- Only users in `base.group_system` can see and edit credentials.

## 3. Key concepts

| Concept | Meaning |
| --- | --- |
| Credential (`sys.credential`) | One named connection to another system |
| Name | Unique reference name, used as `@cred:<name>` |
| Credential Type | The sign-in flow, see the table below |
| Token State | Current access token, refresh token and Expired Time (encrypted at rest, never shown after save) |
| Refresh Before Seconds | How long before expiry the token is renewed (default 300) |
| Last Attempt | Status (Ok or Error), error message and time of the most recent login |

| Credential Type | Fields to fill |
| --- | --- |
| `rbac` (RBAC) | Auth URL, Username, Password |
| `oidc_client` (OIDC Client Credentials) | Auth URL, Client ID, Client Secret, Scope (optional) |
| `oidc_password` (OIDC Password) | Auth URL, Username, Password, Client ID, Client Secret, Scope (optional) |
| `apikey` (API Key) | Access Token / API Key only; Auth URL not required; no expiry |

## 4. Where to find it

| Menu | Path |
| --- | --- |
| Admin > Credentials (Thông tin xác thực) | `/web/sys.credential` |

List columns: Name, Credential Type, Auth URL, Expired Time, Last Login Status, Active. The form groups are Connection, RBAC / OIDC Password, OIDC Client, Token State and Last Attempt.

## 5. Step-by-step tasks

### Create a credential
1. Open Admin > Credentials and create a new record.
2. Enter a unique Name (for example `crm-api`); this is what workflows will reference.
3. Pick the Credential Type and fill the fields listed above. Auth URL is required for every type except API Key.
4. Save. Secrets are encrypted and will not be displayed again.

### Test a login
For OIDC types, click Try at the top of the form. The Last Attempt group then shows Ok or Error, the message and time. For API Key, paste the key into Access Token / API Key.

### Use it in a workflow
In a workflow action parameter, write `@cred:<name>`. At execution time it resolves to the current, auto-refreshed token.

### Change a secret
Type the new value in the secret field and save. Leaving a secret field untouched keeps the stored value.

### Disable a credential
Untick Active. The refresh job then skips it.

## 6. Administration and configuration

| Item | Detail |
| --- | --- |
| Access | `base.group_system`: read, write, create, delete |
| Seeds | Automation action "Credential: Refresh Due Tokens" and an automation cron of the same name, active by default, running every 60 seconds (max 5 failures) |
| Encryption | Password, Client Secret, Access Token and Refresh Token are encrypted at rest and never returned by the API |
| Refresh timing | Per-credential Refresh Before Seconds; the scheduler checks every 60 seconds |

## 7. API and automation touchpoints

| Touchpoint | Purpose |
| --- | --- |
| Method `login` on `sys.credential` | Immediate (re-)authentication, same as the Try button |
| `@cred:<name>` in workflows | Resolves to a live token |
| Automation handler `credential.refresh_due_credentials` | Renews tokens close to expiry |
| MCP `credential.record.search_read`, `read`, `create`, `write`, `unlink` | Manage credentials; secrets never returned; `create` needs an `idempotency_key` |

## 8. Troubleshooting and FAQ

| Problem | Likely cause and action |
| --- | --- |
| Last Login Status = Error | Read Last Login Error: wrong URL, username, password or client secret, or the remote system is down. Fix and click Try |
| Token not renewing | Credential is not Active, or the "Credential: Refresh Due Tokens" cron is inactive or has hit its failure limit |
| Save fails: Auth URL required | Every type except API Key needs an Auth URL |
| Save fails: type not supported | Use one of `rbac`, `oidc_client`, `oidc_password`, `apikey` |
| Cannot see saved password | By design; re-enter it to change it |

## 9. Limits and notes

- The Try button appears only for OIDC Client and OIDC Password types; use the `login` method for others.
- API Key credentials have no expiry and are never refreshed.
- Name must be unique so that `@cred:<name>` is unambiguous.
- The module cannot be uninstalled because workflows rely on it.
