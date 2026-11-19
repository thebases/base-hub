package models

// File overview:
// - Responsibility: verifies base-module business model behavior and runtime metadata for res partner.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import "testing"

func TestResPartnerModelSpecRelations(t *testing.T) {
	childField, err := ResPartnerModelSpec.Definition.Field("child_ids")
	if err != nil {
		t.Fatalf("expected child_ids relation field: %v", err)
	}
	if childField.Stored {
		t.Fatalf("expected child_ids to be non-stored relation metadata")
	}

	bankAccountsField, err := ResPartnerModelSpec.Definition.Field("bank_account_ids")
	if err != nil {
		t.Fatalf("expected bank_account_ids relation field: %v", err)
	}
	if bankAccountsField.Relation != BankModelName {
		t.Fatalf("expected bank_account_ids relation %q, got %q", BankModelName, bankAccountsField.Relation)
	}
	if bankAccountsField.Inverse != "partner_id" {
		t.Fatalf("expected bank_account_ids inverse partner_id, got %q", bankAccountsField.Inverse)
	}
	if bankAccountsField.Stored {
		t.Fatalf("expected bank_account_ids to be non-stored relation metadata")
	}

	usersField, err := ResPartnerModelSpec.Definition.Field("user_ids")
	if err != nil {
		t.Fatalf("expected user_ids relation field: %v", err)
	}
	if usersField.Relation != ResUserModelName {
		t.Fatalf("expected user_ids relation %q, got %q", ResUserModelName, usersField.Relation)
	}
	if usersField.Inverse != "partner_id" {
		t.Fatalf("expected user_ids inverse partner_id, got %q", usersField.Inverse)
	}
	if usersField.Stored {
		t.Fatalf("expected user_ids to be non-stored relation metadata")
	}
}

func TestResPartnerPrimaryUserID(t *testing.T) {
	partner := ResPartnerRecord{UserIDs: []int64{44}}
	if got := partner.PrimaryUserID(); got == nil || *got != 44 {
		t.Fatalf("expected primary user id 44, got %#v", got)
	}

	partner.UserIDs = []int64{44, 45}
	if got := partner.PrimaryUserID(); got != nil {
		t.Fatalf("expected nil primary user id for multiple users, got %#v", got)
	}
}
