---
name: base-automation
version: 0.0.1
category: System
installable: true
depends: [base]
auto_install: true
frontend_widgets: {}
---

# Base Automation

Kernel-adjacent automation module for Base Hub.

This module owns internal cron-driven automation and its user-defined action
runtime:

- `automation.cron`
- `automation.action`

Action types:

- `lua` — sandboxed `gopher-lua` script executed against a JSON payload.
- `sys_model` — declarative CRUD against any runtime model.

Like `base`, this module is always installed and cannot be disabled or
uninstalled.
