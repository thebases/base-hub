package models

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestUIMenuGroupFilterAllows(t *testing.T) {
	restricted := uiMenuTreeRow{GroupBIDs: []string{"base.group_multi_company", "base.group_no_one"}}
	open := uiMenuTreeRow{}

	holder := uiMenuGroupFilter{held: map[string]struct{}{"base.group_multi_company": {}}}
	if !holder.allows(restricted) {
		t.Fatalf("a caller holding a listed group must see the menu")
	}
	outsider := uiMenuGroupFilter{held: map[string]struct{}{"base.group_user": {}}}
	if outsider.allows(restricted) {
		t.Fatalf("a caller without any listed group must not see the menu")
	}
	if !outsider.allows(open) {
		t.Fatalf("an unrestricted menu must stay visible")
	}
	if !(uiMenuGroupFilter{unrestricted: true}).allows(restricted) {
		t.Fatalf("an unrestricted caller (sudo) must see every menu")
	}
}

func TestIsUIMenuAccessDenied(t *testing.T) {
	if !isUIMenuAccessDenied(orm.Forbiddenf("no read on %s", "x")) {
		t.Fatal("typed ErrForbidden must count as denied")
	}
	if !isUIMenuAccessDenied(fmt.Errorf("permission denied: record rule blocked read")) {
		t.Fatal("legacy permission denied string must count as denied")
	}
	if isUIMenuAccessDenied(fmt.Errorf("connection reset")) {
		t.Fatal("unrelated errors must not count as denied")
	}
}

func TestBuildUIMenuSidebarTreeDropsSubtreeOfHiddenParent(t *testing.T) {
	parent := int64(1)
	// Row 1 (the parent) was filtered out upstream; its child 2 must not surface as a root.
	rows := []uiMenuTreeRow{{ID: 2, Name: "Child", ParentID: &parent, RoutePath: "/web/x"}}
	if tree := buildUIMenuSidebarTree(rows, map[int64]bool{1: true}); len(tree) != 0 {
		t.Fatalf("children of a hidden parent must be dropped, got %v", tree)
	}
}

func TestUIMenuCacheKeyChangesWhenVisibleSetChanges(t *testing.T) {
	a := []uiMenuTreeRow{{ID: 1}, {ID: 2}}
	b := []uiMenuTreeRow{{ID: 1}, {ID: 3}} // same count, same version, different rows
	if uiMenuCacheKey("s", 7, a) == uiMenuCacheKey("s", 7, b) {
		t.Fatal("cache key must change when the visible row set changes")
	}
	rev := []uiMenuTreeRow{{ID: 2}, {ID: 1}}
	if uiMenuCacheKey("s", 7, a) != uiMenuCacheKey("s", 7, rev) {
		t.Fatal("cache key must not depend on row order")
	}
}

func TestAdminMenuRootNameMatchesSeed(t *testing.T) {
	raw, err := os.ReadFile("../../modules/base/views/menu-base.xml")
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	want := fmt.Sprintf(`id="menu.admin" name="%s"`, adminMenuRootName)
	if !strings.Contains(string(raw), want) {
		t.Fatalf("menu-base.xml no longer seeds %s; update adminMenuRootName or the company gate breaks", want)
	}
}
