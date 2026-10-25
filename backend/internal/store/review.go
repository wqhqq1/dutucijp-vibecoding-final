package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"returnsrecon/internal/access"
	"returnsrecon/internal/model"
)

const ReviewPolicyVersion = "review-1.0.0"

func newReviewID(seed string) (string, error) {
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return "", e
	}
	return ID(seed + "|" + fmt.Sprintf("%x", nonce)), nil
}

type DuplicateMember struct {
	SaleID    string `json:"sale_id"`
	Sheet     string `json:"sheet"`
	SourceRow int    `json:"source_row"`
}
type DuplicateProposal struct {
	ID          string            `json:"id"`
	CanonicalID string            `json:"canonical_id"`
	Capacity    int64             `json:"capacity"`
	Allocated   int64             `json:"allocated"`
	Remaining   int64             `json:"remaining"`
	Fingerprint string            `json:"fingerprint"`
	Members     []DuplicateMember `json:"members"`
}
type ReviewOption struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	SaleID      string             `json:"sale_id"`
	Invoice     string             `json:"invoice"`
	Time        string             `json:"time"`
	GapSeconds  int64              `json:"gap_seconds"`
	Remaining   int64              `json:"remaining"`
	Duplicate   *DuplicateProposal `json:"duplicate,omitempty"`
	BasketID    string             `json:"basket_id,omitempty"`
	BasketCases []string           `json:"basket_cases,omitempty"`
	Warning     string             `json:"warning,omitempty"`
}
type ReviewItem struct {
	Case          WorkflowCase   `json:"case"`
	Cancel        model.Row      `json:"cancel"`
	Options       []ReviewOption `json:"options"`
	ChoiceID      string         `json:"choice_id"`
	HasAllocation bool           `json:"has_allocation"`
	Notice        string         `json:"notice,omitempty"`
}
type ReviewSet struct {
	RemediationRevision int `json:"remediation_revision"`

	ScopeVersion  int            `json:"scope_version,omitempty"`
	ID            string         `json:"id"`
	RunID         string         `json:"run_id"`
	Actor         WorkflowActor  `json:"actor"`
	PolicyVersion string         `json:"policy_version"`
	CreatedAt     string         `json:"created_at"`
	Items         []ReviewItem   `json:"items"`
	Counts        map[string]int `json:"counts"`
}
type ReviewPage struct {
	SetID         string         `json:"set_id"`
	PolicyVersion string         `json:"policy_version"`
	Items         []ReviewItem   `json:"items"`
	Total         int            `json:"total"`
	Page          int            `json:"page"`
	PageSize      int            `json:"page_size"`
	Counts        map[string]int `json:"counts"`
	// ReadOnly counts closed items that match the filter but are not listed.
	ReadOnly int `json:"readonly"`
}
type ReviewGroup struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Count       int    `json:"count"`
	Recommended int    `json:"recommended"`
}
type reviewSale struct {
	model.Row
	Original, Allocated, Remaining int64
	Duplicate                      *DuplicateProposal
}

// rawFingerprint mirrors CR-05: preserve cell types/text and canonicalize only
// numeric cells. Invalid legacy fixtures are not evidence of a duplicate.
func rawFingerprint(raw string) (string, error) {
	var cells []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &cells); err != nil || len(cells) != 8 {
		return "", fmt.Errorf("invalid eight-field source")
	}
	normalized := make([]any, 8)
	for i, cell := range cells {
		if string(cell) == "null" {
			continue
		}
		var pair []string
		if err := json.Unmarshal(cell, &pair); err != nil || len(pair) != 2 {
			return "", fmt.Errorf("invalid source cell")
		}
		if pair[0] == "n" {
			v, e := decimal.NewFromString(pair[1])
			if e != nil {
				return "", e
			}
			pair[1] = v.String()
		}
		normalized[i] = pair
	}
	b, e := json.Marshal(normalized)
	return string(b), e
}

func sourceLess(a, b model.Row) bool {
	if a.SheetIndex != b.SheetIndex {
		return a.SheetIndex < b.SheetIndex
	}
	if a.SourceRow != b.SourceRow {
		return a.SourceRow < b.SourceRow
	}
	return a.ID < b.ID
}
func reviewKey(r model.Row) string {
	p, e := decimal.NewFromString(r.Price)
	if e != nil {
		return ""
	}
	b, _ := json.Marshal([]string{r.Customer, r.Stock, p.String(), r.Country})
	return string(b)
}
func saleResource(o ReviewOption) string {
	if o.Duplicate != nil {
		return "duplicate:" + o.Duplicate.ID
	}
	return "sale:" + o.SaleID
}
func optionFor(kind string, s *reviewSale, c model.Row) ReviewOption {
	ct, _ := time.Parse("2006-01-02T15:04:05.000000", c.Time)
	st, _ := time.Parse("2006-01-02T15:04:05.000000", s.Time)
	o := ReviewOption{Kind: kind, SaleID: s.ID, Invoice: s.Invoice, Time: s.Time, GapSeconds: int64(ct.Sub(st) / time.Second), Remaining: s.Remaining, Duplicate: s.Duplicate}
	if s.Duplicate != nil {
		o.SaleID = s.Duplicate.CanonicalID
		o.Remaining = s.Duplicate.Remaining
	}
	o.ID = ID(kind + "|" + o.SaleID)
	return o
}

// GenerateReviewSet takes one consistent read snapshot. Suggestions never write
// allocations or reserve balances; only the finished, actor-scoped set is saved.
func (s *Store) GenerateReviewSet(ctx context.Context, actor WorkflowActor, run string) (out ReviewSet, err error) {
	if !actor.valid() {
		return out, workflowError("unauthenticated", "需要可信身份", WorkflowCase{})
	}
	conn, e := s.DB.Conn(ctx)
	if e != nil {
		return out, e
	}
	defer conn.Close()
	if _, e = conn.ExecContext(ctx, "BEGIN"); e != nil {
		return out, e
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	var dataset string
	if e = conn.QueryRowContext(ctx, "SELECT r.dataset_id FROM runs r JOIN datasets d ON d.id=r.dataset_id WHERE r.id=? AND r.status='published' AND d.status='published'", run).Scan(&dataset); e != nil {
		return out, workflowError("not_found", "运行尚未发布", WorkflowCase{})
	}
	out = ReviewSet{ScopeVersion: access.Version(ctx), RunID: run, Actor: actor, PolicyVersion: ReviewPolicyVersion, CreatedAt: time.Now().Format(time.RFC3339Nano), Items: []ReviewItem{}, Counts: map[string]int{}}
	out.RemediationRevision, _, e = remediationRevision(ctx, conn, run)
	if e != nil {
		return out, e
	}
	out.ID, e = newReviewID(run + "|" + actor.ID + "|" + out.CreatedAt)
	if e != nil {
		return out, e
	}
	cancels := map[string]model.Row{}
	invoices := map[string][]model.Row{}
	rows, e := conn.QueryContext(ctx, businessSQL(ctx, conn, run, `SELECT n.id,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,s.sheet,s.sheet_index,s.source_row FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id WHERE n.dataset_id=? AND (n.kind='cancel' OR upper(substr(n.invoice,1,1))='C')`), dataset)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r model.Row
		if e = rows.Scan(&r.ID, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.Sheet, &r.SheetIndex, &r.SourceRow); e != nil {
			rows.Close()
			return out, e
		}
		cancels[r.ID] = r
		invoices[r.Invoice] = append(invoices[r.Invoice], r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	caseQuery := `SELECT c.id,c.run_id,r.dataset_id,c.cancel_id,c.reason,c.status,coalesce(c.assignee,''),c.version,c.conclusion,c.assigned_at,c.assigned_by,d.source_hash,s.sheet,s.source_row,r.rule_version,d.cleaning_version,c.generation,c.is_current,c.previous_case_id,c.outcome,c.waiting,c.missing_materials,c.next_step FROM cases c JOIN runs r ON r.id=c.run_id JOIN datasets d ON d.id=r.dataset_id JOIN normalized_rows n ON n.dataset_id=d.id AND n.id=c.cancel_id JOIN source_rows s ON s.id=n.source_id WHERE c.run_id=? AND c.is_current=1`
	args := []any{run}
	if actor.Role == "warehouse" {
		caseQuery += " AND c.assignee=?"
		args = append(args, actor.ID)
	}
	rows, e = conn.QueryContext(ctx, businessSQL(ctx, conn, run, caseQuery), args...)
	if e != nil {
		return out, e
	}
	itemByCancel := map[string]int{}
	for rows.Next() {
		var c WorkflowCase
		e = rows.Scan(&c.ID, &c.RunID, &c.DatasetID, &c.CancelID, &c.Reason, &c.Status, &c.Assignee, &c.Version, &c.Conclusion, &c.AssignedAt, &c.AssignedBy, &c.SourceHash, &c.Sheet, &c.SourceRow, &c.RuleVersion, &c.CleaningVersion, &c.Generation, &c.Current, &c.PreviousCaseID, &c.Outcome, &c.Waiting, &c.MissingMaterials, &c.NextStep)
		if e != nil {
			rows.Close()
			return out, e
		}
		c.Country = cancels[c.CancelID].Country
		if !access.Allows(ctx, c.Country) {
			continue
		}
		itemByCancel[c.CancelID] = len(out.Items)
		out.Items = append(out.Items, ReviewItem{Case: c, Cancel: cancels[c.CancelID], Options: []ReviewOption{}})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	allocations := map[string]string{}
	automatic := map[string]string{}
	rows, e = conn.QueryContext(ctx, "SELECT cancel_id,sale_id,method FROM allocations WHERE run_id=?", run)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var cid, sid, method string
		if e = rows.Scan(&cid, &sid, &method); e != nil {
			rows.Close()
			return out, e
		}
		allocations[cid] = sid
		if method == "automatic" {
			automatic[cid] = sid
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	sales := map[string]*reviewSale{}
	byKey := map[string][]*reviewSale{}
	fingerprints := map[string][]*reviewSale{}
	independent := map[string]bool{}
	verifiedRows, ve := conn.QueryContext(ctx, "SELECT row_id FROM duplicate_verifications WHERE run_id=? AND disposition='independent'", run)
	if ve != nil {
		return out, ve
	}
	for verifiedRows.Next() {
		var id string
		if ve = verifiedRows.Scan(&id); ve != nil {
			verifiedRows.Close()
			return out, ve
		}
		independent[id] = true
	}
	ve = verifiedRows.Err()
	verifiedRows.Close()
	if ve != nil {
		return out, ve
	}
	rows, e = conn.QueryContext(ctx, businessSQL(ctx, conn, run, `WITH keys AS (SELECT DISTINCT customer,stock,country FROM normalized_rows INDEXED BY row_lookup WHERE dataset_id=? AND kind='cancel' AND excluded='') SELECT n.id,n.invoice,n.stock,n.description,n.quantity,n.source_time,n.price,n.customer,n.country,n.kind,n.excluded,n.flags,s.sheet,s.sheet_index,s.source_row,s.raw_json,b.original,b.allocated,b.remaining FROM keys k CROSS JOIN normalized_rows n INDEXED BY row_identity ON k.customer=n.customer AND k.stock=n.stock AND k.country=n.country CROSS JOIN source_rows s ON s.id=n.source_id CROSS JOIN sale_balances b ON b.run_id=? AND b.sale_id=n.id WHERE n.dataset_id=? AND n.kind='sale' AND n.excluded=''`), dataset, run, dataset)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		r := &reviewSale{}
		var raw string
		e = rows.Scan(&r.ID, &r.Invoice, &r.Stock, &r.Description, &r.Quantity, &r.Time, &r.Price, &r.Customer, &r.Country, &r.Kind, &r.Excluded, &r.Flags, &r.Sheet, &r.SheetIndex, &r.SourceRow, &raw, &r.Original, &r.Allocated, &r.Remaining)
		if e != nil {
			rows.Close()
			return out, e
		}
		sales[r.ID] = r
		byKey[reviewKey(r.Row)] = append(byKey[reviewKey(r.Row)], r)
		if fp, fe := rawFingerprint(raw); fe == nil && !independent[r.ID] {
			fingerprints[fp] = append(fingerprints[fp], r)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for fp, group := range fingerprints {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return sourceLess(group[i].Row, group[j].Row) })
		d := &DuplicateProposal{ID: ID(fp), CanonicalID: group[0].ID, Capacity: group[0].Quantity, Fingerprint: fp, Members: []DuplicateMember{}}
		for _, r := range group {
			d.Allocated += r.Allocated
			d.Members = append(d.Members, DuplicateMember{r.ID, r.Sheet, r.SourceRow})
		}
		d.Remaining = d.Capacity - d.Allocated
		for _, r := range group {
			r.Duplicate = d
		}
	}
	// Explicitly confirmed groups, including supplemental sources, override raw proposals.
	confirmed := map[string]*DuplicateProposal{}
	groupRows, ge := conn.QueryContext(ctx, "SELECT g.id,g.canonical_id,g.capacity,m.sale_id FROM duplicate_sale_groups g JOIN duplicate_sale_members m ON m.run_id=g.run_id AND m.group_id=g.id WHERE g.run_id=? ORDER BY g.id,m.sale_id", run)
	if ge != nil {
		return out, ge
	}
	for groupRows.Next() {
		var gid, canonical, sid string
		var cap int64
		if ge = groupRows.Scan(&gid, &canonical, &cap, &sid); ge != nil {
			groupRows.Close()
			return out, ge
		}
		d := confirmed[gid]
		if d == nil {
			d = &DuplicateProposal{ID: gid, CanonicalID: canonical, Capacity: cap, Members: []DuplicateMember{}}
			confirmed[gid] = d
		}
		if sale := sales[sid]; sale != nil {
			d.Allocated += sale.Allocated
			d.Members = append(d.Members, DuplicateMember{sid, sale.Sheet, sale.SourceRow})
			sale.Duplicate = d
		}
	}
	ge = groupRows.Err()
	groupRows.Close()
	if ge != nil {
		return out, ge
	}
	for _, d := range confirmed {
		d.Remaining = d.Capacity - d.Allocated
	}
	choices := map[string][]*reviewSale{}
	for i := range out.Items {
		it := &out.Items[i]
		c := it.Cancel
		it.HasAllocation = allocations[c.ID] != ""
		if it.HasAllocation || it.Case.Status == "closed" {
			continue
		}
		if !c.HasIdentity() || c.Duplicate() {
			it.Notice = "缺身份或疑似重复退货，需补充证据核查"
			continue
		}
		seen := map[string]bool{}
		for _, sale := range byKey[reviewKey(c)] {
			q, _, ok := manualEligible(c, sale.Row)
			remaining := sale.Remaining
			if sale.Duplicate != nil {
				remaining = sale.Duplicate.Remaining
			}
			if !ok || remaining < q {
				continue
			}
			resource := sale.ID
			if sale.Duplicate != nil {
				resource = sale.Duplicate.ID
			}
			if seen[resource] {
				continue
			}
			seen[resource] = true
			choices[c.ID] = append(choices[c.ID], sale)
		}
		cs := choices[c.ID]
		sort.Slice(cs, func(a, b int) bool {
			if cs[a].Time != cs[b].Time {
				return cs[a].Time > cs[b].Time
			}
			return sourceLess(cs[a].Row, cs[b].Row)
		})
		if len(cs) == 1 {
			kind := "unique"
			if cs[0].Duplicate != nil {
				kind = "duplicate"
			}
			it.Options = append(it.Options, optionFor(kind, cs[0], c))
			it.ChoiceID = it.Options[0].ID
		} else if len(cs) > 1 {
			latest := cs[0].Time
			for _, sale := range cs {
				if sale.Time != latest {
					break
				}
				o := optionFor("nearest", sale, c)
				o.Warning = "最近时间仅供推荐，未证明真实原单"
				it.Options = append(it.Options, o)
			}
			if len(it.Options) == 1 {
				it.ChoiceID = it.Options[0].ID
			} else {
				it.Notice = "最近时间并列，请明确选择候选"
			}
		}
	}
	// Full invoice gates include excluded C rows. Pending suggestions are never anchors.
	for inv, lines := range invoices {
		if strings.TrimSpace(inv) == "" || len(lines) < 2 {
			continue
		}
		valid := true
		stocks := map[string]bool{}
		stockPrices := map[string]bool{}
		common := map[string]bool{}
		lineChoices := map[string][]string{}
		pending := []int{}
		for li, c := range lines {
			if c.Excluded != "" || c.Kind != "cancel" || c.Quantity >= 0 || !c.HasIdentity() || c.Duplicate() || c.Customer != lines[0].Customer || c.Country != lines[0].Country || c.Time != lines[0].Time {
				valid = false
				break
			}
			stocks[c.Stock] = true
			key := c.Stock + "|" + c.Price
			if stockPrices[key] {
				valid = false
				break
			}
			stockPrices[key] = true
			ids := []string{}
			if sid := automatic[c.ID]; sid != "" {
				ids = append(ids, sid)
			} else {
				idx, ok := itemByCancel[c.ID]
				if !ok || out.Items[idx].HasAllocation || out.Items[idx].Case.Status == "closed" || out.Items[idx].Case.Reason != "ambiguous_candidates" {
					valid = false
					break
				}
				pending = append(pending, idx)
				for _, sale := range byKey[reviewKey(c)] {
					q, _, ok := manualEligible(c, sale.Row)
					if ok && sale.Remaining >= q {
						ids = append(ids, sale.ID)
					}
				}
			}
			rowInv := map[string]bool{}
			for _, sid := range ids {
				sale := sales[sid]
				if sale == nil || strings.TrimSpace(sale.Invoice) == "" {
					valid = false
					break
				}
				rowInv[sale.Invoice] = true
			}
			if !valid {
				break
			}
			lineChoices[c.ID] = ids
			if li == 0 {
				common = rowInv
			} else {
				for x := range common {
					if !rowInv[x] {
						delete(common, x)
					}
				}
			}
		}
		if !valid || len(stocks) < 2 || len(pending) == 0 || len(common) != 1 {
			continue
		}
		var target string
		for x := range common {
			target = x
		}
		selected := map[string]*reviewSale{}
		saleIdentity := ""
		for _, c := range lines {
			var picked *reviewSale
			count := 0
			for _, sid := range lineChoices[c.ID] {
				sale := sales[sid]
				if sale.Invoice == target {
					count++
					picked = sale
				}
			}
			if count != 1 || picked.Duplicate != nil || picked.Row.Duplicate() {
				valid = false
				break
			}
			identity := picked.Customer + "|" + picked.Country + "|" + picked.Time
			if saleIdentity != "" && identity != saleIdentity {
				valid = false
				break
			}
			saleIdentity = identity
			selected[c.ID] = picked
		}
		if !valid {
			continue
		}
		caseIDs := []string{}
		for _, idx := range pending {
			caseIDs = append(caseIDs, out.Items[idx].Case.ID)
		}
		sort.Strings(caseIDs)
		for _, idx := range pending {
			it := &out.Items[idx]
			o := optionFor("basket", selected[it.Cancel.ID], it.Cancel)
			o.BasketID = ID(run + "|basket|" + inv)
			o.BasketCases = caseIDs
			o.Warning = "共同原销售单仍属规则推断；合并退货可能产生不同解释"
			if it.ChoiceID != "" {
				for _, alt := range it.Options {
					if alt.ID == it.ChoiceID && alt.SaleID != o.SaleID {
						o.Warning += "；与最近一笔推荐不同"
					}
				}
			}
			it.Options = append(it.Options, o)
			if len(choices[it.Cancel.ID]) > 1 {
				it.ChoiceID = o.ID
			}
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return sourceLess(out.Items[i].Cancel, out.Items[j].Cancel) })
	for _, it := range out.Items {
		if it.ChoiceID != "" {
			out.Counts["recommended"]++
			for _, o := range it.Options {
				if o.ID == it.ChoiceID {
					out.Counts[o.Kind]++
				}
			}
		} else if len(it.Options) > 0 {
			out.Counts["tied"]++
		}
		if it.Case.Status == "closed" {
			out.Counts["closed"]++
		}
		out.Counts["total"]++
	}
	if _, e = conn.ExecContext(ctx, "COMMIT"); e != nil {
		return out, e
	}
	// Release the read connection before acquiring a short write transaction.
	// Four concurrent previews must not exhaust the four-connection pool.
	if e = conn.Close(); e != nil {
		return out, e
	}
	body, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	err = s.immediate(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, "INSERT INTO review_sets(id,run_id,actor,policy_version,created_at,body_json) VALUES(?,?,?,?,?,?)", out.ID, run, actor.ID, ReviewPolicyVersion, out.CreatedAt, string(body))
		return e
	})
	return
}

func (s *Store) reviewSet(ctx context.Context, actor WorkflowActor, id string) (set ReviewSet, err error) {
	var body string
	err = s.DB.QueryRowContext(ctx, "SELECT body_json FROM review_sets WHERE id=? AND actor=?", id, actor.ID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return set, workflowError("not_found", "推荐快照不存在", WorkflowCase{})
	}
	if err != nil {
		return
	}
	err = json.Unmarshal([]byte(body), &set)
	if err != nil {
		return ReviewSet{}, err
	}
	revision, _, e := remediationRevision(ctx, s.DB, set.RunID)
	if e != nil {
		return ReviewSet{}, e
	}
	if revision != set.RemediationRevision {
		return ReviewSet{}, workflowError("materials_changed", "补充资料已变化，请重新生成推荐", WorkflowCase{})
	}
	if !access.Compatible(ctx, set.ScopeVersion) {
		return ReviewSet{}, workflowError("scope_changed", "负责国家/地区已调整，请重新生成推荐", WorkflowCase{})
	}
	if !actor.valid() || set.Actor != actor {
		return ReviewSet{}, workflowError("forbidden", "推荐快照不属于当前身份", WorkflowCase{})
	}
	return
}

// reviewMatches limits the batch workbench to items that can still change.
// Closed items stay readable in the case list; they are never listed,
// grouped or selected here, so a batch never contains them.
func reviewMatches(it ReviewItem, f QueryFilter, onlyRecommended bool) bool {
	return it.Case.Status != "closed" && reviewFilterMatches(it, f, onlyRecommended)
}
func reviewFilterMatches(it ReviewItem, f QueryFilter, onlyRecommended bool) bool {
	c, r := it.Case, it.Cancel
	if f.Kind != "" && f.Kind != "cancel" {
		return false
	}
	if f.Classification != "" {
		class := "suspected"
		if c.Reason == "no_identity_match" || c.Reason == "no_strictly_earlier_sale" || c.Reason == "insufficient_remaining_quantity" {
			class = "no_original"
		}
		if f.Classification != class {
			return false
		}
	}
	if f.ReviewGroupBy != "" && reviewGroupKey(it, f.ReviewGroupBy) != f.ReviewGroupKey {
		return false
	}
	if onlyRecommended && len(it.Options) == 0 {
		return false
	}
	return (f.Waiting == "" || (f.Waiting == "1") == c.Waiting) && (f.Status == "" || f.Status == c.Status) && (f.Reason == "" || f.Reason == c.Reason) && (f.Assignee == "" || f.Assignee == c.Assignee) && (f.Customer == "" || f.Customer == r.Customer) && (f.Country == "" || f.Country == r.Country) && (f.Stock == "" || f.Stock == r.Stock) && (f.Invoice == "" || f.Invoice == r.Invoice) && (f.DateFrom == "" || r.Time >= f.DateFrom) && (f.DateTo == "" || r.Time < f.DateTo)
}
func (s *Store) ListReview(ctx context.Context, a WorkflowActor, id string, f QueryFilter, recommended bool) (out ReviewPage, err error) {
	set, e := s.reviewSet(ctx, a, id)
	if e != nil {
		return out, e
	}
	if f.RunID != "" && f.RunID != set.RunID {
		return out, workflowError("invalid_input", "运行与快照不一致", WorkflowCase{})
	}
	f.RunID = set.RunID
	f, e = s.validateFilter(ctx, a, f)
	if e != nil {
		return out, e
	}
	if f.ReviewGroupBy != "" && !validReviewGrouping(f.ReviewGroupBy) {
		return out, workflowError("invalid_input", "未知分组", WorkflowCase{})
	}
	sort.SliceStable(set.Items, func(i, j int) bool {
		a, b := set.Items[i].Cancel, set.Items[j].Cancel
		var av, bv string
		switch f.Sort {
		case "date":
			av, bv = a.Time, b.Time
		case "invoice":
			av, bv = a.Invoice, b.Invoice
		case "customer":
			av, bv = a.Customer, b.Customer
		case "stock":
			av, bv = a.Stock, b.Stock
		case "country":
			av, bv = a.Country, b.Country
		}
		less := sourceLess(a, b)
		if av != bv {
			less = av < bv
		}
		if f.Direction == "desc" {
			return !less && a.ID != b.ID
		}
		return less
	})
	out = ReviewPage{SetID: id, PolicyVersion: set.PolicyVersion, Items: []ReviewItem{}, Page: f.Page, PageSize: f.PageSize, Counts: set.Counts}
	for _, it := range set.Items {
		if !reviewMatches(it, f, recommended) {
			if it.Case.Status == "closed" && reviewFilterMatches(it, f, recommended) {
				out.ReadOnly++
			}
			continue
		}
		if out.Total >= (f.Page-1)*f.PageSize && len(out.Items) < f.PageSize {
			out.Items = append(out.Items, it)
		}
		out.Total++
	}
	return out, nil
}
func (s *Store) ReviewGroups(ctx context.Context, a WorkflowActor, id, by string, f QueryFilter, recommended bool) ([]ReviewGroup, error) {
	if !validReviewGrouping(by) {
		return nil, workflowError("invalid_input", "未知分组", WorkflowCase{})
	}
	set, e := s.reviewSet(ctx, a, id)
	if e != nil {
		return nil, e
	}
	f.RunID = set.RunID
	if f, e = s.validateFilter(ctx, a, f); e != nil {
		return nil, e
	}
	m := map[string]*ReviewGroup{}
	for _, it := range set.Items {
		if !reviewMatches(it, f, recommended) {
			continue
		}
		var key, label string
		switch by {
		case "invoice":
			key = it.Cancel.Invoice
			label = key
		case "customer":
			b, _ := json.Marshal([]string{it.Cancel.Customer, it.Cancel.Country})
			key = string(b)
			label = it.Cancel.Customer + " · " + it.Cancel.Country
		case "reason":
			key = it.Case.Reason
			label = key
		case "stock":
			key = it.Cancel.Stock
			label = key
		default:
			return nil, workflowError("invalid_input", "未知分组", WorkflowCase{})
		}
		g := m[key]
		if g == nil {
			g = &ReviewGroup{Key: key, Label: label}
			m[key] = g
		}
		g.Count++
		if it.ChoiceID != "" {
			g.Recommended++
		}
	}
	out := []ReviewGroup{}
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func validReviewGrouping(by string) bool {
	return by == "invoice" || by == "customer" || by == "reason" || by == "stock"
}

func checkDuplicateCapacity(ctx context.Context, conn *sql.Conn, run, sale string, qty int64) error {
	var cap, used int64
	var canonical string
	e := conn.QueryRowContext(ctx, `SELECT g.canonical_id,g.capacity,coalesce((SELECT sum(b.allocated) FROM duplicate_sale_members mm JOIN sale_balances b ON b.run_id=mm.run_id AND b.sale_id=mm.sale_id WHERE mm.run_id=g.run_id AND mm.group_id=g.id),0) FROM duplicate_sale_members m JOIN duplicate_sale_groups g ON g.run_id=m.run_id AND g.id=m.group_id WHERE m.run_id=? AND m.sale_id=?`, run, sale).Scan(&canonical, &cap, &used)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if canonical != sale && qty > 0 {
		return workflowError("ineligible_candidate", "已确认重复组，请选择代表销售："+canonical, WorkflowCase{})
	}
	if used < 0 || used > cap || qty > cap-used {
		return workflowError("insufficient_remaining", "重复组可退余量不足（整组只计一笔销售）", WorkflowCase{})
	}
	return nil
}
