package models

// File overview:
// - Responsibility: keeps the linked partner row in sync with res.users name and delegated values.
// - Key symbols: `ensureResUsersPartner`, `resUsersWriteNameSyncPlan`, `syncResUsersCreateNameWithPartner`, `planResUsersWriteNameSync`.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// ensureResUsersPartner links a freshly created user to a partner. When the
// caller supplied no partner_id, a new tbl.partner is created (named after the
// user, falling back to the login) and mapped onto userValues["partner_id"].
func ensureResUsersPartner(ctx context.Context, userValues, partnerValues map[string]any) error {
	if partnerID, ok := int64FromAny(userValues["partner_id"]); ok && partnerID > 0 {
		return nil
	}
	name := strings.TrimSpace(stringValue(userValues["name"]))
	if name == "" {
		name = strings.TrimSpace(stringValue(userValues["login"]))
	}
	if name == "" {
		return nil
	}
	values := cloneRecordMap(partnerValues)
	values["name"] = name
	if _, exists := values["email"]; !exists && schemaHasField(ResPartnerDefinition, "email") {
		if email := strings.TrimSpace(stringValue(userValues["email"])); email != "" {
			values["email"] = email
		}
	}
	partner, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(ResPartnerModelName, nil), values)
	if err != nil {
		return err
	}
	partnerID, ok := int64FromAny(partner["id"])
	if !ok || partnerID <= 0 {
		return fmt.Errorf("base.users: failed to create partner for user %q", name)
	}
	userValues["partner_id"] = partnerID
	if strings.TrimSpace(stringValue(userValues["name"])) == "" {
		userValues["name"] = name
	}
	return nil
}

type resUsersWriteNameSyncPlan struct {
	ExplicitName string
	FallbackName bool
}

// syncResUsersCreateNameWithPartner keeps user and partner names aligned on
// create without forcing callers to send the same display name twice.
func syncResUsersCreateNameWithPartner(ctx context.Context, userValues, partnerValues map[string]any) error {
	trimmedName, hasName := normalizedResUsersNameValue(userValues)
	if hasName && trimmedName != "" {
		userValues["name"] = trimmedName
		if partnerID, ok := int64FromAny(userValues["partner_id"]); ok && partnerID > 0 {
			partnerValues["name"] = trimmedName
		}
		return nil
	}
	partnerName, err := loadResUsersPartnerName(ctx, userValues["partner_id"])
	if err != nil {
		return err
	}
	if partnerName != "" {
		userValues["name"] = partnerName
	}
	return nil
}

// planResUsersWriteNameSync captures whether write should push an explicit user
// name to the partner or pull the current partner name back onto the user.
func planResUsersWriteNameSync(userValues, partnerValues map[string]any) resUsersWriteNameSyncPlan {
	trimmedName, hasName := normalizedResUsersNameValue(userValues)
	if !hasName {
		return resUsersWriteNameSyncPlan{}
	}
	if trimmedName != "" {
		userValues["name"] = trimmedName
		partnerValues["name"] = trimmedName
		return resUsersWriteNameSyncPlan{ExplicitName: trimmedName}
	}
	// Leave the current user name untouched during the bulk write, then fill it
	// from each linked partner record individually after the write succeeds.
	delete(userValues, "name")
	return resUsersWriteNameSyncPlan{FallbackName: true}
}

// applyResUsersWriteNameFallback fills blank user names from partner.name after
// the main write succeeds so each record can resolve its own linked partner.
func applyResUsersWriteNameFallback(ctx context.Context, ids []string, userValues map[string]any, plan resUsersWriteNameSyncPlan) error {
	if !plan.FallbackName || len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		partnerID, err := resolveResUsersWritePartnerID(ctx, id, userValues)
		if err != nil {
			return err
		}
		if partnerID <= 0 {
			continue
		}
		partnerName, err := loadResUsersPartnerName(ctx, partnerID)
		if err != nil {
			return err
		}
		if partnerName == "" {
			continue
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(ResUserModelName, nil), []string{id}, orm.RecordMap{"name": partnerName}); err != nil {
			return err
		}
	}
	return nil
}

func writeResUsersPartnerValuesForIDs(ctx context.Context, ids []string, partnerValues map[string]any) error {
	for _, id := range ids {
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, nil), []string{id}, []string{"partner_id"})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		if err := writeResUsersPartnerValues(ctx, rows[0]["partner_id"], partnerValues); err != nil {
			return err
		}
	}
	return nil
}

func writeResUsersPartnerValues(ctx context.Context, partnerRaw any, partnerValues map[string]any) error {
	if len(partnerValues) == 0 {
		return nil
	}
	// partnerRaw is read-shaped (a RuntimeRead row or a RuntimeCreate record), so the
	// many2one may arrive as an [id, name] tuple.
	partnerID, ok := many2oneID(partnerRaw)
	if !ok {
		return nil
	}
	_, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(ResPartnerModelName, nil), []string{fmt.Sprintf("%d", partnerID)}, cloneRecordMap(partnerValues))
	return err
}

func normalizedResUsersNameValue(values map[string]any) (string, bool) {
	raw, exists := values["name"]
	if !exists {
		return "", false
	}
	return strings.TrimSpace(stringValue(raw)), true
}

func resolveResUsersWritePartnerID(ctx context.Context, id string, userValues map[string]any) (int64, error) {
	if partnerID, exists := userValues["partner_id"]; exists {
		if resolved, ok := int64FromAny(partnerID); ok && resolved > 0 {
			return resolved, nil
		}
		return 0, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, nil), []string{id}, []string{"partner_id"})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	partnerID, ok := many2oneID(rows[0]["partner_id"])
	if !ok || partnerID <= 0 {
		return 0, nil
	}
	return partnerID, nil
}

func loadResUsersPartnerName(ctx context.Context, partnerRaw any) (string, error) {
	partnerID, ok := int64FromAny(partnerRaw)
	if !ok || partnerID <= 0 {
		return "", nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResPartnerModelName, nil), []string{fmt.Sprintf("%d", partnerID)}, []string{"name"})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return strings.TrimSpace(stringValue(rows[0]["name"])), nil
}

func splitResUsersDelegatedPartnerValues(userDef, partnerDef *orm.SchemaDefinition, values map[string]any) (orm.RecordMap, orm.RecordMap) {
	userValues := make(orm.RecordMap)
	partnerValues := make(orm.RecordMap)
	for key, value := range values {
		// base.users no longer exposes the old lang alias, so drop it instead of
		// delegating it into tbl.partner.lang by accident during payload splits.
		if key == "lang" {
			continue
		}
		// Probe schema membership silently here because delegated payloads may
		// include removed or partner-owned fields during gradual API cleanup.
		if schemaHasField(userDef, key) {
			userValues[key] = value
			continue
		}
		if schemaHasField(partnerDef, key) {
			partnerValues[key] = value
			continue
		}
		userValues[key] = value
	}
	return userValues, partnerValues
}
