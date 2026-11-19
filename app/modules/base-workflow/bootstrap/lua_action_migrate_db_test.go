package bootstrap

// Plan T57: the Lua step migration visits every registered database, each with its own handle; a
// database that cannot be opened is skipped and one database's failure does not stop the others.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/uptrace/bun"
)

func TestReconcileLuaScriptStepsVisitsEveryDatabase(t *testing.T) {
	previousNames, previousOpen, previousRun := luaMigrationDatabases, luaMigrationOpenDB, reconcileLuaScriptStepsInDB
	t.Cleanup(func() {
		luaMigrationDatabases, luaMigrationOpenDB, reconcileLuaScriptStepsInDB = previousNames, previousOpen, previousRun
	})
	luaMigrationDatabases = func() []string { return []string{"tenant_b", "home", "gone", "tenant_a"} }
	luaMigrationOpenDB = func(name string) (*bun.DB, error) {
		if name == "gone" {
			return nil, errors.New("database does not exist")
		}
		return &bun.DB{}, nil
	}
	var visited []string
	reconcileLuaScriptStepsInDB = func(_ context.Context, name string, _ *bun.DB) error {
		visited = append(visited, name)
		if name == "tenant_a" {
			return errors.New("convert lua step 4: boom")
		}
		return nil
	}
	err := ReconcileLuaScriptSteps(context.Background())
	if strings.Join(visited, ",") != "home,tenant_a,tenant_b" {
		t.Fatalf("visited %v, want every openable database", visited)
	}
	if err == nil || !strings.Contains(err.Error(), "db tenant_a") {
		t.Fatalf("error %v must name the failing database", err)
	}
}
