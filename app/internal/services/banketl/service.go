package banketl

// File overview:
// - Responsibility: encapsulates domain/service orchestration for service, keeping integrations and multi-step business flows out of transport handlers.
// - Key symbols: `NewService`, `error`, `NapasDTO`, `MBDTO`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Reads operational behavior from shared config so environments can tune the same code paths without forking implementation logic.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils"
)

type NapasDTO struct {
	CaseID              string `json:"caseId"`
	CreationDateTime    string `json:"creationDateTime"`
	ID                  string `json:"id"`
	TransDateTime       string `json:"transDateTime"`
	Amount              string `json:"amount"`
	IssueBank           string `json:"issueBank"`
	Status              string `json:"status"`
	BeneficiaryBank     string `json:"beneficiaryBank"`
	RealMerchantAccount string `json:"realMerchantAccount"`
	Mcc                 string `json:"mcc"`
	SourceAccount       string `json:"sourceAccount"`
	SystemTrace         string `json:"systemTrace"`
	LocalTime           string `json:"localTime"`
	LocalDate           string `json:"localDate"`
	TerminalID          string `json:"terminalId"`
	RefID               string `json:"refId"`
	CustomerName        string `json:"customerName,omitempty"`
}

type MBDTO struct {
	TransactionID      string `json:"transactionid"`
	TransactionTime    int    `json:"transactiontime"`
	ReferenceNumber    string `json:"referencenumber"`
	Amount             int    `json:"amount"`
	Content            string `json:"content"`
	BankAccount        string `json:"bankaccount"`
	TransactionType    string `json:"transType"`
	ValueDate          int    `json:"valueDate"`
	VirtualAccount     string `json:"va"`
	ReciprocalAccount  string `json:"reciprocalAccount"`
	ReciprocalBankCode string `json:"reciprocalBankCode"`
	BinCode            string `json:"bincode"`
}

type MBMessage struct {
	Event   string `json:"event"`
	Message string `json:"message"`
}

type ibftParticipantFields struct {
	bid            string
	proxyBid       string
	companyRefCode string
	merchantName   string
	fromAccNo      string
	fromAccName    string
	fromAccBincode string
	toAccAlias     string
	toAccNo        string
	toAccName      string
	toAccBincode   string
	toAccBankName  string
}

type ibftTransactionPayload struct {
	IsDebit              bool        `json:"is_debit"`
	TxnID                string      `json:"name"`
	BankTxnID            string      `json:"bank_txn_id"`
	NapasTxnID           string      `json:"napas_txn_id"`
	TxnDate              interface{} `json:"txn_date"`
	TxnDateTimeStamp     float32     `json:"txn_date_timestamp"`
	BankTxnDateTimeStamp float32     `json:"bank_date_timestamp"`
	TxnType              string      `json:"txn_type"`
	TxnStatus            string      `json:"txn_status"`
	TxnAmount            float32     `json:"txn_amount"`
	TxnFee               float32     `json:"txn_fee"`
	TxnCurrency          string      `json:"currency"`
	Narrative            string      `json:"narrative"`
	BillID               string      `json:"bill_id"`
	TerminalID           string      `json:"terminal_id"`
	ExRate               string      `json:"rate"`
	BID                  string      `json:"bid"`
	ProxyBID             string      `json:"proxy_bid"`
	CompanyRefCode       string      `json:"company_ref_code"`
	MerchantName         string      `json:"merchant_name"`
	FromAccNo            string      `json:"frm_acc_no"`
	FromAccName          string      `json:"frm_acc_name"`
	FromAccBincode       string      `json:"frm_acc_bincode"`
	FromAccBankName      string      `json:"frm_acc_bankname"`
	ToAccAlias           string      `json:"to_acc_va"`
	ToAccNo              string      `json:"to_acc_no"`
	ToAccName            string      `json:"to_acc_name"`
	ToAccBincode         string      `json:"to_acc_bincode"`
	ToAccBankName        string      `json:"to_acc_bankname"`
}

type Service struct{}

// NewService constructs and returns the service handled by this package.
func NewService() *Service {
	return &Service{}
}

// HandleNapasFlow keeps the topic-consumer path inside a service boundary so
// Kafka handlers no longer depend on route packages for business orchestration.
func (s *Service) HandleNapasFlow(ctx context.Context, input []byte) error {
	log.Infof("Napas ETL Flow executed with input: %s", input)

	napasDTO, err := parseNapasDTO(input)
	if err != nil {
		return err
	}

	bankAccountData, err := loadBankAccount(ctx, napasDTO.ID)
	if err != nil {
		log.Errorf("load bank account by alias %q: %v", napasDTO.ID, err)
		return fmt.Errorf("load bank account by alias %q: %w", napasDTO.ID, err)
	}
	if bankAccountData == nil {
		log.Errorf("load bank account by alias %q: no record found", napasDTO.ID)
		return fmt.Errorf("load bank account by alias %q: no record found", napasDTO.ID)
	}

	dto := napasIBFTDTO(napasDTO, bankAccountData)
	return s.dispatchCallback(ctx, bankAccountData, dto)
}

// HandleMBFlow owns the partner-specific message parsing and account-enrichment
// rules so the transport layer stays limited to message receipt and ack/nack.
func (s *Service) HandleMBFlow(ctx context.Context, input []byte) error {
	log.Infof("MB ETL Flow executed with input: %s", input)

	mbDTO, err := parseMBPayload(input)
	if err != nil {
		return err
	}

	bankAccountData, err := loadBankAccount(ctx, mbDTO.BankAccount)
	if err != nil {
		log.Errorf("load MB bank account: %v", err)
	}

	dto := mbIBFTDTO(mbDTO, bankAccountData)
	return s.dispatchCallback(ctx, bankAccountData, dto)
}

func parseNapasDTO(input []byte) (*NapasDTO, error) {
	var napasDTO *NapasDTO
	if err := json.Unmarshal(input, &napasDTO); err != nil {
		log.Errorf("parse napas payload: %v", err)
		return nil, fmt.Errorf("parse napas payload: %w", err)
	}
	if napasDTO == nil {
		log.Errorf("parse napas payload: empty payload")
		return nil, fmt.Errorf("parse napas payload: empty payload")
	}
	return napasDTO, nil
}

func parseMBPayload(input []byte) (MBDTO, error) {
	var mbMessage *MBMessage
	if err := json.Unmarshal(input, &mbMessage); err != nil {
		log.Errorf("parse mb envelope: %v", err)
		return MBDTO{}, fmt.Errorf("parse mb envelope: %w", err)
	}
	if mbMessage == nil {
		log.Errorf("parse mb envelope: empty payload")
		return MBDTO{}, fmt.Errorf("parse mb envelope: empty payload")
	}
	var mbDTO MBDTO
	if err := json.Unmarshal([]byte(mbMessage.Message), &mbDTO); err != nil {
		log.Errorf("parse mb payload: %v", err)
		return MBDTO{}, fmt.Errorf("parse mb payload: %w", err)
	}
	return mbDTO, nil
}

// loadBankAccount loads the bank account handled by this package.
func loadBankAccount(ctx context.Context, proxyValue string) (*models.BankDocument, error) {
	records, err := modelruntime.NewService().SearchRead(ctx, models.BankModelName, orm.RecordMap{
		"proxy_value": strings.TrimSpace(proxyValue),
		"__limit":     1,
	}, nil)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return bankDocumentFromRecord(records[0])
}

func bankDocumentFromRecord(record orm.RecordMap) (*models.BankDocument, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	var out models.BankDocument
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func napasIBFTDTO(napasDTO *NapasDTO, bankAccountData *models.BankDocument) ibftTransactionPayload {
	// Build the callback payload from one normalized field set so every Napas event keeps the same downstream contract.
	fields := ibftParticipantFields{
		bid:            bankAccountData.Bid,
		proxyBid:       bankAccountData.ProxyBid,
		companyRefCode: bankAccountData.CompanyRefCode,
		merchantName:   bankAccountData.ProxyName,
		fromAccNo:      napasDTO.CustomerName,
		fromAccName:    napasDTO.SourceAccount,
		fromAccBincode: napasDTO.IssueBank,
		toAccAlias:     napasDTO.ID,
		toAccNo:        napasDTO.RealMerchantAccount,
		toAccName:      bankAccountData.ProxyName,
		toAccBincode:   napasDTO.BeneficiaryBank,
		toAccBankName:  bankAccountData.AccHolderName,
	}
	return ibftTransactionPayload{
		IsDebit:              false,
		TxnID:                napasDTO.CaseID,
		BankTxnID:            napasDTO.RefID,
		NapasTxnID:           napasDTO.SystemTrace,
		TxnDate:              bankTransactionTimeString(napasDTO.TransDateTime),
		TxnDateTimeStamp:     float32(utils.ToTimestamp(napasDTO.TransDateTime)),
		BankTxnDateTimeStamp: float32(utils.ToTimestamp(napasDTO.TransDateTime)),
		TxnType:              "ibft",
		TxnStatus:            "done",
		TxnAmount:            float32(utils.Str2Int(napasDTO.Amount)),
		TxnFee:               0,
		TxnCurrency:          "704",
		Narrative:            "",
		BillID:               napasDTO.RefID,
		TerminalID:           napasDTO.TerminalID,
		ExRate:               "",
		BID:                  fields.bid,
		ProxyBID:             fields.proxyBid,
		CompanyRefCode:       fields.companyRefCode,
		MerchantName:         fields.merchantName,
		FromAccNo:            fields.fromAccNo,
		FromAccName:          fields.fromAccName,
		FromAccBincode:       fields.fromAccBincode,
		FromAccBankName:      "",
		ToAccAlias:           fields.toAccAlias,
		ToAccNo:              fields.toAccNo,
		ToAccName:            fields.toAccName,
		ToAccBincode:         fields.toAccBincode,
		ToAccBankName:        fields.toAccBankName,
	}
}

func mbIBFTDTO(mbDTO MBDTO, bankAccountData *models.BankDocument) ibftTransactionPayload {
	fields := mbParticipantFields(mbDTO, bankAccountData)
	return ibftTransactionPayload{
		IsDebit:              mbDTO.TransactionType == "D",
		TxnID:                mbDTO.TransactionID,
		BankTxnID:            mbDTO.TransactionID,
		NapasTxnID:           mbDTO.ReferenceNumber,
		TxnDate:              false,
		TxnDateTimeStamp:     float32(mbDTO.TransactionTime),
		BankTxnDateTimeStamp: float32(mbDTO.TransactionTime),
		TxnType:              "ibft",
		TxnStatus:            "done",
		TxnAmount:            float32(mbDTO.Amount),
		TxnFee:               0,
		TxnCurrency:          "704",
		Narrative:            mbDTO.Content,
		BillID:               "",
		TerminalID:           "",
		ExRate:               "",
		BID:                  fields.bid,
		ProxyBID:             fields.proxyBid,
		CompanyRefCode:       fields.companyRefCode,
		MerchantName:         fields.merchantName,
		FromAccNo:            fields.fromAccNo,
		FromAccName:          fields.fromAccName,
		FromAccBincode:       fields.fromAccBincode,
		FromAccBankName:      "",
		ToAccAlias:           fields.toAccAlias,
		ToAccNo:              fields.toAccNo,
		ToAccName:            fields.toAccName,
		ToAccBincode:         fields.toAccBincode,
		ToAccBankName:        "",
	}
}

func mbParticipantFields(mbDTO MBDTO, bankAccountData *models.BankDocument) ibftParticipantFields {
	// Keep the legacy MB debit/credit mapping in one helper so both branches stay easy to audit and future field additions cannot drift.
	fields := ibftParticipantFields{}
	if bankAccountData == nil {
		return fields
	}
	fields.bid = bankAccountData.Bid
	fields.proxyBid = bankAccountData.ProxyBid
	fields.companyRefCode = bankAccountData.CompanyRefCode
	fields.merchantName = bankAccountData.ProxyName
	if mbDTO.TransactionType != "D" {
		fields.fromAccBincode = bankAccountData.BankBic
		fields.fromAccName = bankAccountData.AccHolderName
		fields.fromAccNo = bankAccountData.AccNumber
		fields.toAccAlias = mbDTO.VirtualAccount
		fields.toAccNo = mbDTO.ReciprocalAccount
		fields.toAccBincode = mbDTO.ReciprocalBankCode
		return fields
	}
	fields.fromAccBincode = mbDTO.ReciprocalBankCode
	fields.fromAccNo = mbDTO.BankAccount
	fields.toAccAlias = bankAccountData.ProxyValue
	fields.toAccNo = bankAccountData.AccNumber
	fields.toAccName = bankAccountData.AccHolderName
	fields.toAccBincode = bankAccountData.BankBic
	return fields
}

// dispatchCallback handles partner callback delivery after bank payload normalization.
func (s *Service) dispatchCallback(_ context.Context, bankAccountData *models.BankDocument, dto ibftTransactionPayload) error {
	message, err := utils.StructToMap(dto)
	if err != nil {
		log.Errorf("convert dto to map: %v", err)
		return fmt.Errorf("convert dto to map: %w", err)
	}

	if bankAccountData != nil {
		callbackURL := selectCallbackURL(config.Get().AppEnv(), bankAccountData)
		if callbackURL != "" {
			log.Infof("POST callback URL: %s", callbackURL)
			utils.CallApi("POST", callbackURL, map[string]string{"Content-Type": "application/json"}, message)
		}
	}

	return nil
}

func selectCallbackURL(env string, bankAccountData *models.BankDocument) string {
	if bankAccountData == nil {
		return ""
	}

	var candidate string
	if env == "production" {
		candidate = strings.TrimSpace(bankAccountData.IpnURLProd)
	} else {
		candidate = strings.TrimSpace(bankAccountData.IpnURLDev)
	}

	if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
		return candidate
	}
	if strings.TrimSpace(candidate) != "" {
		log.Warnf("Invalid callback URL for env=%s: %s", env, candidate)
	}
	return ""
}

func bankTransactionTimeString(datetimeStr string) string {
	parsed, err := time.Parse(time.RFC3339, datetimeStr)
	// Keep malformed partner timestamps flowing to callbacks unchanged so a
	// formatting issue does not drop the whole bank notification.
	if err != nil {
		log.Errorf("bank transaction time parse failed: %v", err)
		return datetimeStr
	}
	return parsed.Format("2006-01-02 15:04:05")
}
