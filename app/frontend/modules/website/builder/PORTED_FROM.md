# Base website builder: provenance

This builder is an in-house implementation, not a file-by-file port of another builder. It follows the
contracts of the reference design (a plugin has an id and dependencies, every change goes through an
operation that gives undo and redo, option panels come from declarative schemas, a block has a stable
`bh-<name>` class and a `data-snippet` key, public behaviors load only when used) and is built only on
`@thebase/ui` and the Owl runtime embedded in it (imported as `nodexui/owl`).

There is no host shim of another framework: server calls go straight to Base HUB RuntimeMethods through
`host/*.js` (`jsonv2.js` underneath). The earlier plan to port files one by one was dropped because each
file would have needed rewriting for server calls anyway; see
`.agents/planning/2026-10-15-website-builder-plan.md`, section 9 ("Deviation from the literal port").

Ported or copied code must not contain reference-implementation vocabulary
(`scripts/check_website_constraints.sh`).
