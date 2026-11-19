package models

// File overview:
// - Responsibility: res.users password and API-key handling: alias binding, hashing rules and active-password checks.
// - Key symbols: `resUsersNewPasswordArg`, `bindResUsersPasswordAlias`, `renderResUsersPasswordAlias`, `actionSetPasswordResUsers`.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils"
	"golang.org/x/crypto/bcrypt"
)

// resUsersNewPasswordArg reads the new password from args[0] or kwargs.new_password (trimmed).
func resUsersNewPasswordArg(args []any, kwargs map[string]any) string {
	return firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["new_password"])))
}

// bindResUsersPasswordAlias maps the bedc body {"password"} to new_password and takes the user id
// from the path only (a body "id" can never retarget another user).
func bindResUsersPasswordAlias(call *orm.AliasCall) error {
	body, err := call.JSONObject()
	if err != nil {
		return err
	}
	id := strings.TrimSpace(call.Params["id"])
	if id == "" {
		return &orm.AliasError{Status: 400, Code: "invalid_id", Message: "id is required"}
	}
	password := strings.TrimSpace(stringValue(body["password"]))
	if password == "" {
		return &orm.AliasError{Status: 400, Code: "invalid_request", Message: "password is required"}
	}
	call.IDs = []string{id}
	call.Kwargs = map[string]any{"new_password": password}
	return nil
}

// renderResUsersPasswordAlias answers with a bare OK envelope so the written user row (which
// carries the password hash) never leaves the server.
func renderResUsersPasswordAlias(*orm.AliasCall, any) (int, any, error) {
	return 200, map[string]any{"jsonrpc": "", "result": map[string]any{"status": "OK"}, "id": nil}, nil
}

// actionSetPasswordResUsers handles the object-button password update flow for a saved base.users row.
// It reuses the normal write path so hashing, validation, and access-control behavior stay centralized.
func actionSetPasswordResUsers(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	newPassword := resUsersNewPasswordArg(args, kwargs)
	if len(ids) == 0 {
		log.Errorf("base.users.action_set_password requires target ids")
		return nil, orm.InvalidParamsf("base.users.action_set_password requires target ids")
	}
	if newPassword == "" {
		log.Errorf("base.users.action_set_password requires new_password")
		return nil, orm.InvalidParamsf("base.users.action_set_password requires new_password")
	}
	return writeResUsers(ctx, model, ids, []any{orm.RecordMap{"new_password": newPassword}}, nil)
}

// actionGenerateAPIKeyResUsers (re)generates the target user's X-API-Key. Only the SHA-256 digest is
// persisted (written directly, so writeResUsers does not hash it a second time); the plaintext is
// returned in this response only and no later read can recover it. Regenerating invalidates the
// previous key immediately.
func actionGenerateAPIKeyResUsers(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("base.users.action_generate_api_key requires target ids")
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("base.users.action_generate_api_key: %w", err)
	}
	plaintext := "bhk_" + hex.EncodeToString(buf)
	ref := orm.RuntimeModelRef(ResUserModelName, model)
	// Guarded write: this public method is not a write override, so the record-rule check on the
	// target id has to come from WriteRecords.
	if _, err := orm.WriteRecords(ctx, ref, ids[:1], orm.RecordMap{"api_key": orm.HashAPIKey(plaintext)}); err != nil {
		return nil, fmt.Errorf("base.users.action_generate_api_key: %w", err)
	}
	login := ""
	if rows, err := orm.RuntimeRepositoryRead(ctx, ref, ids[:1], []string{"login"}); err == nil && len(rows) > 0 {
		login = strings.TrimSpace(stringValue(rows[0]["login"]))
	}
	return orm.RecordMap{"user_login": login, "user_api_key": plaintext}, nil
}

const bcryptMaxPasswordBytes = 72

// resUsersPasswordMask is what maskResUsersSecrets returns on read; a form that
// echoes it back must not overwrite the stored hash with a hash of the mask.
const resUsersPasswordMask = "••••••••"

func normalizeResUsersPassword(values map[string]any) error {
	rawPassword := strings.TrimSpace(stringValue(values["new_password"]))
	if rawPassword == "" {
		// A `password` value is always plaintext: a "$2..." prefix is no proof of a
		// valid hash, so skipping hashing would let a client store plaintext or inject a hash.
		rawPassword = strings.TrimSpace(stringValue(values["password"]))
	}
	if rawPassword == "" {
		return nil
	}
	if rawPassword == resUsersPasswordMask && strings.TrimSpace(stringValue(values["new_password"])) == "" {
		delete(values, "password")
		return nil
	}
	// bcrypt silently truncates past 72 bytes; reject instead of storing a weaker credential.
	if len(rawPassword) > bcryptMaxPasswordBytes {
		return orm.InvalidParamsf("password must be at most %d bytes", bcryptMaxPasswordBytes)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(rawPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	values["password"] = string(hash)
	delete(values, "new_password")
	return nil
}

// normalizeResUsersAPIKey turns a submitted plaintext api_key into its SHA-256 hex digest before
// persistence. Empty/false/null clears the key (NULL = X-API-Key disabled); the read mask value is
// dropped so re-saving a form that echoed it cannot overwrite the real digest.
func normalizeResUsersAPIKey(values map[string]any) {
	raw, ok := values["api_key"]
	if !ok {
		return
	}
	key := ""
	if str, isStr := raw.(string); isStr {
		key = strings.TrimSpace(str)
	}
	switch key {
	case "••••••••":
		delete(values, "api_key")
	case "":
		values["api_key"] = nil
	default:
		values["api_key"] = orm.HashAPIKey(key)
	}
}

// resUsersGeneratedPasswordLength gives ~143 bits from a 62-symbol alphabet; well under bcrypt's 72 bytes.
const resUsersGeneratedPasswordLength = 24

// applyResUsersGeneratedPassword gives an active user created without a password a random one
// and returns it (empty when the payload carries a password or the user is inactive). A fixed
// default would be a guessable credential on every such account; a random one that is never
// shown leaves the account unusable until an admin sets a password (action_set_password). Only
// the bootstrap superadmin's is revealed, see revealResUsersBootstrapPassword.
func applyResUsersGeneratedPassword(values map[string]any) (string, error) {
	if active, ok := values["active"].(bool); ok && !active {
		return "", nil
	}
	if strings.TrimSpace(stringValue(values["new_password"])) != "" || strings.TrimSpace(stringValue(values["password"])) != "" {
		return "", nil
	}
	password, err := utils.RandomAlphaNumericString(resUsersGeneratedPasswordLength)
	if err != nil {
		return "", fmt.Errorf("base.users: generate password: %w", err)
	}
	values["password"] = password
	return password, nil
}

// revealResUsersBootstrapPassword logs, once, the generated password of a superadmin created by
// the system seed import (sudo, uid 0, install_mode): the first-init admin, whose seed carries no
// password. Every other generated password stays secret. A later reimport takes the write path
// and master-data strips already-set passwords, so this fires only on creation.
func revealResUsersBootstrapPassword(ctx context.Context, values map[string]any, password string) {
	env := orm.EnvironmentFromContext(ctx)
	if !env.IsSudo() || env.UserID() != 0 {
		return
	}
	if installMode, _ := env.ContextValue("install_mode"); !boolValue(installMode) {
		return
	}
	groupID, ok := resGroupIDValue(values["group_id"])
	if !ok || groupID <= 0 {
		return
	}
	superadmin, err := resGroupsGrantSuperadmin(ctx, []int64{groupID})
	if err != nil {
		log.Errorf("base.users: cannot resolve the initial superadmin group, password not shown (use reset-admin-password): %v", err)
		return
	}
	if !superadmin {
		return
	}
	log.Warnf("base.users: generated the initial password of superadmin %q; copy it now, it is shown only once and cannot be retrieved: %s",
		strings.TrimSpace(stringValue(values["login"])), password)
}

// requireResUsersActivePassword rejects create payloads that would persist an
// active user without any stored credential (SEC-01). Inactive users may stay
// password-less (placeholder or pre-provisioned accounts) until activation,
// because the auth path already refuses empty stored passwords.
func requireResUsersActivePassword(values map[string]any) error {
	if active, ok := values["active"].(bool); ok && !active {
		return nil
	}
	if strings.TrimSpace(stringValue(values["password"])) == "" {
		log.Errorf("base.users.create requires a non-empty password for active users")
		return orm.InvalidParamsf("base.users.create requires a non-empty password for active users")
	}
	return nil
}
