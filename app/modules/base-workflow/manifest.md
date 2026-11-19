---
name: base-workflow
version: 0.0.1
category: System
installable: true
depends: [base, base-automation, base-credential, base-bus]
auto_install: false
frontend_widgets: {}
---

# Workflow

Scheduling and async workflow support module for Base Hub.

This module owns workflow-specific runtime models and UI metadata for:

- `base.workflow.action`
- `base.workflow`
- `base.workflow.outbound`
- `base.workflow.run`
