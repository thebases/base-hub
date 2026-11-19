//go:build conformance

package conformance

// File overview:
// - Responsibility: CONF-01 — exercises the generic RPC surface (CRUD,
//   search_read, toggle_active, onchange) end-to-end per registered model
//   kind and asserts the SRS-F-004 serialization contract: many2one values
//   serialize as [id, name] tuples, x2many values as arrays of tuples, and
//   empty relations normalize to false while booleans keep literal false.
// - Pattern: table-driven over the model kinds so a new kind only adds a row.

import (
	"context"
	"net/http"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// init registers the CONF-01 onchange probe exactly once per test binary; the
// runtime hook registry is append-only, so re-registering per test would run
// the hook twice and break the assertion on its output.
func init() {
	orm.RegisterOnchange(confTicketModelName, []string{"name"}, func(
		_ context.Context, _ orm.RecordSet, values orm.RecordMap, _ []string, _ map[string]any,
	) (orm.RecordMap, error) {
		// Echo a derived note so the test can prove the hook both ran and saw
		// the caller's draft values.
		return orm.RecordMap{"note": "onchange:" + stringField(values, "name")}, nil
	})
}

func stringField(values orm.RecordMap, key string) string {
	if raw, ok := values[key]; ok {
		if text, ok := raw.(string); ok {
			return text
		}
	}
	return ""
}

// TestConf01CRUDPerModelKind runs the generic surface across one model per
// registered kind: plain, archive-aware, company-scoped, and metadata.
func TestConf01CRUDPerModelKind(t *testing.T) {
	suite := newConfSuite(t)

	kinds := []struct {
		name          string
		model         string
		archiveAware  bool
		createValues  map[string]any
		writeValues   map[string]any
		searchField   string
		searchValue   string
	}{
		{
			name:         "plain",
			model:        confPartnerModelName,
			archiveAware: false,
			createValues: map[string]any{"name": "conf01 plain row"},
			writeValues:  map[string]any{"name": "conf01 plain row v2"},
			searchField:  "name",
			searchValue:  "conf01 plain row v2",
		},
		{
			name:         "archive",
			model:        confTicketModelName,
			archiveAware: true,
			createValues: map[string]any{"name": "conf01 archive row", "note": "n1"},
			writeValues:  map[string]any{"note": "n2"},
			searchField:  "name",
			searchValue:  "conf01 archive row",
		},
		{
			name:         "company-scoped",
			model:        confScopedModelName,
			archiveAware: true,
			createValues: map[string]any{"name": "conf01 scoped row"},
			writeValues:  map[string]any{"name": "conf01 scoped row v2"},
			searchField:  "name",
			searchValue:  "conf01 scoped row v2",
		},
		{
			name:         "metadata",
			model:        confMetaModelName,
			archiveAware: false,
			createValues: map[string]any{"name": "conf01 metadata row"},
			writeValues:  map[string]any{"name": "conf01 metadata row v2"},
			searchField:  "name",
			searchValue:  "conf01 metadata row v2",
		},
	}

	for _, kind := range kinds {
		kind := kind
		t.Run(kind.name, func(t *testing.T) {
			// create
			created := asMap(t, suite.rpcResult(t, rpcPath(kind.model, "create"),
				map[string]any{"values": kind.createValues}, authAPIKey()))
			id := recordID(t, created)

			// read
			rows := asList(t, suite.rpcResult(t, rpcPath(kind.model, "read", id),
				map[string]any{"fields": []string{"id", "name"}}, authAPIKey()))
			if len(rows) != 1 {
				t.Fatalf("read returned %d rows, want 1", len(rows))
			}
			if got := asMap(t, rows[0])["name"]; got != kind.createValues["name"] {
				t.Fatalf("read name = %v, want %v", got, kind.createValues["name"])
			}

			// write
			suite.rpcResult(t, rpcPath(kind.model, "write", id),
				map[string]any{"values": kind.writeValues}, authAPIKey())

			// search_read finds the written state
			found := asList(t, suite.rpcResult(t, rpcPath(kind.model, "search_read"), map[string]any{
				"domain": []any{[]any{kind.searchField, "=", kind.searchValue}},
				"fields": []string{"id", kind.searchField},
			}, authAPIKey()))
			matched := false
			for _, row := range found {
				if recordID(t, asMap(t, row)) == id {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("search_read did not return written record %d: %v", id, found)
			}

			// toggle_active only applies to archive-aware kinds; plain and
			// metadata models have no persisted active flag by design.
			if kind.archiveAware {
				suite.rpcResult(t, rpcPath(kind.model, "toggle_active", id), map[string]any{}, authAPIKey())
				archived := asList(t, suite.rpcResult(t, rpcPath(kind.model, "read", id),
					map[string]any{"fields": []string{"id", "active"}}, authAPIKey()))
				if got := asMap(t, archived[0])["active"]; got != false {
					t.Fatalf("toggle_active: active = %v, want false", got)
				}
				// Default active_test search must now hide the archived row.
				hidden := asList(t, suite.rpcResult(t, rpcPath(kind.model, "search_read"), map[string]any{
					"domain": []any{[]any{"id", "=", id}},
					"fields": []string{"id"},
				}, authAPIKey()))
				if len(hidden) != 0 {
					t.Fatalf("archived record still visible in default search: %v", hidden)
				}
			}

			// unlink
			suite.rpcResult(t, rpcPath(kind.model, "unlink", id), map[string]any{}, authAPIKey())
			gone := asList(t, suite.rpcResult(t, rpcPath(kind.model, "search_read"), map[string]any{
				"domain": []any{[]any{"id", "=", id}},
				"fields": []string{"id"},
			}, authAPIKey()))
			if len(gone) != 0 {
				t.Fatalf("unlinked record still present: %v", gone)
			}
		})
	}
}

// TestConf01SerializationContract locks the SRS-F-004 wire format on reads.
func TestConf01SerializationContract(t *testing.T) {
	suite := newConfSuite(t)

	partner := asMap(t, suite.rpcResult(t, rpcPath(confPartnerModelName, "create"),
		map[string]any{"values": map[string]any{"name": "conf01 serialization partner"}}, authAPIKey()))
	partnerID := recordID(t, partner)

	linked := asMap(t, suite.rpcResult(t, rpcPath(confTicketModelName, "create"),
		map[string]any{"values": map[string]any{
			"name":       "conf01 linked ticket",
			"urgent":     false,
			"partner_id": partnerID,
		}}, authAPIKey()))
	linkedID := recordID(t, linked)

	orphan := asMap(t, suite.rpcResult(t, rpcPath(confTicketModelName, "create"),
		map[string]any{"values": map[string]any{"name": "conf01 orphan ticket"}}, authAPIKey()))
	orphanID := recordID(t, orphan)

	t.Cleanup(func() {
		// Fixture rows are removed through SQL because the RPC app is bound to
		// the test's lifecycle and cleanup must not depend on it.
		_, _ = suite.db.ExecContext(context.Background(), "DELETE FROM conf_ticket WHERE id IN (?, ?)", linkedID, orphanID)
		_, _ = suite.db.ExecContext(context.Background(), "DELETE FROM conf_partner WHERE id = ?", partnerID)
	})

	rows := asList(t, suite.rpcResult(t, rpcPath(confTicketModelName, "search_read"), map[string]any{
		"domain": []any{[]any{"id", "in", []any{linkedID, orphanID}}},
		"fields": []string{"id", "name", "urgent", "partner_id"},
	}, authAPIKey()))
	if len(rows) != 2 {
		t.Fatalf("search_read returned %d rows, want 2: %v", len(rows), rows)
	}
	for _, raw := range rows {
		row := asMap(t, raw)
		switch recordID(t, row) {
		case linkedID:
			// many2one with a value serializes as the [id, name] tuple.
			requireTuple(t, row["partner_id"], partnerID, "linked ticket partner_id")
			// boolean false stays literal false (never null).
			if got := row["urgent"]; got != false {
				t.Fatalf("urgent = %v (%T), want false", got, got)
			}
		case orphanID:
			// empty relation normalizes to false, not null.
			if got := row["partner_id"]; got != false {
				t.Fatalf("empty partner_id = %v (%T), want false", got, got)
			}
		default:
			t.Fatalf("unexpected row: %v", row)
		}
	}

	// x2many serializes as an array of [id, name] tuples on the inverse side.
	partnerRows := asList(t, suite.rpcResult(t, rpcPath(confPartnerModelName, "read", partnerID),
		map[string]any{"fields": []string{"id", "name", "ticket_ids"}}, authAPIKey()))
	partnerRead := asMap(t, partnerRows[0])
	tickets := asList(t, partnerRead["ticket_ids"])
	if len(tickets) != 1 {
		t.Fatalf("ticket_ids = %v, want one tuple", partnerRead["ticket_ids"])
	}
	requireTuple(t, tickets[0], linkedID, "partner ticket_ids[0]")
}

// TestConf01Onchange proves the onchange transport path: draft values go in,
// the registered hook's update comes back under "value".
func TestConf01Onchange(t *testing.T) {
	suite := newConfSuite(t)

	result := asMap(t, suite.rpcResult(t, rpcPath(confTicketModelName, "onchange"), map[string]any{
		"values":      map[string]any{"name": "draft"},
		"field_names": []string{"name"},
	}, authAPIKey()))
	value := asMap(t, result["value"])
	if got := value["note"]; got != "onchange:draft" {
		t.Fatalf("onchange note = %v, want %q", got, "onchange:draft")
	}
}

// TestConf01AnonymousIsRejected pins the transport-level contract that model
// RPC always requires an identity (anonymous row of the CONF-02 matrix).
func TestConf01AnonymousIsRejected(t *testing.T) {
	suite := newConfSuite(t)

	status, _ := suite.rpcCall(t, rpcPath(confPartnerModelName, "search_read"),
		map[string]any{"fields": []string{"id"}}, authNone(), nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous search_read status = %d, want %d", status, http.StatusUnauthorized)
	}
}
