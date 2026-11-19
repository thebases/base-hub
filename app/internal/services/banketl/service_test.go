package banketl

// File overview:
// - Responsibility: verifies bank ETL orchestration and normalization behavior for service.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down ETL behavior so parser and enrichment changes do not silently alter downstream transaction processing.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/services/log"
)

func TestSelectCallbackURLPrefersMatchingEnvironmentAndValidScheme(t *testing.T) {
	bankAccount := &models.BankDocument{
		IpnURLDev:  "https://dev.example.com/ipn",
		IpnURLProd: "https://prod.example.com/ipn",
	}

	if got := selectCallbackURL("development", bankAccount); got != "https://dev.example.com/ipn" {
		t.Fatalf("expected development callback URL, got %q", got)
	}
	if got := selectCallbackURL("production", bankAccount); got != "https://prod.example.com/ipn" {
		t.Fatalf("expected production callback URL, got %q", got)
	}
}

func TestSelectCallbackURLRejectsInvalidURL(t *testing.T) {
	log.Init("/tmp/banketl-service-test.log")

	bankAccount := &models.BankDocument{
		IpnURLDev: "not-a-url",
	}

	if got := selectCallbackURL("development", bankAccount); got != "" {
		t.Fatalf("expected invalid callback URL to be rejected, got %q", got)
	}
}

func TestMBParticipantFieldsCreditTransactionUsesBankAccountAsSender(t *testing.T) {
	bankAccount := &models.BankDocument{
		Bid:            "bid-1",
		ProxyBid:       "proxy-1",
		CompanyRefCode: "company-1",
		ProxyName:      "Merchant A",
		BankBic:        "BANKVN",
		AccHolderName:  "Receiver A",
		AccNumber:      "123456",
		ProxyValue:     "alias-1",
	}
	dto := MBDTO{
		TransactionType:    "C",
		VirtualAccount:     "va-1",
		ReciprocalAccount:  "987654",
		ReciprocalBankCode: "OTHERBANK",
	}

	got := mbParticipantFields(dto, bankAccount)

	if got.bid != "bid-1" || got.proxyBid != "proxy-1" || got.companyRefCode != "company-1" || got.merchantName != "Merchant A" {
		t.Fatalf("expected bank account identity to be copied, got %+v", got)
	}
	if got.fromAccNo != "123456" || got.fromAccName != "Receiver A" || got.fromAccBincode != "BANKVN" {
		t.Fatalf("expected sender fields from bank account, got %+v", got)
	}
	if got.toAccAlias != "va-1" || got.toAccNo != "987654" || got.toAccBincode != "OTHERBANK" {
		t.Fatalf("expected receiver fields from MB payload, got %+v", got)
	}
	if got.toAccName != "" || got.toAccBankName != "" {
		t.Fatalf("expected unset optional receiver labels for credit mapping, got %+v", got)
	}
}

func TestMBParticipantFieldsDebitTransactionUsesBankAccountAsReceiver(t *testing.T) {
	bankAccount := &models.BankDocument{
		Bid:            "bid-2",
		ProxyBid:       "proxy-2",
		CompanyRefCode: "company-2",
		ProxyName:      "Merchant B",
		BankBic:        "BANKB",
		AccHolderName:  "Receiver B",
		AccNumber:      "222333",
		ProxyValue:     "alias-2",
	}
	dto := MBDTO{
		TransactionType:    "D",
		BankAccount:        "source-1",
		ReciprocalBankCode: "SOURCEBANK",
	}

	got := mbParticipantFields(dto, bankAccount)

	if got.fromAccNo != "source-1" || got.fromAccBincode != "SOURCEBANK" {
		t.Fatalf("expected sender fields from MB payload, got %+v", got)
	}
	if got.toAccAlias != "alias-2" || got.toAccNo != "222333" || got.toAccName != "Receiver B" || got.toAccBincode != "BANKB" {
		t.Fatalf("expected receiver fields from bank account, got %+v", got)
	}
}

func TestMBParticipantFieldsWithoutBankAccountKeepsZeroValues(t *testing.T) {
	dto := MBDTO{TransactionType: "C"}

	got := mbParticipantFields(dto, nil)

	if got != (ibftParticipantFields{}) {
		t.Fatalf("expected zero-value participant fields when bank account is missing, got %+v", got)
	}
}
