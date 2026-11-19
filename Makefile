.PHONY: build build\:new-admin build\:be run deploy migrate

# app/frontend is a static, no-bundler SPA (plain ES modules loaded
# directly by the browser) — nothing to compile, just stamp its version
# manifest so a fresh deploy is distinguishable from the previous one.
build\:new-admin:
	BUILD_VERSION=$${BUILD_VERSION:-base_admin_$$(date +%s)} node scripts/write-frontend-version.mjs app/frontend/version.json

build\:be:
	cd app && $(MAKE) build

build: build\:new-admin build\:be

# Ships app/build/ to production and runs schema migrations before
# restarting the service. See app/Makefile's `deploy` target for the
# full recipe/variables (DEPLOY_HOST, DEPLOY_USER, DEPLOY_PATH, SSH_OPTS,
# MIGRATE_CMD); overrides passed here (e.g. `make deploy DEPLOY_HOST=...`)
# propagate to the sub-make automatically.
deploy:
	cd app && $(MAKE) deploy

# Runs the backend's standalone migrate tool. See app/Makefile's
# `migrate` target for variables (MIGRATE_CONFIG, MIGRATE_CMD, MIGRATE_DB).
migrate:
	cd app && $(MAKE) migrate

run:
	cd app && $(MAKE) run
