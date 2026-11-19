package models

// File overview:
// - Responsibility: keeps the same-named fields of tbl.company and its linked tbl.partner (company.partner_id) identical in both directions.
// - Key symbols: `companyPartnerSharedFields`, `syncCompanyValuesToPartners`, `syncPartnerValuesToCompanies`, `createCompanyPartner`.
// - Pattern: A context flag marks a write that originated from the sync so the mirrored write does not bounce back and loop.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// companyPartnerSharedFields lists the columns that exist under the same name and meaning on
// tbl.company and tbl.partner. parent_id is deliberately absent: it points at a company on one
// side and at a partner on the other, so copying the id would be wrong.
var companyPartnerSharedFields = []string{
	"name", "email", "phone", "website",
	"street", "street2", "zip", "city",
	"state_id", "country_id", "vat", "company_registry",
}

type companyPartnerSyncKey struct{}

// inCompanyPartnerSync reports whether the current write was issued by the sync itself.
func inCompanyPartnerSync(ctx context.Context) bool {
	flagged, _ := ctx.Value(companyPartnerSyncKey{}).(bool)
	return flagged
}

// pickCompanyPartnerSharedValues returns only the shared keys the caller actually sent, so an
// unrelated write never touches the counterpart row.
func pickCompanyPartnerSharedValues(values orm.RecordMap) orm.RecordMap {
	picked := orm.RecordMap{}
	for _, field := range companyPartnerSharedFields {
		if value, exists := values[field]; exists {
			picked[field] = value
		}
	}
	return picked
}

// createCompanyPartner creates and links the partner for a freshly created company, or pushes the
// company values onto the partner the caller already linked.
func createCompanyPartner(ctx context.Context, company orm.RecordMap, values orm.RecordMap) error {
	if inCompanyPartnerSync(ctx) {
		return nil
	}
	companyID, _ := int64FromAny(company["id"])
	if companyID <= 0 {
		return nil
	}
	shared := pickCompanyPartnerSharedValues(values)
	// Linked at create time: the company side stays the source of truth for its first values.
	if partnerID, _ := int64FromAny(values["partner_id"]); partnerID > 0 {
		return syncCompanyValuesToPartners(ctx, []string{fmt.Sprintf("%d", companyID)}, shared)
	}
	if strings.TrimSpace(stringValue(shared["name"])) == "" {
		return nil
	}
	partnerValues := cloneRecordMap(shared)
	partnerValues["is_company"] = true
	partnerValues["company_type"] = "company"
	partnerValues["complete_name"] = strings.TrimSpace(stringValue(shared["name"]))
	// The company's own partner is scoped to that company, like every seeded partner.
	partnerValues["company_id"] = companyID
	applyResPartnerDefaults(partnerValues)
	partner, err := orm.RuntimeRepositoryCreate(withCompanyPartnerSync(ctx), orm.RuntimeModelRef(ResPartnerModelName, nil), partnerValues)
	if err != nil {
		return err
	}
	partnerID, _ := int64FromAny(partner["id"])
	if partnerID <= 0 {
		return nil
	}
	_, err = orm.RuntimeRepositoryWrite(withCompanyPartnerSync(ctx), orm.RuntimeModelRef(ResCompanyModelName, nil), []string{fmt.Sprintf("%d", companyID)}, orm.RecordMap{"partner_id": partnerID})
	return err
}

func withCompanyPartnerSync(ctx context.Context) context.Context {
	return context.WithValue(ctx, companyPartnerSyncKey{}, true)
}

// syncCompanyValuesToPartners copies the shared values written on companies to their linked partners.
func syncCompanyValuesToPartners(ctx context.Context, companyIDs []string, shared orm.RecordMap) error {
	if inCompanyPartnerSync(ctx) || len(shared) == 0 || len(companyIDs) == 0 {
		return nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResCompanyModelName, nil), companyIDs, []string{"id", "partner_id"})
	if err != nil {
		return err
	}
	partnerIDs := make([]string, 0, len(rows))
	seen := map[int64]struct{}{}
	for _, row := range rows {
		partnerID, _ := many2oneID(row["partner_id"])
		if partnerID <= 0 {
			continue
		}
		if _, dup := seen[partnerID]; dup {
			continue
		}
		seen[partnerID] = struct{}{}
		partnerIDs = append(partnerIDs, fmt.Sprintf("%d", partnerID))
	}
	if len(partnerIDs) == 0 {
		return nil
	}
	partnerValues := cloneRecordMap(shared)
	// Partner stores a denormalized complete_name; a company's partner has no parent, so it equals name.
	if name, renamed := shared["name"]; renamed {
		partnerValues["complete_name"] = strings.TrimSpace(stringValue(name))
	}
	_, err = orm.RuntimeRepositoryWrite(withCompanyPartnerSync(ctx), orm.RuntimeModelRef(ResPartnerModelName, nil), partnerIDs, partnerValues)
	return err
}

// syncPartnerValuesToCompanies copies the shared values written on partners to every company
// linked through partner_id.
func syncPartnerValuesToCompanies(ctx context.Context, partnerIDs []string, shared orm.RecordMap) error {
	if inCompanyPartnerSync(ctx) || len(shared) == 0 || len(partnerIDs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(partnerIDs))
	for _, raw := range partnerIDs {
		if id, ok := int64FromAny(raw); ok && id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ResCompanyModelName, nil), orm.RecordMap{
		"__domain": orm.Where("partner_id", "in", ids),
	}, []string{"id"})
	if err != nil {
		return err
	}
	companyIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if id, _ := int64FromAny(row["id"]); id > 0 {
			companyIDs = append(companyIDs, fmt.Sprintf("%d", id))
		}
	}
	if len(companyIDs) == 0 {
		return nil
	}
	_, err = orm.RuntimeRepositoryWrite(withCompanyPartnerSync(ctx), orm.RuntimeModelRef(ResCompanyModelName, nil), companyIDs, shared)
	return err
}

// syncPartnerNameToUsers copies a renamed partner's name onto every user linked through
// partner_id. It runs as sudo because the partner editor may not be allowed to write users, and
// it uses the repository write (not the user runtime method), so the user->partner push in
// writeResUsers is never re-triggered. Users already carrying the name are skipped.
func syncPartnerNameToUsers(ctx context.Context, partnerIDs []string, values orm.RecordMap) error {
	rawName, renamed := values["name"]
	name := strings.TrimSpace(stringValue(rawName))
	// A blank name is ignored: user.name is required, so never blank it from the partner side.
	if !renamed || name == "" || len(partnerIDs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(partnerIDs))
	for _, raw := range partnerIDs {
		if id, ok := int64FromAny(raw); ok && id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sudoCtx := ctx
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		sudoCtx = orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))
	}
	users, err := orm.RuntimeRepositorySearchRead(sudoCtx, orm.RuntimeModelRef(ResUserModelName, nil), orm.RecordMap{
		"__domain": orm.Where("partner_id", "in", ids),
	}, []string{"id", "name"})
	if err != nil {
		return err
	}
	userIDs := make([]string, 0, len(users))
	for _, user := range users {
		if id, _ := int64FromAny(user["id"]); id > 0 && strings.TrimSpace(stringValue(user["name"])) != name {
			userIDs = append(userIDs, fmt.Sprintf("%d", id))
		}
	}
	if len(userIDs) == 0 {
		return nil
	}
	_, err = orm.RuntimeRepositoryWrite(sudoCtx, orm.RuntimeModelRef(ResUserModelName, nil), userIDs, orm.RecordMap{"name": name})
	return err
}

const (
	rootCompanyID          int64 = 1
	technicalFeaturesGroup       = "base.group_no_one"
)

// grantRootSuperadminsCompany appends a new company to company_ids of every Technical Features
// user whose active company is the root company (id 1), so they can switch into it. It runs as
// sudo because the creator may not be allowed to read or write those users. Already-present ids
// are skipped, which keeps a repeated call (reinit, retry) idempotent.
func grantRootSuperadminsCompany(ctx context.Context, newCompanyID int64) error {
	if newCompanyID <= 0 || newCompanyID == rootCompanyID {
		return nil
	}
	sudoCtx := ctx
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		sudoCtx = orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))
	}
	groups, err := orm.RuntimeRepositorySearchRead(sudoCtx, orm.RuntimeModelRef(ResGroupModelName, nil), orm.RecordMap{"bid": technicalFeaturesGroup}, []string{"id"})
	if err != nil || len(groups) == 0 {
		return err
	}
	groupID, _ := int64FromAny(groups[0]["id"])
	users, err := orm.RuntimeRepositorySearchRead(sudoCtx, orm.RuntimeModelRef(ResUserModelName, nil), orm.RecordMap{"group_id": groupID, "company_id": rootCompanyID}, []string{"id", "company_ids"})
	if err != nil {
		return err
	}
	for _, user := range users {
		userID, _ := int64FromAny(user["id"])
		companyIDs := int64SliceFromAny(user["company_ids"])
		if userID <= 0 || slices.Contains(companyIDs, newCompanyID) {
			continue
		}
		if _, err := orm.RuntimeRepositoryWrite(sudoCtx, orm.RuntimeModelRef(ResUserModelName, nil), []string{fmt.Sprintf("%d", userID)}, orm.RecordMap{"company_ids": append(companyIDs, newCompanyID)}); err != nil {
			return err
		}
	}
	return nil
}
