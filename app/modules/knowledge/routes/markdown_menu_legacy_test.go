package routes

import "testing"

// A legacy snapshot with an RFC3339 updated_at must decode (it used to 500 /md/menu.json).
func TestDecodeMarkdownMenuSnapshotAcceptsLegacyUpdatedAt(t *testing.T) {
	raw := []byte(`{"items":[{"name":"A","updated_at":"2026-05-01T07:15:50Z"},{"name":"B","updated_at":1777620950},{"name":"C"}]}`)
	payload, err := decodeMarkdownMenuSnapshot(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := payload.Items[0].UpdatedAt; got != 1777619750 {
		t.Fatalf("legacy string updated_at = %d", got)
	}
	if got := payload.Items[1].UpdatedAt; got != 1777620950 {
		t.Fatalf("numeric updated_at = %d", got)
	}
	if payload.Items[2].UpdatedAt != 0 {
		t.Fatalf("missing updated_at should be 0")
	}
}
