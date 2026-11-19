package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_stage` model — a pipeline
//   stage (a kanban-column), shareable across teams.
//   Ownership: helpdesk module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskStageRecord`, `HelpdeskStageModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskStageRecord stores one pipeline stage. Stages are shareable across
// teams (TeamIDs many2many, not a single owning team).
type HelpdeskStageRecord struct {
	orm.BaseModel
	Name     string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the stage's display name."`
	Sequence int    `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the stage's ordering position within a team's pipeline."`
	Fold     bool   `bun:"fold,notnull,default:false" json:"fold" meta:"required,label:Fold,help:Controls whether this stage's kanban column starts folded."`
	IsClosed bool   `bun:"is_closed,notnull,default:false" json:"is_closed" meta:"required,label:Is Closed,help:Controls whether a ticket in this stage is considered closed for reporting/status derivation."`
	// Unattended (§9.1): a ticket sitting in this stage is considered
	// "unattended" (no agent action yet) — used to compute
	// tbl.helpdesk_ticket.unattended.
	Unattended bool    `bun:"unattended,notnull,default:false" json:"unattended" meta:"required,label:Unattended,help:Controls whether a ticket parked in this stage counts as unattended."`
	TeamIDs    []int64 `bun:"team_ids,type:jsonb,nullzero" json:"team_ids" meta:"kind:many2many,model:tbl.helpdesk_team,relation_table:tbl_helpdesk_stage_team_rel,relation_column:stage_id,relation_key:team_id,label:Teams,help:Lists the tbl.helpdesk_team records that expose this stage in their pipeline."`
}

const HelpdeskStageModelName = "tbl.helpdesk_stage"

var HelpdeskStageModelSpec = orm.ModelSpec{
	Name:         HelpdeskStageModelName,
	Label:        "Helpdesk Stages",
	Description:  "Pipeline stages shareable across teams, owned by the helpdesk module",
	Definition:   orm.ModelDefinition(HelpdeskStageModelName, (*HelpdeskStageRecord)(nil)),
	DefaultOrder: "sequence",
}

var HelpdeskStageModel = orm.NewHandlerWithModel[HelpdeskStageRecord](HelpdeskStageModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskStageModelName, 903, HelpdeskStageModel.InitDefault, HelpdeskStageModel.Shutdown)
}
