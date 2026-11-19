package masterdata

// File overview:
// - Responsibility: verifies masterdata import, alignment, and rendered view output for view xml.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down import and render contracts so bootstrap data and generated XML stay stable across refactors.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadXMLImportRecordsParsesUIViewRecords(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "modules", "base", "views", "tbl_partner_views.xml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir views dir: %v", err)
	}

	payload := `<?xml version="1.0" encoding="utf-8"?>
<data noupdate="1">
  <record id="view_tbl_partner_form" model="ui.view">
    <field name="name">tbl.partner.form</field>
    <field name="model">tbl.partner</field>
    <field name="type">form</field>
    <field name="inherit_id" ref="base.view_partner_base"/>
    <field name="group_ids" eval="[(4, ref('base.group_system')), (4, ref('group_user'))]"/>
    <field name="arch" type="xml">
      <form string="Partner">
        <sheet>
          <field name="name"/>
        </sheet>
      </form>
    </field>
  </record>
</data>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write xml fixture: %v", err)
	}

	records, err := loadXMLImportRecords(path)
	if err != nil {
		t.Fatalf("loadXMLImportRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}

	record := records[0]
	if record.Model != "ui.view" {
		t.Fatalf("model = %q, want ui.view", record.Model)
	}
	if record.ExternalID != "base.view_tbl_partner_form" {
		t.Fatalf("external id = %q, want base.view_tbl_partner_form", record.ExternalID)
	}
	if !record.NoUpdate {
		t.Fatalf("expected noupdate=true")
	}
	if got := record.Data["inherit_id"]; got != "base.view_partner_base" {
		t.Fatalf("inherit_id = %#v, want base.view_partner_base", got)
	}
	if got := record.Data["group_ids"]; !reflect.DeepEqual(got, []string{"base.group_system", "base.group_user"}) {
		t.Fatalf("group_ids = %#v", got)
	}
	arch, _ := record.Data["arch"].(string)
	if arch == "" || arch[0] != '<' {
		t.Fatalf("arch = %q, want xml payload", arch)
	}
}

func TestLoadXMLImportRecordsAcceptsLegacyWrapper(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "modules", "base", "views", "tbl_groups_views.xml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir views dir: %v", err)
	}

	payload := `<?xml version="1.0" encoding="utf-8"?>
<odoo>
  <data noupdate="1">
    <record id="view_tbl_groups_list" model="ui.view">
      <field name="name">tbl.groups.list</field>
      <field name="model">tbl.groups</field>
      <field name="type">list</field>
      <field name="arch" type="xml">
        <list string="Groups">
          <field name="name"/>
        </list>
      </field>
    </record>
  </data>
</odoo>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write xml fixture: %v", err)
	}

	records, err := loadXMLImportRecords(path)
	if err != nil {
		t.Fatalf("loadXMLImportRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if !records[0].NoUpdate {
		t.Fatalf("expected noupdate=true")
	}
}

func TestLoadXMLImportRecordsParsesMenuItemsAndActions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "modules", "base", "views", "menu_views.xml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir views dir: %v", err)
	}

	payload := `<?xml version="1.0" encoding="utf-8"?>
<data noupdate="1">
  <record id="action_tbl_partner" model="ui.actions.window">
    <field name="name">Partners</field>
    <field name="sys_model">tbl.partner</field>
    <field name="view_mode">list,form</field>
  </record>
  <record id="action_open_settings" model="ui.actions.client">
    <field name="name">Open Settings</field>
    <field name="tag">reload</field>
    <field name="params" eval="{'menu_id': ref('menu_root')}"/>
  </record>
  <menuitem id="menu_root" name="Settings" sequence="10">
    <menuitem id="menu_partner" name="Partners" action="action_tbl_partner" sequence="20"/>
  </menuitem>
</data>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write xml fixture: %v", err)
	}

	records, err := loadXMLImportRecords(path)
	if err != nil {
		t.Fatalf("loadXMLImportRecords() error = %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("record count = %d, want 4", len(records))
	}

	var (
		windowAction ImportRecord
		clientAction ImportRecord
		rootMenu     ImportRecord
		childMenu    ImportRecord
	)
	for _, record := range records {
		switch record.ExternalID {
		case "base.action_tbl_partner":
			windowAction = record
		case "base.action_open_settings":
			clientAction = record
		case "base.menu_root":
			rootMenu = record
		case "base.menu_partner":
			childMenu = record
		}
	}

	if windowAction.Model != "ui.actions.window" {
		t.Fatalf("window action model = %q", windowAction.Model)
	}
	if clientAction.Model != "ui.actions.client" {
		t.Fatalf("client action model = %q", clientAction.Model)
	}
	params, ok := clientAction.Data["params"].(map[string]any)
	if !ok || params["menu_id"] != "base.menu_root" {
		t.Fatalf("client params = %#v", clientAction.Data["params"])
	}
	if rootMenu.Model != "ui.menu" {
		t.Fatalf("root menu model = %q", rootMenu.Model)
	}
	if got := rootMenu.Data["name"]; got != "Settings" {
		t.Fatalf("root menu name = %#v", got)
	}
	if childMenu.Model != "ui.menu" {
		t.Fatalf("child menu model = %q", childMenu.Model)
	}
	if got := childMenu.Data["parent_id"]; got != "base.menu_root" {
		t.Fatalf("child parent = %#v", got)
	}
	action, ok := childMenu.Data["action"].(map[string]any)
	if !ok {
		t.Fatalf("child action type = %T", childMenu.Data["action"])
	}
	if got := action["type"]; got != "ui.actions.window" {
		t.Fatalf("child action type = %#v", got)
	}
	if got := action["bid"]; got != "base.action_tbl_partner" {
		t.Fatalf("child action bid = %#v", got)
	}
	if got := childMenu.Data["route_path"]; got != "/web/tbl.partner" {
		t.Fatalf("child route_path = %#v", got)
	}
	if got := rootMenu.Data["active"]; got != true {
		t.Fatalf("root menu active = %#v, want true by default", got)
	}
}

func TestLoadXMLImportRecordsMenuItemActiveOverride(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "modules", "base", "views", "menu_active_views.xml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir views dir: %v", err)
	}

	payload := `<?xml version="1.0" encoding="utf-8"?>
<data noupdate="1">
  <menuitem id="menu_inactive" name="Archived Menu" active="0"/>
  <menuitem id="menu_active" name="Active Menu" active="true"/>
</data>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write xml fixture: %v", err)
	}

	records, err := loadXMLImportRecords(path)
	if err != nil {
		t.Fatalf("loadXMLImportRecords() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	for _, record := range records {
		switch record.ExternalID {
		case "base.menu_inactive":
			if got := record.Data["active"]; got != false {
				t.Fatalf("inactive menu active = %#v, want false", got)
			}
		case "base.menu_active":
			if got := record.Data["active"]; got != true {
				t.Fatalf("active menu active = %#v, want true", got)
			}
		}
	}
}

func TestLoadXMLImportRecordsMenuItemGroups(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "modules", "base", "views", "menu_groups_views.xml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir views dir: %v", err)
	}
	payload := `<?xml version="1.0" encoding="utf-8"?>
<data noupdate="1">
  <menuitem id="menu_restricted" name="Restricted" groups="base.group_multi_company, base.group_no_one"/>
  <menuitem id="menu_open" name="Open"/>
</data>`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write xml fixture: %v", err)
	}
	records, err := loadXMLImportRecords(path)
	if err != nil {
		t.Fatalf("loadXMLImportRecords() error = %v", err)
	}
	for _, record := range records {
		groups, has := record.Data["group_bids"]
		switch record.ExternalID {
		case "base.menu_restricted":
			want := []string{"base.group_multi_company", "base.group_no_one"}
			if !reflect.DeepEqual(groups, want) {
				t.Fatalf("group_bids = %#v, want %#v", groups, want)
			}
		case "base.menu_open":
			if has {
				t.Fatalf("unrestricted menu must not carry group_bids, got %#v", groups)
			}
		}
	}
}
