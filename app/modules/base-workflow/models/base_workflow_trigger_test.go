package models

import (
	"git.thebase.vn/base_hub/internal/config"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestWorkflowTriggerModelSpecExposesWebhookKafkaMQTTModelSelection(t *testing.T) {
	meta, err := orm.RegisterModelType[WorkflowTriggerRecord](WorkflowTriggerModelSpec)
	if err != nil {
		t.Fatalf("RegisterModelType() error = %v", err)
	}

	field, err := meta.Field("trigger_type")
	if err != nil {
		t.Fatalf("expected trigger_type field metadata: %v", err)
	}
	if field.Kind != orm.FieldKindSelection {
		t.Fatalf("trigger_type kind = %q, want %q", field.Kind, orm.FieldKindSelection)
	}

	got := field.Selection
	if len(got) != 5 {
		t.Fatalf("trigger_type selection count = %d, want 5", len(got))
	}

	want := []orm.SelectionOption{
		{Value: "webhook", Label: "Webhook"},
		{Value: "kafka", Label: "Kafka"},
		{Value: "mqtt", Label: "MQTT"},
		{Value: "model", Label: "Model Event"},
		{Value: "tcp", Label: "TCP"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trigger_type selection[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestNormalizeWorkflowTriggerValuesAcceptsWebhook(t *testing.T) {
	values, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id":  int64(7),
		"trigger_type": " webhook ",
		"topic":        "orders.created",
		"config": map[string]any{
			"consumer_group": "ignored",
		},
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowTriggerValues() error = %v", err)
	}

	if got := values["trigger_type"]; got != "webhook" {
		t.Fatalf("trigger_type = %#v, want webhook", got)
	}
	if got := values["is_active"]; got != true {
		t.Fatalf("is_active = %#v, want true", got)
	}
	config, ok := values["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %#v, want map[string]any", values["config"])
	}
	if len(config) != 0 {
		t.Fatalf("config = %#v, want empty config for webhook", config)
	}
}

func TestNormalizeWorkflowTriggerValuesAcceptsModelWithDomainAndSynthesizesTopic(t *testing.T) {
	values, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id":  int64(7),
		"trigger_type": "model",
		"config": map[string]any{
			"model":     "res.partner",
			"operation": "after_write",
			"domain":    []any{[]any{"status", "=", "active"}},
		},
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowTriggerValues() error = %v", err)
	}

	config, ok := values["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %#v, want map[string]any", values["config"])
	}
	if got := config["model"]; got != "res.partner" {
		t.Fatalf("config.model = %#v, want res.partner", got)
	}
	if got := config["operation"]; got != "after_write" {
		t.Fatalf("config.operation = %#v, want after_write", got)
	}
	if _, ok := config["domain"]; !ok {
		t.Fatalf("config.domain missing, want the parsed-valid domain preserved")
	}
	if got := values["topic"]; got != "model:res.partner:after_write" {
		t.Fatalf("topic = %#v, want auto-derived model:res.partner:after_write", got)
	}
}

func TestNormalizeWorkflowTriggerValuesRejectsModelWithoutModel(t *testing.T) {
	_, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id":  int64(7),
		"trigger_type": "model",
		"config": map[string]any{
			"operation": "after_create",
		},
	}, nil, true)
	if err == nil {
		t.Fatalf("normalizeWorkflowTriggerValues() error = nil, want error for missing model")
	}
}

func TestNormalizeWorkflowTriggerValuesRejectsModelWithInvalidOperation(t *testing.T) {
	_, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id":  int64(7),
		"trigger_type": "model",
		"config": map[string]any{
			"model":     "res.partner",
			"operation": "before_read",
		},
	}, nil, true)
	if err == nil {
		t.Fatalf("normalizeWorkflowTriggerValues() error = nil, want error for invalid operation")
	}
}

func TestNormalizeWorkflowTriggerValuesRejectsModelWithInvalidDomain(t *testing.T) {
	_, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id":  int64(7),
		"trigger_type": "model",
		"config": map[string]any{
			"model":     "res.partner",
			"operation": "after_create",
			"domain":    "not-a-domain",
		},
	}, nil, true)
	if err == nil {
		t.Fatalf("normalizeWorkflowTriggerValues() error = nil, want error for invalid domain")
	}
}

func withTCPConfig(t *testing.T, cfg config.TCPConfig) {
	t.Helper()
	prev := config.Get().Tcp
	config.Update(func(c *config.AppConfig) { c.Tcp = cfg })
	t.Cleanup(func() { config.Update(func(c *config.AppConfig) { c.Tcp = prev }) })
}

func TestNormalizeWorkflowTriggerValuesTCPDefaults(t *testing.T) {
	withTCPConfig(t, config.TCPConfig{UseTCP: true})
	values, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id": int64(7), "trigger_type": "tcp", "config": map[string]any{},
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg := values["config"].(map[string]any)
	if cfg["port"] != int64(10123) || cfg["framing"] != "len2_bcd" || cfg["tls_mode"] != "none" ||
		cfg["reply_timeout_ms"] != int64(30000) || cfg["len_includes_header"] != false {
		t.Fatalf("defaults wrong: %#v", cfg)
	}
	if values["topic"] != "tcp:10123" {
		t.Fatalf("topic = %#v", values["topic"])
	}
}

func TestNormalizeWorkflowTriggerValuesTCPExplicitAndTopicDerived(t *testing.T) {
	withTCPConfig(t, config.TCPConfig{UseTCP: true})
	values, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id": int64(7), "trigger_type": "tcp", "topic": "ignored",
		"config": map[string]any{"port": float64(20001), "framing": "STX_ETX", "len_includes_header": true, "reply_timeout_ms": "5000"},
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg := values["config"].(map[string]any)
	if cfg["port"] != int64(20001) || cfg["framing"] != "stx_etx" || cfg["len_includes_header"] != true || cfg["reply_timeout_ms"] != int64(5000) {
		t.Fatalf("config wrong: %#v", cfg)
	}
	if values["topic"] != "tcp:20001" {
		t.Fatalf("topic must be derived from the port, got %#v", values["topic"])
	}
}

func TestNormalizeWorkflowTriggerValuesTCPRejections(t *testing.T) {
	withTCPConfig(t, config.TCPConfig{UseTCP: true})
	cases := map[string]map[string]any{
		"port below range":  {"port": 9999},
		"port above range":  {"port": 50000},
		"port not a number": {"port": "abc"},
		"bad framing":       {"framing": "iso"},
		"bad tls mode":      {"tls_mode": "starttls"},
		"tls without certs": {"tls_mode": "tls"},
		"zero timeout":      {"reply_timeout_ms": 0},
		"huge timeout":      {"reply_timeout_ms": 999999999},
	}
	for name, cfg := range cases {
		_, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{"workflow_id": int64(7), "trigger_type": "tcp", "config": cfg}, nil, true)
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	withTCPConfig(t, config.TCPConfig{UseTCP: true, TLSCertFile: "c", TLSKeyFile: "k"})
	if _, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{"workflow_id": int64(7), "trigger_type": "tcp", "config": map[string]any{"tls_mode": "tls"}}, nil, true); err != nil {
		t.Errorf("tls with cert files: %v", err)
	}
	if _, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{"workflow_id": int64(7), "trigger_type": "tcp", "config": map[string]any{"tls_mode": "mtls"}}, nil, true); err == nil {
		t.Error("mtls without client CA must fail")
	}
}

func TestNormalizeWorkflowTriggerValuesTCPRequiresUseTCPOnCreate(t *testing.T) {
	withTCPConfig(t, config.TCPConfig{UseTCP: false})
	in := orm.RecordMap{"workflow_id": int64(7), "trigger_type": "tcp", "config": map[string]any{}}
	if _, err := normalizeWorkflowTriggerValues(nil, in, nil, true); err == nil {
		t.Fatal("create with useTcp=false must fail")
	}
	current := workflowTriggerStateFromRecord(orm.RecordMap{"workflow_id": int64(7), "trigger_type": "tcp", "topic": "tcp:10123"})
	if _, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{"is_active": false}, &current, false); err != nil {
		t.Fatalf("editing an existing tcp row must stay possible: %v", err)
	}
}

func TestCopiedTCPTriggerIsInactive(t *testing.T) {
	v := cloneWorkflowTriggerCopyValues(orm.RecordMap{"trigger_type": "tcp", "is_active": true, "topic": "tcp:10123"}, 9, "")
	if v["is_active"] != false {
		t.Fatalf("copied tcp trigger must be inactive: %#v", v)
	}
}
