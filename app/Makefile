# Base Hub backend — verification entrypoints (SRS §4.1 PERF-01, §4.6 CONF-04)
# plus the canonical build/deploy recipe, shared by local ops and
# .gitlab-ci.yml's build:backend/deploy jobs.
#
# Previously build.sh (hardcoded WorkingDirectory=/opt/services/base-hub,
# scp'd manually to server "base_40" via deploy.sh's REMOTE_DIR=/opt/base-hub)
# and .gitlab-ci.yml (DEPLOY_HOST=base_150, DEPLOY_PATH=/opt/services/base-hub,
# with a `sed s|/opt/base-hub|...|` that never matched because build.sh never
# emitted that literal path) were three independently-hardcoded copies of the
# same install path. That drift let a manual `./deploy.sh` run ship a stale
# binary — built before the /frontend (in-progress admin rewrite) route and
# its go:embed existed — to a host/path the CI pipeline doesn't touch, which
# is why /frontend 404'd in production while /admin and / kept working. There
# is now exactly one place (DEPLOY_HOST/DEPLOY_USER/DEPLOY_PATH below) that
# defines where a build gets installed; CI passes the same variables in so
# `make build`/`make deploy` run locally and in CI always agree.

GO ?= go
BINARY_NAME := base-hub
SERVICE_NAME := base-hub
GOOS ?= linux
GOARCH ?= amd64
CGO_ENABLED ?= 0
SERVICE_USER ?= deploy

BUILD_DIR := build
OUTPUT := $(BUILD_DIR)/$(BINARY_NAME)
SERVICE_FILE := $(BUILD_DIR)/$(SERVICE_NAME).service
CONFIG_SOURCE := internal/config/config.yaml
CONFIG_TARGET := $(BUILD_DIR)/config.yaml
# Runs on the deploy host (piped over ssh) to keep app.encryptKey when BASEHUB_ENCRYPT_KEY is unset.
ENCRYPT_KEY_SCRIPT := scripts/deploy_encrypt_key.sh

# Standalone schema-migration tool (internal/cmd/migrate). It embeds its SQL
# files at compile time (see migrationFiles in main.go) specifically so this
# compiled binary — unlike a plain `go run` invocation — can be shipped to
# and executed on the deploy host, which has no repo checkout or Go toolchain.
MIGRATE_BINARY := $(BINARY_NAME)-migrate
MIGRATE_OUTPUT := $(BUILD_DIR)/$(MIGRATE_BINARY)
# Dev-convenience default for `make migrate` (mirrors the old migrate.sh);
# override to point at a different environment, e.g.
# MIGRATE_CONFIG=internal/config/config.yaml.
MIGRATE_CONFIG ?= internal/config/config_dev.yaml
MIGRATE_CMD ?= up
MIGRATE_DB ?=

# Must match .gitlab-ci.yml's DEPLOY_HOST/DEPLOY_USER/DEPLOY_PATH variables —
# keep both in sync if the real production install path ever changes.
DEPLOY_HOST ?= base_150
DEPLOY_USER ?= deploy
DEPLOY_PATH ?= /opt/services/$(SERVICE_NAME)
SSH_OPTS ?=

.PHONY: build vet test conformance perf deploy migrate

run:
	@echo "Stamping frontend/version.json..."
	@echo '{"version": "base-$(shell date +%Y%m%d-%H%M%S)"}' > frontend/version.json
	@echo " Starting service..."
	go run ./main.go start --force-config -c ${CONFIG_SOURCE}

# Produces the exact deployable bundle: binary, systemd unit (baked with this
# invocation's DEPLOY_PATH/SERVICE_USER, no post-build sed needed), and
# config.yaml, all under build/. Safe to run for local smoke builds too —
# DEPLOY_PATH only affects the unit file's WorkingDirectory/ExecStart, never
# where this command writes locally.
build:
	@mkdir -p "$(BUILD_DIR)"
	@echo "Generating backend module import registry..."
	$(GO) generate ./modules/all
	@echo "Generating alias API OpenAPI docs (frontend/vendor/base_apidoc/open_apidoc*.json)..."
	$(GO) run ./internal/tools/generate_open_apidoc 2>/dev/null
	@echo "Stamping frontend/version.json..."
	@echo '{"version": "base-$(shell date +%Y%m%d-%H%M%S)"}' > frontend/version.json
	@echo "Building Go service for $(GOOS)/$(GOARCH)..."
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o "$(OUTPUT)" .
	@echo "Building migrate tool for $(GOOS)/$(GOARCH)..."
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o "$(MIGRATE_OUTPUT)" ./internal/cmd/migrate
	@{ \
		echo "[Unit]"; \
		echo "Description=IPN Service"; \
		echo "After=network.target"; \
		echo ""; \
		echo "[Service]"; \
		echo "Type=simple"; \
		echo "User=$(SERVICE_USER)"; \
		echo "Group=$(SERVICE_USER)"; \
		echo "WorkingDirectory=$(DEPLOY_PATH)"; \
		echo "ExecStart=$(DEPLOY_PATH)/$(BINARY_NAME) start -c $(DEPLOY_PATH)/config.yaml"; \
		echo "StandardOutput=journal+console"; \
		echo "StandardError=journal+console"; \
		echo "Restart=on-failure"; \
		echo "RestartSec=3s"; \
		echo ""; \
		echo "[Install]"; \
		echo "WantedBy=multi-user.target"; \
	} > "$(SERVICE_FILE)"
	@cp "$(CONFIG_SOURCE)" "$(CONFIG_TARGET)"
	@echo "Build succeeded: $(OUTPUT)"
	@echo "Migrate tool built: $(MIGRATE_OUTPUT)"
	@echo "Service file created: $(SERVICE_FILE) (WorkingDirectory=$(DEPLOY_PATH))"
	@echo "Config copied to: $(CONFIG_TARGET)"

# Local dev convenience, replaces migrate.sh: runs the migrate tool from
# source (`go run`, no rebuild needed) against MIGRATE_CONFIG. Examples:
#   make migrate                                    # up, against config_dev.yaml
#   make migrate MIGRATE_CMD=status
#   make migrate MIGRATE_CMD=down
#   make migrate MIGRATE_CONFIG=internal/config/config.yaml MIGRATE_DB=hub.example.vn
migrate:
	$(GO) run ./internal/cmd/migrate -config="$(MIGRATE_CONFIG)" $(if $(MIGRATE_DB),-db="$(MIGRATE_DB)") "$(MIGRATE_CMD)"

vet:
	$(GO) vet ./...
	# The conformance suite is build-tagged, so plain vet never compiles it.
	$(GO) vet -tags conformance ./tests/...

test:
	$(GO) test ./... -count=1

# Runtime conformance suite (CONF-01..03). Needs a reachable PostgreSQL; see
# tests/conformance/README.md for the RUNTIME_GRAPH_TEST_DB_* convention.
conformance:
	$(GO) test -tags conformance ./tests/conformance/... -count=1 -v

# k6 load harness (PERF-01) against a running, seeded server. Thresholds from
# SRS §4.1 fail the run with measured percentiles; see tests/perf/README.md.
perf:
	./tests/perf/run.sh

# Ships build/ to DEPLOY_USER@DEPLOY_HOST:DEPLOY_PATH, then (re)starts the
# systemd unit. Mirrors .gitlab-ci.yml's deploy job step-for-step so a manual
# `make deploy` lands in the identical place the CI pipeline does — pass
# SSH_OPTS to point at a specific known_hosts file, same as CI does. Schema
# migrations are no longer run as part of deploy; run them explicitly on
# DEPLOY_HOST via the shipped $(MIGRATE_BINARY), or locally with `make migrate`.
deploy:
	@command -v ssh scp >/dev/null || { echo "ssh/scp not found"; exit 1; }
	@# app.encryptKey is never committed or kept in build/ (a CI artifact); it comes from the
	@# masked BASEHUB_ENCRYPT_KEY variable and is checked BEFORE the service is stopped.
	@# Unset = bypass: the host keeps the encryptKey already in its config.yaml (see below);
	@# set but malformed is still a hard failure.
	@if [ -z "$$BASEHUB_ENCRYPT_KEY" ]; then echo "WARNING: BASEHUB_ENCRYPT_KEY not set; keeping the encryptKey already in $(DEPLOY_PATH)/config.yaml"; \
		else printf '%s' "$$BASEHUB_ENCRYPT_KEY" | grep -Eq '^[A-Za-z0-9_-]{16,}$$' || { echo "BASEHUB_ENCRYPT_KEY must be >=16 chars of [A-Za-z0-9_-] (the current app.encryptKey)"; exit 1; }; fi
	@# Bypass path: refuse to deploy (service left running) when the host config has no key to keep.
	@if [ -z "$$BASEHUB_ENCRYPT_KEY" ]; then ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "bash -s -- check '$(DEPLOY_PATH)'" < $(ENCRYPT_KEY_SCRIPT); fi
	@echo "== Stop $(SERVICE_NAME).service on $(DEPLOY_HOST) =="
	ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "if systemctl list-unit-files '$(SERVICE_NAME).service' --no-legend | grep -q .; then sudo systemctl stop '$(SERVICE_NAME).service'; else echo 'Unit not installed yet; skipping stop.'; fi"
	@echo "== Copy $(BUILD_DIR)/ to $(DEPLOY_PATH) =="
	ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "mkdir -p '$(DEPLOY_PATH)' '$(DEPLOY_PATH)/data'"
	@# Without a CI key, snapshot the live config first: scp below overwrites it with the
	@# build's placeholder (encryptKey: ""), which would orphan every encrypted device secret.
	@if [ -z "$$BASEHUB_ENCRYPT_KEY" ]; then ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "bash -s -- snapshot '$(DEPLOY_PATH)'" < $(ENCRYPT_KEY_SCRIPT); fi
	scp $(SSH_OPTS) -r $(BUILD_DIR)/* "$(DEPLOY_USER)@$(DEPLOY_HOST):$(DEPLOY_PATH)/"
	@# Overwrite the shipped placeholder config with a key-injected copy built in a private temp
	@# file (awk reads the key from ENVIRON, so it never appears in argv or the job log).
	@# Bypass path: carry the previous config's encryptKey (either spelling) into the new config on
	@# the host itself (the key never crosses the CI log). Fails and restores the previous config if
	@# the result has no key; config.yaml.prev is kept as a backup.
	@if [ -z "$$BASEHUB_ENCRYPT_KEY" ]; then \
		echo "== Keep existing app.encryptKey in $(DEPLOY_PATH)/config.yaml =="; \
		ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "bash -s -- merge '$(DEPLOY_PATH)'" < $(ENCRYPT_KEY_SCRIPT); \
	fi
	@if [ -n "$$BASEHUB_ENCRYPT_KEY" ]; then \
		echo "== Inject app.encryptKey into $(DEPLOY_PATH)/config.yaml =="; \
		tmp=$$(mktemp) && chmod 600 "$$tmp" && \
		awk '$$1 == "encryptKey:" { sub(/encryptKey:.*/, "encryptKey: \"" ENVIRON["BASEHUB_ENCRYPT_KEY"] "\"") } { print }' "$(CONFIG_TARGET)" > "$$tmp" && \
		grep -q 'encryptKey: "..*"' "$$tmp" && \
		scp $(SSH_OPTS) "$$tmp" "$(DEPLOY_USER)@$(DEPLOY_HOST):$(DEPLOY_PATH)/config.yaml"; \
		status=$$?; rm -f "$$tmp"; exit $$status; \
	fi
	ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "chmod 600 '$(DEPLOY_PATH)/config.yaml'"
	ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "chmod +x '$(DEPLOY_PATH)/$(BINARY_NAME)' '$(DEPLOY_PATH)/$(MIGRATE_BINARY)'"
	@# Sync (not just install-if-missing): a stale unit left from an earlier
	@# layout (e.g. ExecStart=/opt/services/base-hub-single/...) otherwise
	@# survives every deploy and fails with status=203/EXEC.
	@echo "== Install/refresh unit file =="
	ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "if cmp -s '$(DEPLOY_PATH)/$(SERVICE_NAME).service' '/etc/systemd/system/$(SERVICE_NAME).service'; then echo 'Unit up to date.'; else echo 'Unit missing or stale; installing.'; sudo cp '$(DEPLOY_PATH)/$(SERVICE_NAME).service' /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable '$(SERVICE_NAME).service'; fi"
	@echo "== Start $(SERVICE_NAME).service =="
	ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "sudo systemctl start '$(SERVICE_NAME).service'"
	@# Give Restart=on-failure a moment to surface a crash loop, then fail the
	@# job with the unit status + recent journal instead of a bare exit 3.
	ssh $(SSH_OPTS) "$(DEPLOY_USER)@$(DEPLOY_HOST)" "sleep 5; if systemctl is-active --quiet '$(SERVICE_NAME).service'; then sudo systemctl --no-pager -l status '$(SERVICE_NAME).service' || true; else sudo systemctl --no-pager -l status '$(SERVICE_NAME).service'; sudo journalctl -u '$(SERVICE_NAME).service' -n 50 --no-pager; exit 1; fi"
