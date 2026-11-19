package models

// File overview:
// - Responsibility: the builtin workflow action registry — the `NodeDef` each node file under
//   service/nodes registers (plan T93), and the catalog view of it (`ETLActionCatalogEntry`)
//   consumed by action/catalog normalization and the execution engine. Owns no persisted rows;
//   data/11_etl_action_catalog.json must stay 1:1 with it (TestWorkflowActionCatalogSeedMatchesNodeDefs).
// - Key symbols: `RegisterNodeDef`, `LookupNodeDef`, `NodeDefs`, `ResolveBuiltinWorkflowAction`, `BuiltinETLActions`.
// - Pattern: Registration happens only from package init (service/nodes), before any reader runs,
//   so the map needs no lock. A binary or test that resolves builtins must link service/nodes.

import (
	"fmt"
	"sort"
	"strings"
)

type ETLActionCatalogEntry struct {
	Label          string
	Name           string
	Version        int
	Description    string
	HandlerKey     string
	HandlerVersion int
	DefaultParams  map[string]any
}

// nodeDefs is keyed by nodeDefKey(code, version).
var nodeDefs = map[string]NodeDef{}

func nodeDefKey(code string, version int) string {
	return fmt.Sprintf("%s@%d", strings.TrimSpace(code), normalizedNodeVersion(version))
}

func normalizedNodeVersion(version int) int {
	if version <= 0 {
		return 1
	}
	return version
}

// RegisterNodeDef adds one builtin node definition. It is called from package init only; an empty
// code, a param without exactly one storage target or a duplicate code@version is a programming
// error and panics so the binary never starts with an ambiguous registry.
func RegisterNodeDef(def NodeDef) {
	def.Code = strings.TrimSpace(def.Code)
	def.Version = normalizedNodeVersion(def.Version)
	if def.Code == "" {
		panic("workflow: RegisterNodeDef needs a code")
	}
	for _, param := range def.Params {
		if (param.Column == "") == (param.ExtraKey == "") {
			panic(fmt.Sprintf("workflow: node %s param %q needs exactly one of Column or ExtraKey", def.Code, param.Name))
		}
	}
	key := nodeDefKey(def.Code, def.Version)
	if _, exists := nodeDefs[key]; exists {
		panic("workflow: node " + key + " registered twice")
	}
	nodeDefs[key] = def
}

// LookupNodeDef returns the registered definition of code@version (version <= 0 means 1).
func LookupNodeDef(code string, version int) (NodeDef, bool) {
	def, ok := nodeDefs[nodeDefKey(code, version)]
	return def, ok
}

// NodeDefs lists every registered definition in Sequence order (code, version break ties).
func NodeDefs() []NodeDef {
	defs := make([]NodeDef, 0, len(nodeDefs))
	for _, def := range nodeDefs {
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Sequence != defs[j].Sequence {
			return defs[i].Sequence < defs[j].Sequence
		}
		if defs[i].Code != defs[j].Code {
			return defs[i].Code < defs[j].Code
		}
		return defs[i].Version < defs[j].Version
	})
	return defs
}

// ResolveBuiltinWorkflowAction resolves the builtin workflow action handled by this package.
func ResolveBuiltinWorkflowAction(name string, version int) (ETLActionCatalogEntry, bool) {
	def, ok := LookupNodeDef(name, version)
	if !ok {
		return ETLActionCatalogEntry{}, false
	}
	return def.catalogEntry(), true
}

func BuiltinETLActions() []ETLActionCatalogEntry {
	defs := NodeDefs()
	entries := make([]ETLActionCatalogEntry, 0, len(defs))
	for _, def := range defs {
		entries = append(entries, def.catalogEntry())
	}
	return entries
}

// ResolveBuiltinETLAction resolves the builtin etl action handled by this package.
func ResolveBuiltinETLAction(name string, version int) (ETLActionCatalogEntry, bool) {
	return ResolveBuiltinWorkflowAction(name, version)
}
