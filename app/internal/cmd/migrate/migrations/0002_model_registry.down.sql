-- +migrate Down

DROP TABLE IF EXISTS "public"."sys_model_field" CASCADE;
DROP TABLE IF EXISTS "public"."sys_model" CASCADE;
