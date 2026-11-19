package orm

// File overview:
// - Responsibility: T91 typed adapter: kwargs/row decoding by json tags, many2one collapse, invalid-params
//   errors on type mismatch, derived Params, Call.ID, and ModelSet ordering (T88).
// - Key symbols: TestDecodeRecord*, TestTypedRuntimeMethod, TestModelSetInitOrder.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type typedTestArgs struct {
	Name      string `json:"name" meta:"required,label:Name"`
	PartnerID int64  `json:"partner_id"`
	ParentID  *int64 `json:"parent_id"`
	Count     int    `json:"count"`
	Ratio     float64
	Tags      []string `json:"tags"`
}

// typedTestRow is a model-shaped row: embedded BaseModel fields decode like top-level ones.
type typedTestRow struct {
	BaseModel
	typedTestArgs
}

func TestDecodeRecordCollapsesMany2OneAndFalse(t *testing.T) {
	row := RecordMap{
		"id":         int64(7),
		"name":       "x",
		"partner_id": [2]any{int64(3), "Partner"},
		"parent_id":  false,
		"count":      float64(4),
		"tags":       []any{"a", "b"},
		"ignored":    "extra keys are fine",
	}
	got, err := DecodeRecord[typedTestRow](row)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 7 || got.Name != "x" || got.PartnerID != 3 || got.ParentID != nil || got.Count != 4 || strings.Join(got.Tags, ",") != "a,b" {
		t.Fatalf("decoded %+v", got)
	}

	// The JSON round-trip shape ([]any) collapses too.
	got, err = DecodeRecord[typedTestRow](RecordMap{"parent_id": []any{float64(9), "Parent"}})
	if err != nil || got.ParentID == nil || *got.ParentID != 9 {
		t.Fatalf("[]any pair: %+v, %v", got, err)
	}
}

func TestDecodeRecordTypeMismatchIsInvalidParams(t *testing.T) {
	for _, row := range []RecordMap{{"count": "four"}, {"count": 1.5}, {"name": int64(3)}} {
		_, err := DecodeRecord[typedTestArgs](row)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != ErrInvalidParams {
			t.Fatalf("row %v: err = %v, want invalid params", row, err)
		}
	}
}

func TestTypedRuntimeMethod(t *testing.T) {
	method := Typed(RuntimeMethod{Name: "use", Public: true, UsesPathID: true},
		func(_ context.Context, c Call[typedTestArgs]) (string, error) {
			id, err := c.ID()
			if err != nil {
				return "", err
			}
			return c.Args.Name + ":" + string(rune('0'+id)) + ":" + string(rune('0'+c.Args.PartnerID)), nil
		})

	// kwargs win over a positional object.
	got, err := method.Call(context.Background(), nil, []string{"5"},
		[]any{map[string]any{"name": "positional", "partner_id": float64(1)}},
		map[string]any{"name": "kw", "context": map[string]any{"lang": "en"}})
	if err != nil || got != "kw:5:1" {
		t.Fatalf("call = %v, %v", got, err)
	}

	_, err = method.Call(context.Background(), nil, []string{"1", "2"}, nil, map[string]any{"name": "x"})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != ErrInvalidParams {
		t.Fatalf("two ids: err = %v, want invalid params", err)
	}

	// Params derive from the scalar fields of A; slices have no input type and are left out.
	var names []string
	for _, param := range method.Params {
		names = append(names, param.Name+"/"+param.Type)
	}
	if want := "name/char,partner_id/integer,parent_id/integer,count/integer,Ratio/float"; strings.Join(names, ",") != want {
		t.Fatalf("params = %s, want %s", strings.Join(names, ","), want)
	}
	if !method.Params[0].Required || method.Params[0].Label != "Name" {
		t.Fatalf("name param = %+v", method.Params[0])
	}
	// A many2one pair passes the declared integer param check, which runs before Call.
	if err := ValidateRuntimeMethodKwargs(method, nil, map[string]any{"name": "x", "partner_id": []any{float64(3), "P"}}); err != nil {
		t.Fatalf("m2o pair rejected by param validation: %v", err)
	}
}

func TestModelSetInitOrder(t *testing.T) {
	var set ModelSet
	var trace []string
	add := func(name string, order int) {
		set.Add(name, order, func() { trace = append(trace, "init "+name) }, func() { trace = append(trace, "stop "+name) })
	}
	add("b", 10)
	add("a", 10)
	add("c", 5)
	shutdown := set.Init()
	shutdown()
	want := "init c,init a,init b,stop b,stop a,stop c"
	if strings.Join(trace, ",") != want {
		t.Fatalf("trace = %s, want %s", strings.Join(trace, ","), want)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("duplicate name must panic")
		}
	}()
	add("a", 1)
}
