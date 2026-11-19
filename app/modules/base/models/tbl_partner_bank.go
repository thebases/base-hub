package models

// File overview:
// - Responsibility: defines the `base` module model contract for res partner bank, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `BankModel`, `BankInputData`, `BankDto`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import "git.thebase.vn/base_hub/internal/orm"

type BankInputData struct {
	Action         string      `json:"_action"`
	ID             int         `json:"_id"`
	Model          string      `json:"_model"`
	AccHolderName  string      `json:"acc_holder_name"`
	AccNumber      string      `json:"acc_number"`
	AccType        string      `json:"acc_type"`
	Active         bool        `json:"active"`
	BankBic        string      `json:"bank_bic"`
	CompanyRefCode string      `json:"company_ref_code"`
	ID0            int         `json:"id"`
	IpnURLDev      interface{} `json:"ipn_url_dev"`
	IpnURLProd     interface{} `json:"ipn_url_prod"`
	IsMainAccount  bool        `json:"is_main_account"`
	ProxyName      string      `json:"proxy_name"`
	ProxyType      string      `json:"proxy_type"`
	ProxyValue     string      `json:"proxy_value"`
	ProxyBid       string      `json:"proxy_bid"`
	Tid            bool        `json:"tid"`
	Bid            string      `json:"bid"`
	Realm          string      `json:"realm"`
}

type BankDto struct {
	AccHolderName  string      `json:"acc_holder_name"`
	AccNumber      string      `json:"acc_number"`
	AccType        string      `json:"acc_type"`
	Active         bool        `json:"active"`
	BankBic        string      `json:"bank_bic"`
	CompanyRefCode string      `json:"company_ref_code"`
	IpnURLDev      interface{} `json:"ipn_url_dev"`
	IpnURLProd     interface{} `json:"ipn_url_prod"`
	IsMainAccount  bool        `json:"is_main_account"`
	ProxyName      string      `json:"proxy_name"`
	ProxyType      string      `json:"proxy_type"`
	ProxyValue     string      `json:"proxy_value"`
	ProxyBid       string      `json:"proxy_bid"`
	Tid            bool        `json:"tid"`
	Bid            string      `json:"bid"`
	Realm          string      `json:"realm"`
}
type BankDocument struct {
	orm.BaseModel
	Name           string `bun:"name" json:"name" meta:"translate,label:Name,help:Stores the name value for this record."`
	PartnerID      *int64 `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:tbl.partner,ondelete:set null,index,label:Partner ID,help:Links this record to the related tbl.partner record for partner id."`
	AccHolderName  string `bun:"acc_holder_name" json:"acc_holder_name" meta:"label:Acc Holder Name,help:Stores the acc holder name value for this record."`
	AccNumber      string `bun:"acc_number" json:"acc_number" meta:"label:Acc Number,help:Stores the acc number value for this record."`
	AccType        string `bun:"acc_type" json:"acc_type" meta:"label:Acc Type,help:Stores the acc type value for this record."`
	BankBic        string `bun:"bank_bic" json:"bank_bic" meta:"filter:bic,label:Bank BIC,help:Stores the bank bic value for this record."`
	CompanyRefCode string `bun:"company_ref_code" json:"company_ref_code" meta:"label:Company Ref Code,help:Stores the company ref code value for this record."`
	IpnURLDev      string `bun:"ipn_url_dev" json:"ipn_url_dev" meta:"label:Ipn URL Dev,help:Stores the ipn url dev value for this record."`
	IpnURLProd     string `bun:"ipn_url_prod" json:"ipn_url_prod"  meta:"label:Ipn URL Prod,help:Stores the ipn url prod value for this record."`
	IsMainAccount  bool   `bun:"is_main_account" json:"is_main_account" meta:"label:Is Main Account,help:Controls whether is main account is enabled for this record."`
	ProxyName      string `bun:"proxy_name" json:"proxy_name" meta:"label:Proxy Name,help:Stores the proxy name value for this record."`
	ProxyType      string `bun:"proxy_type" json:"proxy_type" meta:"label:Proxy Type,help:Stores the proxy type value for this record."`
	ProxyValue     string `bun:"proxy_value" json:"proxy_value" meta:"filter,index,label:Proxy Value,help:Stores the proxy value value for this record."`
	ProxyBid       string `bun:"proxy_bid" json:"proxy_bid" meta:"label:Proxy Bid,help:Stores the proxy bid value for this record."`
	Tid            string `bun:"tid" json:"tid" meta:"label:Tid,help:Stores the tid value for this record."`
	Bid            string `bun:"bid" json:"bid" meta:"label:Bid,help:Stores the bid value for this record."`
	Realm          string `bun:"realm" json:"realm" meta:"label:Realm,help:Stores the realm value for this record."`
}

var BankModelName = "tbl.partner.bank"

var BankDefinition = orm.ModelDefinition(BankModelName, (*BankDocument)(nil))

var BankModelSpec = orm.ModelSpec{
	Name:        BankModelName,
	Label:       "Bank Account",
	Description: "Partner bank account and proxy metadata",
	Definition:  BankDefinition,
	// Physical storage uses the tbl_* prefix while the logical model name stays
	// dotted (tbl.partner.bank); the legacy tbl_partner_bank table is renamed in
	// place during migration so account rows survive.
}

// BankModel should compose future bank-specific behavior from small helpers and
// handler options so account invariants stay isolated and testable.
var BankModel = orm.Define[BankDocument](&Models, 80, BankModelSpec)
