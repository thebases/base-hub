package all

// File overview:
// - Responsibility: source-scan checks for the coding-pattern rules in
//   `.agents/memory/context/coding-rules.md` "Coding patterns" (plan T105) that a parser can check.
// - Key symbols: TestNoErrNoRowsEquality (rule 11), TestContextBackgroundCallers (rule 1).
// - Regenerate the allowlist with `go test ./modules/all -run TestContextBackgroundCallers -update`,
//   then replace every TODO with a reason.

import (
	"go/ast"
	"go/token"
	"testing"
)

// TestNoErrNoRowsEquality fails on `err == sql.ErrNoRows` (or pgx.ErrNoRows): a wrapped error never
// matches `==`, so the not-found branch is skipped silently. Use errors.Is.
func TestNoErrNoRowsEquality(t *testing.T) {
	funcs, err := funcsMatching("../..", []string{"modules", "internal"}, isErrNoRowsComparison)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range funcs {
		t.Errorf("%s compares an error with ErrNoRows using ==/!=: use errors.Is(err, sql.ErrNoRows)", fn)
	}
}

func isErrNoRowsComparison(node ast.Node) bool {
	binary, ok := node.(*ast.BinaryExpr)
	if !ok || (binary.Op != token.EQL && binary.Op != token.NEQ) {
		return false
	}
	return isErrNoRowsSelector(binary.X) || isErrNoRowsSelector(binary.Y)
}

func isErrNoRowsSelector(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "ErrNoRows"
}

const contextBackgroundAllowlistPath = "testdata/context_background_allowlist.txt"

const contextBackgroundAllowlistHeader = `# context.Background() callers in modules/** and internal/models/** (plan T105, coding rule 1).
# Format: <path>:<Func>  <reason>. Tenant work must carry the request or event context; work that outlives
# the request uses context.WithoutCancel(ctx) plus a timeout. Keep only process-level or nil-ctx fallbacks.
`

// TestContextBackgroundCallers lists non-test functions in modules/** and internal/models/** that call
// context.Background(). Each must be in the allowlist with a reason; new callers fail the test.
func TestContextBackgroundCallers(t *testing.T) {
	callers, err := funcsMatching("../..", []string{"modules", "internal/models"}, isContextBackgroundCall)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := readFuncAllowlist(contextBackgroundAllowlistPath)
	if err != nil && !*updateGolden {
		t.Fatal(err)
	}
	if *updateGolden {
		writeFuncAllowlist(t, contextBackgroundAllowlistPath, contextBackgroundAllowlistHeader, "TODO(T105): not yet reviewed", callers, allowed)
		return
	}
	for _, caller := range callers {
		if _, ok := allowed[caller]; !ok {
			t.Errorf("%s calls context.Background(): carry the caller's ctx (context.WithoutCancel for detached work), or add it to %s with a reason", caller, contextBackgroundAllowlistPath)
		}
		delete(allowed, caller)
	}
	for caller := range allowed {
		t.Errorf("allowlist entry %s no longer calls context.Background(): remove it", caller)
	}
}

func isContextBackgroundCall(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	return ok && isPackageSelector(call.Fun, "context", "Background")
}
