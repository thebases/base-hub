package models

// File overview:
// - Responsibility: defines the `project` module runtime bootstrap ordering, mirroring the
//   base-credential/base-calendar modules' per-file registration pattern.
// - Key symbols: `RegisterRuntimeBootstrap`, `InitRuntimeModels`, `runtimeBootstrapEntry`.

import "sort"

type runtimeBootstrapEntry struct {
	name     string
	order    int
	initFn   func()
	shutdown func()
}

var runtimeBootstrapRegistry []runtimeBootstrapEntry

// RegisterRuntimeBootstrap keeps model bootstrap ownership close to each file
// while the module initializes its runtime surface explicitly.
func RegisterRuntimeBootstrap[T any](name string, order int, initFn func() T, shutdownFn func()) {
	if name == "" {
		panic("project models: runtime bootstrap name is required")
	}
	if initFn == nil {
		panic("project models: runtime bootstrap init function is required for " + name)
	}
	if shutdownFn == nil {
		panic("project models: runtime bootstrap shutdown function is required for " + name)
	}
	for _, existing := range runtimeBootstrapRegistry {
		if existing.name == name {
			panic("project models: duplicate runtime bootstrap registration for " + name)
		}
	}
	runtimeBootstrapRegistry = append(runtimeBootstrapRegistry, runtimeBootstrapEntry{
		name:  name,
		order: order,
		initFn: func() {
			_ = initFn()
		},
		shutdown: shutdownFn,
	})
}

// InitRuntimeModels initializes every registered model in declared order and
// returns one combined shutdown that tears them down in reverse.
func InitRuntimeModels() func() {
	entries := append([]runtimeBootstrapEntry(nil), runtimeBootstrapRegistry...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].order == entries[j].order {
			return entries[i].name < entries[j].name
		}
		return entries[i].order < entries[j].order
	})

	shutdowns := make([]func(), 0, len(entries))
	for _, entry := range entries {
		entry.initFn()
		shutdowns = append(shutdowns, entry.shutdown)
	}
	return func() {
		for i := len(shutdowns) - 1; i >= 0; i-- {
			shutdowns[i]()
		}
	}
}
