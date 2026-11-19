package models

// File overview:
// - Responsibility: guards the bootstrap compute contract for every kernel model registered by this package.
// - Key symbols: `TestKernelModelsRegisterComputeHandlers`.
// - Pattern: Mirrors the server boot fail-fast check so a missing RegisterCompute call fails in CI instead of at deploy time.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// TestKernelModelsRegisterComputeHandlers mirrors the orm.ValidateComputeHandlers
// call in server boot (SRS-F-018) so any kernel model that declares a compute:
// meta tag without a matching orm.RegisterCompute call fails here first.
// Regression guard for "computed fields have no registered compute handler:
// ui.menu.statusbar_visible", where the field was declared on UIMenuRecord but
// only ui.view registered a handler.
func TestKernelModelsRegisterComputeHandlers(t *testing.T) {
	if err := orm.ValidateComputeHandlers(); err != nil {
		t.Fatalf("compute handler validation failed: %v", err)
	}
}
