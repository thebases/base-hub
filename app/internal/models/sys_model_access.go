package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys model access inside the kernel model catalog.
// - Key symbols: `init`, `createSysModelAccess`, `SysModelAccessRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

// SysModelAccessRecord stores model-level CRUD grants per RBAC role so the ORM
// can enforce access consistently across all RPC model calls.
type SysModelAccessRecord struct {
	// bun.BaseModel `bun:"table:sys_model_access,alias:sys_model_access"`
	orm.BaseModel
	Name       string `bun:"name" json:"name" meta:"label:Name,help:Stores the human readable name for this record."`
	ModelName  string `bun:"model_name" json:"model_name" meta:"index,label:Model Name,help:Stores the model name value for this record."`
	GroupID    *int64 `bun:"group_id" json:"group_id" meta:"kind:many2one,index,model:tbl.groups,label:Group ID,help:Links this record to the related tbl.groups record for group id."`
	PermRead   bool   `bun:"perm_read" json:"perm_read" meta:"label:Perm Read,help:Controls whether perm read is enabled for this record."`
	PermWrite  bool   `bun:"perm_write" json:"perm_write" meta:"label:Perm Write,help:Controls whether perm write is enabled for this record."`
	PermCreate bool   `bun:"perm_create" json:"perm_create" meta:"label:Perm Create,help:Controls whether perm create is enabled for this record."`
	PermUnlink bool   `bun:"perm_unlink" json:"perm_unlink" meta:"label:Perm Unlink,help:Controls whether perm unlink is enabled for this record."`
}

var SysModelAccessModelName = "sys.model.access"

// SysModelAccessModel should compose access-specific behavior through small
// helpers and runtime hooks instead of concentrating authorization logic in a
// single customize function.
var SysModelAccessModel = orm.Define[SysModelAccessRecord](&Models, 290, orm.ModelSpec{
	Name:        SysModelAccessModelName,
	Label:       "Model Access",
	Description: "Role-bound CRUD access rows applied to runtime model execution",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysModelAccess},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysModelAccess},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysModelAccess},
	},
})

// createSysModelAccess creates the sys model access handled by this package.
func createSysModelAccess(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	return createLoggedMetadataRecords(ctx, SysModelAccessModelName, args, kwargs)
}

func writeSysModelAccess(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	return writeLoggedMetadataRecords(ctx, SysModelAccessModelName, ids, args, kwargs)
}

func unlinkSysModelAccess(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	return unlinkLoggedMetadataRecords(ctx, SysModelAccessModelName, ids)
}
