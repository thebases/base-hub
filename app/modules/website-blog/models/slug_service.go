package models

// File overview:
// - Responsibility: collision-free slug assignment shared by blog, post, tag and category writes.
// - Key symbols: `uniqueBlogSlug`.

import (
	"context"
	"fmt"
	"strconv"

	"git.thebase.vn/base_hub/internal/orm"
)

// uniqueBlogSlug returns base, or base-2/base-3/... when another row already owns it. `scope`
// narrows the uniqueness domain (a post slug is unique per blog); excludeID is the row being
// rewritten so it does not collide with itself. The lookup runs without the caller's record rules:
// a row the caller cannot see still owns its slug (and, for posts, the unique index rejects it).
func uniqueBlogSlug(ctx context.Context, modelName, base string, scope orm.RecordMap, excludeID string) (string, error) {
	ctx = blogSudoContext(ctx)
	candidate := base
	for suffix := 2; suffix < 1000; suffix++ {
		filter := cloneRecordMap(scope)
		filter["slug"] = candidate
		filter["__limit"] = 2
		rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(modelName, nil), filter, []string{"id"})
		if err != nil {
			return "", err
		}
		taken := false
		for _, row := range rows {
			if id, ok := int64FromAny(row["id"]); ok && strconv.FormatInt(id, 10) != excludeID {
				taken = true
				break
			}
		}
		if !taken {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	return "", orm.Conflictf("%s: could not allocate a unique slug for %q", modelName, base)
}

// blogSudoContext lifts record rules and company scope for a lookup, keeping the request DB/tx.
func blogSudoContext(ctx context.Context) context.Context {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return ctx
	}
	return orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))
}
