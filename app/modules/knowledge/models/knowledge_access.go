package models

// File overview:
// - Responsibility: the single write-permission rule for knowledge documents, shared by the
//   /md/api/files* HTTP routes and the knowledge.doc.create/update/rename/delete MCP tools.
// - Key symbols: `EnsureKnowledgeWriteAllowed`, `KnowledgeWriterGroupBID`.

import (
	"context"
	"slices"

	"git.thebase.vn/base_hub/internal/orm"
)

// KnowledgeWriterGroupBID may create, edit, rename and delete "public" namespace documents.
// Those documents are served to every caller of the tenant (anonymous /md/* included), so a
// plain login is not enough to change them.
const KnowledgeWriterGroupBID = "base.group_system"

// Test seam over the kernel helper that needs the auth database.
var knowledgeCallerGroupBIDs = orm.CallerGroupBIDs

// EnsureKnowledgeWriteAllowed lets sudo and base.group_system (primary or implied) callers write
// documents. ctx must carry the caller's identity environment (orm.EnvironmentForIdentity);
// a missing environment or an anonymous one is refused. In-process Go callers that call the
// document service directly are trusted and do not go through this check.
func EnsureKnowledgeWriteAllowed(ctx context.Context) error {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return orm.Forbiddenf("permission denied: writing knowledge documents needs an authenticated caller")
	}
	if env.IsSudo() {
		return nil
	}
	groups, err := knowledgeCallerGroupBIDs(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(groups, KnowledgeWriterGroupBID) {
		return orm.Forbiddenf("permission denied: only %s can write knowledge documents", KnowledgeWriterGroupBID)
	}
	return nil
}
