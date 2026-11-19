package orm

// File overview:
// - Responsibility: one shared replacement for the per-module `models/runtime_bootstrap.go` copies (plan T88):
//   a module declares its models into a ModelSet and boots them in one explicit order.
// - Key symbols: `ModelSet`, `Define`, `ModelSet.Add`, `ModelSet.Init`.
// - Pattern: composition only: Define wraps the existing ModelSpec + NewHandlerWithModel path, so a converted
//   model registers exactly as before (same storage name, same order, same handler).

import (
	"sort"
	"strings"
)

type modelSetEntry struct {
	name     string
	order    int
	init     func()
	shutdown func()
}

// ModelSet collects one module's runtime bootstrap entries. The zero value is ready to use; it is
// filled from package-level var initializers and booted once by the module's Bootstrap.
type ModelSet struct {
	entries []modelSetEntry
}

// Define builds the handler for one model and adds it to set, replacing the
// `init() { RegisterRuntimeBootstrap(...) }` + `Definition: ModelDefinition(...)` boilerplate.
// A nil spec.Definition is derived from T. Handler options pass through unchanged.
func Define[T any](set *ModelSet, order int, spec ModelSpec, opts ...HandlerOption[T]) *Handler[T] {
	if spec.Definition == nil {
		spec.Definition = ModelDefinition(spec.Name, (*T)(nil))
	}
	handler := NewHandlerWithModel[T](spec, opts...)
	set.Add(spec.Name, order, initHandlerFunc(handler), handler.Shutdown)
	return handler
}

// initHandlerFunc adapts InitDefault (which returns the handler for chaining) to a plain init func.
func initHandlerFunc[T any](handler *Handler[T]) func() {
	return func() { handler.InitDefault() }
}

// Add registers a bootstrap entry that is not a model handler (a sync job, a schema backfill, ...).
// It panics on an empty name, a nil init and a duplicate name, like RegisterRuntimeBootstrap: these
// are programming errors in package initialisation and must stop the binary at start-up.
func (s *ModelSet) Add(name string, order int, init func(), shutdown func()) {
	if strings.TrimSpace(name) == "" {
		panic("orm: model set entry name is required")
	}
	if init == nil {
		panic("orm: model set init function is required for " + name)
	}
	if shutdown == nil {
		shutdown = func() {}
	}
	for _, existing := range s.entries {
		if existing.name == name {
			panic("orm: duplicate model set entry for " + name)
		}
	}
	s.entries = append(s.entries, modelSetEntry{name: name, order: order, init: init, shutdown: shutdown})
}

// Init boots every entry by order, then name (same rule as InitRuntimeModels, so converting a module
// keeps its registration order), and returns one shutdown that tears them down in reverse.
func (s *ModelSet) Init() func() {
	entries := append([]modelSetEntry(nil), s.entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].order == entries[j].order {
			return entries[i].name < entries[j].name
		}
		return entries[i].order < entries[j].order
	})

	shutdowns := make([]func(), 0, len(entries))
	for _, entry := range entries {
		entry.init()
		shutdowns = append(shutdowns, entry.shutdown)
	}
	return func() {
		for i := len(shutdowns) - 1; i >= 0; i-- {
			shutdowns[i]()
		}
	}
}
