package model

import "strings"

const RuleVersion = "conservative-1.0.0"

type Row struct {
	ID          string `json:"id"`
	SheetIndex  int    `json:"sheet_index"`
	Sheet       string `json:"sheet"`
	SourceRow   int    `json:"source_row"`
	RawJSON     string `json:"raw_json"`
	Invoice     string `json:"invoice"`
	Stock       string `json:"stock"`
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	Time        string `json:"time"`
	Price       string `json:"price"`
	Customer    string `json:"customer"`
	Country     string `json:"country"`
	Kind        string `json:"kind"`
	Excluded    string `json:"excluded"`
	Flags       string `json:"flags"`
	DuplicateOf string `json:"duplicate_of"`
}

func (r Row) Duplicate() bool   { return strings.Contains("|"+r.Flags+"|", "|same_sheet_duplicate|") }
func (r Row) HasIdentity() bool { return r.Customer != "" && r.Country != "" && r.Stock != "" }

type Evidence struct {
	SaleID    string `json:"sale_id"`
	Remaining int64  `json:"remaining_at_decision"`
	Eligible  bool   `json:"eligible"`
	Reason    string `json:"reason"`
}

type Decision struct {
	CancelID            string     `json:"cancel_id"`
	SaleID              string     `json:"sale_id,omitempty"`
	Quantity            int64      `json:"quantity"`
	Amount              string     `json:"exact_amount,omitempty"`
	Classification      string     `json:"classification"`
	Reason              string     `json:"reason"`
	CandidateLowerBound int        `json:"candidate_count_lower_bound"`
	Evidence            []Evidence `json:"evidence"`
}

type Balance struct {
	SaleID    string `json:"sale_id"`
	Original  int64  `json:"original"`
	Allocated int64  `json:"allocated"`
	Remaining int64  `json:"remaining"`
}

type Result struct {
	RuleVersion string     `json:"rule_version"`
	Decisions   []Decision `json:"decisions"`
	Balances    []Balance  `json:"balances"`
	Matched     int        `json:"matched"`
	Suspected   int        `json:"suspected"`
	NoOriginal  int        `json:"no_original"`
	Workers     int        `json:"workers"`
}
