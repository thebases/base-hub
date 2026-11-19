package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsMissingSysCredentialRelationMatchesSQLState(t *testing.T) {
	if !isMissingSysCredentialRelation(fmt.Errorf("claim: %w", &pgconn.PgError{Code: "42P01"})) {
		t.Fatal("wrapped 42P01 must match")
	}
	if isMissingSysCredentialRelation(&pgconn.PgError{Code: "42703"}) {
		t.Fatal("undefined_column must not match")
	}
	if isMissingSysCredentialRelation(errors.New(`relation "sys_credential" does not exist`)) {
		t.Fatal("plain text must not match")
	}
	if isMissingSysCredentialRelation(nil) {
		t.Fatal("nil must not match")
	}
}
