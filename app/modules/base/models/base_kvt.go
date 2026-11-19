package models

// File overview:
// - Responsibility: defines `base.kvt`, a small key/value/type store with a public read route `GET /kvt/:key`.
// - Key symbols: `BaseKvtRecord`, `BaseKvtModelSpec`, `getPublicKvt`.
// - Pattern: access tiers are enforced by ACL + record rules (see security/); the anonymous route only ever serves type 0.

import (
	"context"
	"encoding/json"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// Access tiers stored in BaseKvtRecord.Type.
const (
	BaseKvtTypePublic    = 0 // any user, including anonymous callers
	BaseKvtTypeInternal  = 1 // internal users (base.group_user) and above
	BaseKvtTypeSystem    = 2 // system users (base.group_system) and above
	BaseKvtTypeTechnical = 3 // technical users (base.group_no_one) only
)

type BaseKvtRecord struct {
	orm.BaseModel
	Name  string `bun:"name,notnull" json:"name" meta:"index,unique,required,label:Name,help:Stores the key used to look up the value."`
	Value any    `bun:"value,type:jsonb,nullzero" json:"value" meta:"kind:json,label:Value,help:Stores the JSON value returned for this key."`
	Type  int    `bun:"type,notnull,default:0" json:"type" meta:"index,label:Type,help:Access tier: 0 public, 1 internal user, 2 system user, 3 technical user."`
}

const BaseKvtModelName = "base.kvt"

var BaseKvtDefinition = orm.ModelDefinition(BaseKvtModelName, (*BaseKvtRecord)(nil))

var BaseKvtModelSpec = orm.ModelSpec{
	Name:        BaseKvtModelName,
	Label:       "Key Value",
	Description: "Key/value store with per-record access tier",
	Definition:  BaseKvtDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{
			Name:       "get_public",
			Public:     true,
			ModelOnly:  true,
			ReadOnly:   true,
			AliasRoute: "GET /kvt/:key",
			// Anonymous on purpose: the route serves only type=0 rows, filtered in code because no ACL runs here.
			AliasAnonymous: true,
			Call:           getPublicKvt,
			Description:    "Anonymous read of the JSON value of the type-0 base.kvt record named by the path key.",
		},
	},
}

var BaseKvtModel = orm.Define[BaseKvtRecord](&Models, 290, BaseKvtModelSpec)

func kvtJSONReply(status int, body string) orm.AliasResponse {
	return orm.AliasResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: body}
}

// getPublicKvt answers GET /kvt/:key with the raw JSON value of the public record.
func getPublicKvt(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	request, ok := orm.AliasRequestFromKwargs(kwargs)
	if !ok {
		return nil, nil
	}
	key := strings.TrimSpace(request.Params["key"])
	if key == "" {
		return kvtJSONReply(404, `{"error":"not_found"}`), nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(BaseKvtModelName, nil),
		orm.RecordMap{"name": key, "type": BaseKvtTypePublic, "__limit": 1}, []string{"value"})
	if err != nil {
		log.Errorf("base.kvt: read %q: %v", key, err)
		return kvtJSONReply(500, `{"error":"internal_error"}`), nil
	}
	if len(rows) == 0 {
		return kvtJSONReply(404, `{"error":"not_found"}`), nil
	}
	body, err := json.Marshal(rows[0]["value"])
	if err != nil {
		log.Errorf("base.kvt: marshal %q: %v", key, err)
		return kvtJSONReply(500, `{"error":"internal_error"}`), nil
	}
	return kvtJSONReply(200, string(body)), nil
}
