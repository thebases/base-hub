-- +migrate Up

CREATE TABLE IF NOT EXISTS "public"."sys_model" (
    "name" varchar(255) PRIMARY KEY,
    "label" varchar(255) NOT NULL,
    "description" text NOT NULL DEFAULT '',
    "storage_backend" varchar(32) NOT NULL,
    "storage_name" varchar(255) NOT NULL,
    "go_type" varchar(255) NOT NULL,
    "registered_at" timestamptz NOT NULL,
    "synced_at" timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS "idx_sys_model_storage_backend"
    ON "public"."sys_model" ("storage_backend");

CREATE INDEX IF NOT EXISTS "idx_sys_model_storage_name"
    ON "public"."sys_model" ("storage_name");

CREATE TABLE IF NOT EXISTS "public"."sys_model_field" (
    "model_name" varchar(255) NOT NULL,
    "field_name" varchar(255) NOT NULL,
    "go_name" varchar(255) NOT NULL,
    "data_type" varchar(255) NOT NULL,
    "bson_name" varchar(255) NOT NULL DEFAULT '',
    "json_name" varchar(255) NOT NULL DEFAULT '',
    "bun_column" varchar(255) NOT NULL DEFAULT '',
    "is_primary_key" boolean NOT NULL DEFAULT false,
    "is_required" boolean NOT NULL DEFAULT false,
    "field_index" integer NOT NULL DEFAULT 0,
    "updated_at" timestamptz NOT NULL,
    PRIMARY KEY ("model_name", "field_name"),
    CONSTRAINT "fk_sys_model_field_model_name"
        FOREIGN KEY ("model_name")
        REFERENCES "public"."sys_model" ("name")
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS "idx_sys_model_field_model_name"
    ON "public"."sys_model_field" ("model_name");

CREATE INDEX IF NOT EXISTS "idx_sys_model_field_bun_column"
    ON "public"."sys_model_field" ("bun_column");
