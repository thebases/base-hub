package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys module category inside the kernel model catalog.
// - Key symbols: `init`, `createSysModuleCategory`, `SysModuleCategoryRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// SysModuleCategoryRecord stores the module-category tree used to organize
// modules and privilege groups inside admin tooling.
type SysModuleCategoryRecord struct {
	bun.BaseModel `bun:"table:sys_module_category,alias:sys_module_category"`
	orm.MetadataBaseModel
	Name      string  `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	ParentID  *int64  `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:sys.module.category,ondelete:SET NULL,index,label:Parent ID,help:Links this record to the related sys.module.category record for parent id."`
	ChildIDs  []int64 `bun:"child_ids" json:"child_ids" meta:"kind:one2many,model:sys.module.category,inverse:parent_id,stored:false,label:Child IDs,help:Lists the related sys.module.category records linked back through parent_id."`
	ModuleIDs []int64 `bun:"module_ids" json:"module_ids" meta:"kind:one2many,model:sys.module.module,inverse:category_id,stored:false,label:Module IDs,help:Lists the related sys.module.module records linked back through category_id."`
}

const SysModuleCategoryModelName = "sys.module.category"

var SysModuleCategoryModel = orm.Define[SysModuleCategoryRecord](&Models, 12, orm.ModelSpec{
	Name:        SysModuleCategoryModelName,
	Label:       "Module Categories",
	Description: "Module category tree used to organize backend modules and privilege groupings",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysModuleCategory},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysModuleCategory},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysModuleCategory},
	},
})

// createSysModuleCategory creates the sys module category handled by this package.
func createSysModuleCategory(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	return createLoggedMetadataRecords(ctx, SysModuleCategoryModelName, args, kwargs)
}

func writeSysModuleCategory(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	return writeLoggedMetadataRecords(ctx, SysModuleCategoryModelName, ids, args, kwargs)
}

func unlinkSysModuleCategory(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	return unlinkLoggedMetadataRecords(ctx, SysModuleCategoryModelName, ids)
}
