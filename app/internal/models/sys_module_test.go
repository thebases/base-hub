package models

// File overview:
// - Responsibility: verifies that base and every base-prefixed module can never be
//   uninstalled through the sys.module.module runtime RPC surface (unlink, write).
// - Key symbols: focused Test cases for rejectIfProtectedModuleName, writeTargetsModuleUninstall,
//   and the guarded RuntimeMethod handlers.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestRejectIfProtectedModuleName(t *testing.T) {
	tests := []struct {
		name      string
		wantError bool
	}{
		{name: "base", wantError: true},
		{name: "base-automation", wantError: true},
		{name: "base-workflow", wantError: true},
		{name: "base-mcp", wantError: true},
		{name: "workflow", wantError: false},
		{name: "mcp", wantError: false},
		{name: "knowledge", wantError: false},
		{name: "website", wantError: false},
		{name: "database", wantError: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectIfProtectedModuleName(tt.name)
			if tt.wantError && err == nil {
				t.Fatalf("rejectIfProtectedModuleName(%q) = nil, want an error", tt.name)
			}
			if !tt.wantError && err != nil {
				t.Fatalf("rejectIfProtectedModuleName(%q) = %v, want nil", tt.name, err)
			}
		})
	}
}

func TestWriteTargetsModuleUninstall(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{name: "moves state away from installed", values: map[string]any{"state": "to_remove"}, want: true},
		{name: "keeps installed state", values: map[string]any{"state": "installed"}, want: false},
		{name: "benign field edit", values: map[string]any{"category": "Hidden"}, want: false},
		{name: "no values", values: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := writeTargetsModuleUninstall([]any{tt.values}, nil)
			if got != tt.want {
				t.Fatalf("writeTargetsModuleUninstall(%#v) = %v, want %v", tt.values, got, tt.want)
			}
		})
	}
}

func TestUnlinkSysModuleRejectsProtectedModule(t *testing.T) {
	restore := orm.StubRuntimeRepository(SysModuleModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"name": "base-automation"}}, nil
		},
	})
	defer restore()

	if _, err := unlinkSysModule(context.Background(), nil, []string{"1"}, nil, nil); err == nil {
		t.Fatalf("unlinkSysModule(base-automation) = nil error, want rejection")
	}
}

func TestWriteSysModuleRejectsUninstallingProtectedModule(t *testing.T) {
	restore := orm.StubRuntimeRepository(SysModuleModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"name": "base-automation"}}, nil
		},
	})
	defer restore()

	_, err := writeSysModule(context.Background(), nil, []string{"1"}, []any{map[string]any{"state": "not_installed"}}, nil)
	if err == nil {
		t.Fatalf("writeSysModule(base-automation, state=not_installed) = nil error, want rejection")
	}
}
