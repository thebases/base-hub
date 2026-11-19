---
name: base-credential
version: 0.0.1
category: System
installable: true
depends: [base, base-automation]
auto_install: true
frontend_widgets: {}
---

# Credential

Kernel-adjacent module for Base Hub that manages credentials this system uses
to authenticate against other systems.

This module owns:

- `sys.credential`

It stores connection secrets (RBAC username/password, OIDC client-credential,
OIDC password grant, or a static API key), calls the configured auth endpoint
to obtain an access token, and auto-refreshes the token before it expires. The
refresh job runs as a `go_handler` automation.action dispatched by
`base-automation`'s scheduler bridge, not as its own direct `sys.cron` job.

Like `base-automation`, this module is always installed and cannot be
disabled or uninstalled. It depends on `base-automation` for cron dispatch and
is itself a dependency of `workflow`, which resolves `@cred:<name>` references
at execution time to a live, auto-refreshed token instead of a static secret.
