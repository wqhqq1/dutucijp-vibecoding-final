package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type BatchRequest struct {
	RetryBatchID    string            `json:"retry_batch_id,omitempty"`
	SetID           string            `json:"set_id"`
	Action          string            `json:"action"`
	Filter          QueryFilter       `json:"filter"`
	RecommendedOnly bool              `json:"recommended_only"`
	All             bool              `json:"all"`
	IDs             []string          `json:"ids"`
	ExcludeIDs      []string          `json:"exclude_ids"`
	Choices         map[string]string `json:"choices"`
	Assignee        string            `json:"assignee"`
	Conclusion      string            `json:"conclusion"`
	GroupBy         string            `json:"group_by"`
	GroupKey        string            `json:"group_key"`
}
type BatchItem struct {
	Item   ReviewItem    `json:"item"`
	Option *ReviewOption `json:"option,omitempty"`
}
type BatchGroup struct {
	ID      int         `json:"id"`
	Items   []BatchItem `json:"items"`
	Status  string      `json:"status"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}
type ReviewBatch struct {
	ID         string        `json:"id"`
	RunID      string        `json:"run_id"`
	Actor      WorkflowActor `json:"actor"`
	SetID      string        `json:"set_id"`
	Action     string        `json:"action"`
	Assignee   WorkflowActor `json:"assignee"`
	Conclusion string        `json:"conclusion"`
	CreatedAt  string        `json:"created_at"`
	Status     string        `json:"status"`
	Groups     []BatchGroup  `json:"groups"`
	Total      int           `json:"total"`
	Succeeded  int           `json:"succeeded"`
	Skipped    int           `json:"skipped"`
	Failed     int           `json:"failed"`
	// ReadOnly counts selected items already closed in the snapshot; they are
	// left out of the batch instead of being listed as skipped.
	ReadOnly int `json:"readonly"`
}

func reviewGroupKey(it ReviewItem, by string) string {
	switch by {
	case "invoice":
		return it.Cancel.Invoice
	case "customer":
		b, _ := json.Marshal([]string{it.Cancel.Customer, it.Cancel.Country})
		return string(b)
	case "reason":
		return it.Case.Reason
	case "stock":
		return it.Cancel.Stock
	}
	return ""
}
func batchGroupError(g *BatchGroup, e error) {
	g.Status = "skipped"
	g.Code = "conflict"
	g.Message = e.Error()
	var w *WorkflowError
	if errors.As(e, &w) {
		g.Code = w.Code
		g.Message = w.Message
	}
}
func batchHasMatch(action string) bool { return action == "confirm" || action == "approve" }

func (s *Store) PreviewBatch(ctx context.Context, a WorkflowActor, req BatchRequest, target WorkflowActor) (out ReviewBatch, err error) {
	set, e := s.reviewSet(ctx, a, req.SetID)
	if e != nil {
		return out, e
	}
	if req.RetryBatchID != "" {
		previous, re := s.Batch(ctx, a, req.RetryBatchID)
		if re != nil {
			return out, re
		}
		if previous.RunID != set.RunID {
			return out, workflowError("invalid_input", "重试运行不一致", WorkflowCase{})
		}
		req.Action = previous.Action
		req.Conclusion = previous.Conclusion
		req.Assignee = previous.Assignee.ID
		target = previous.Assignee
		req.All = false
		req.IDs = []string{}
		req.ExcludeIDs = nil
		req.Filter = QueryFilter{RunID: set.RunID}
		req.RecommendedOnly = false
		req.GroupBy = ""
		req.Choices = map[string]string{}
		for _, g := range previous.Groups {
			if g.Status == "succeeded" {
				continue
			}
			for _, entry := range g.Items {
				req.IDs = append(req.IDs, entry.Item.Case.ID)
				if entry.Option != nil {
					req.Choices[entry.Item.Case.ID] = entry.Option.ID
				}
			}
		}
	}
	switch req.Action {
	case "assign":
		if a.Role == "warehouse" {
			return out, workflowError("forbidden", "只有财务或管理员可以派单", WorkflowCase{})
		}
		if !target.valid() || target.ID != req.Assignee {
			return out, workflowError("invalid_assignee", "请选择有效责任人", WorkflowCase{})
		}
	case "start", "confirm", "close", "approve":
	default:
		return out, workflowError("invalid_input", "未知批量操作", WorkflowCase{})
	}
	if (req.Action == "close" || req.Action == "approve") && strings.TrimSpace(req.Conclusion) == "" {
		return out, workflowError("empty_conclusion", "关闭前必须填写非空白结论", WorkflowCase{})
	}
	req.Filter.RunID = set.RunID
	req.Filter, e = s.validateFilter(ctx, a, req.Filter)
	if e != nil {
		return out, e
	}
	if req.GroupBy != "" && req.GroupBy != "invoice" && req.GroupBy != "customer" && req.GroupBy != "reason" && req.GroupBy != "stock" {
		return out, workflowError("invalid_input", "未知分组", WorkflowCase{})
	}
	ids, excludes := map[string]bool{}, map[string]bool{}
	for _, id := range req.IDs {
		ids[id] = true
	}
	for _, id := range req.ExcludeIDs {
		excludes[id] = true
	}
	selected := []BatchItem{}
	known := map[string]bool{}
	readOnly := 0
	for _, it := range set.Items {
		known[it.Case.ID] = true
		if !reviewFilterMatches(it, req.Filter, req.RecommendedOnly) || excludes[it.Case.ID] || (!req.All && !ids[it.Case.ID]) {
			continue
		}
		if req.GroupBy != "" && reviewGroupKey(it, req.GroupBy) != req.GroupKey {
			continue
		}
		if !reviewMatches(it, req.Filter, req.RecommendedOnly) {
			readOnly++
			continue
		}
		entry := BatchItem{Item: it}
		if batchHasMatch(req.Action) {
			choice := it.ChoiceID
			if c, ok := req.Choices[it.Case.ID]; ok {
				choice = c
			}
			for _, o := range it.Options {
				if o.ID == choice {
					copy := o
					entry.Option = &copy
					break
				}
			}
		}
		selected = append(selected, entry)
	}
	for id := range ids {
		if !known[id] {
			return out, workflowError("forbidden", "所选项目不属于当前推荐快照", WorkflowCase{})
		}
	}
	if len(selected) == 0 && readOnly > 0 {
		return out, workflowError("closed", "所选项目均已关闭，只读，不能批量修改", WorkflowCase{})
	}
	if len(selected) == 0 {
		return out, workflowError("invalid_input", "请选择当前筛选内的项目", WorkflowCase{})
	}
	if len(selected) > 25000 {
		return out, workflowError("invalid_input", "单批最多25000条", WorkflowCase{})
	}
	// Union every shared effective balance and every selected whole-invoice proposal.
	parent := make([]int, len(selected))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(x, y int) { parent[find(x)] = find(y) }
	resources := map[string]int{}
	for i, it := range selected {
		if it.Option == nil {
			continue
		}
		keys := []string{saleResource(*it.Option)}
		if it.Option.BasketID != "" {
			keys = append(keys, "basket:"+it.Option.BasketID)
		}
		for _, key := range keys {
			if p, ok := resources[key]; ok {
				union(i, p)
			} else {
				resources[key] = i
			}
		}
	}
	groups := map[int][]BatchItem{}
	order := []int{}
	for i, it := range selected {
		r := find(i)
		if _, ok := groups[r]; !ok {
			order = append(order, r)
		}
		groups[r] = append(groups[r], it)
	}
	out = ReviewBatch{RunID: set.RunID, Actor: a, SetID: set.ID, Action: req.Action, Assignee: target, Conclusion: strings.TrimSpace(req.Conclusion), CreatedAt: time.Now().Format(time.RFC3339Nano), Status: "preview", Groups: []BatchGroup{}, Total: len(selected), ReadOnly: readOnly}
	out.ID, e = newReviewID(set.ID + "|" + out.CreatedAt)
	if e != nil {
		return out, e
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
	for i, r := range order {
		g := BatchGroup{ID: i + 1, Items: groups[r], Status: "ready"}
		if e = precheckBatchGroup(ctx, conn, out, g); e != nil {
			batchGroupError(&g, e)
		}
		out.Groups = append(out.Groups, g)
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
		if _, e := c.ExecContext(ctx, "INSERT INTO review_batches(id,run_id,actor,created_at,status,body_json) VALUES(?,?,?,?,'preview',?)", out.ID, out.RunID, a.ID, out.CreatedAt, string(body)); e != nil {
			return e
		}
		for _, g := range out.Groups {
			b, e := json.Marshal(g)
			if e != nil {
				return e
			}
			if _, e = c.ExecContext(ctx, "INSERT INTO review_batch_groups(batch_id,group_id,status,body_json) VALUES(?,?,?,?)", out.ID, g.ID, g.Status, string(b)); e != nil {
				return e
			}
		}
		return nil
	})
	return
}

func precheckBatchGroup(ctx context.Context, c *sql.Conn, b ReviewBatch, g BatchGroup) error {
	demand := map[string]int64{}
	capacity := map[string]int64{}
	members := map[string]bool{}
	for _, entry := range g.Items {
		members[entry.Item.Case.ID] = true
	}
	for _, entry := range g.Items {
		old := entry.Item.Case
		cur, e := readWorkflowCase(ctx, c, old.ID)
		if e != nil {
			return e
		}
		if e = authorizeWorkflowRead(b.Actor, cur); e != nil {
			return e
		}
		// Closed is final, so it explains a stale snapshot better than its version.
		if cur.Status == "closed" {
			return workflowError("closed", "已关闭项目只读", cur)
		}
		if cur.Version != old.Version {
			return workflowError("version_conflict", "项目版本已变化，请重新预览", cur)
		}
		switch b.Action {
		case "assign":
			if cur.Assignee != "" {
				return workflowError("already_assigned", "已有责任人："+cur.Assignee, cur)
			}
			if cur.Status != "pending" {
				return workflowError("invalid_transition", "只能派单待处理项目", cur)
			}
		case "approve":
			if cur.Assignee != "" && cur.Assignee != b.Actor.ID {
				return workflowError("forbidden", "已派给其他责任人", cur)
			}
			if cur.Assignee == "" && b.Actor.Role == "warehouse" {
				return workflowError("forbidden", "仓库只能处理本人项目", cur)
			}
		default:
			if cur.Assignee != b.Actor.ID {
				return workflowError("forbidden", "仅当前责任人可以处理", cur)
			}
			if b.Action == "start" && cur.Status != "pending" {
				return workflowError("invalid_transition", "仅待处理项目可以开始", cur)
			}
			if (b.Action == "close" || b.Action == "confirm") && cur.Status != "in_progress" {
				return workflowError("invalid_transition", "请先开始处理", cur)
			}
		}
		if !batchHasMatch(b.Action) {
			continue
		}
		if entry.Option == nil {
			switch {
			case entry.Item.HasAllocation:
				return workflowError("already_allocated", "退货已经配对，如需结案请改用批量结案", cur)
			case len(entry.Item.Options) == 0:
				return workflowError("no_recommendation", "没有合格推荐，请打开详情逐条核查", cur)
			}
			return workflowError("invalid_candidate", "尚未明确选择配对方案（最近时间可能并列）", cur)
		}
		o := *entry.Option
		if o.BasketID != "" {
			for _, id := range o.BasketCases {
				if !members[id] {
					return workflowError("incomplete_basket", "整单建议必须整组选择，或改用其他推荐", cur)
				}
				found := false
				for _, other := range g.Items {
					if other.Item.Case.ID == id && other.Option != nil && other.Option.BasketID == o.BasketID {
						found = true
						break
					}
				}
				if !found {
					return workflowError("incomplete_basket", "同张退货单的方案不一致", cur)
				}
			}
		}
		var existing int
		if e = c.QueryRowContext(ctx, "SELECT count(*) FROM allocations WHERE run_id=? AND cancel_id=?", cur.RunID, cur.CancelID).Scan(&existing); e != nil {
			return e
		}
		if existing != 0 {
			return workflowError("already_allocated", "退货已经配对", cur)
		}
		cancel, e := readManualRow(ctx, c, cur.DatasetID, cur.CancelID)
		if e != nil {
			return e
		}
		sale, e := readManualRow(ctx, c, cur.DatasetID, o.SaleID)
		if e != nil {
			return e
		}
		qty, _, ok := manualEligible(cancel, sale)
		if !ok || cancel.Duplicate() {
			return workflowError("ineligible_candidate", "当前来源不满足推荐配对约束", cur)
		}
		if e = checkDuplicateCapacity(ctx, c, cur.RunID, o.SaleID, qty); e != nil {
			return e
		}
		resource := saleResource(o)
		if qty > math.MaxInt64-demand[resource] {
			return workflowError("insufficient_remaining", "整组退量超过可表示数量", cur)
		}
		demand[resource] += qty
		if o.Duplicate != nil {
			var used int64
			for _, m := range o.Duplicate.Members {
				var alloc int64
				if e = c.QueryRowContext(ctx, "SELECT allocated FROM sale_balances WHERE run_id=? AND sale_id=?", cur.RunID, m.SaleID).Scan(&alloc); e != nil {
					return e
				}
				if alloc > o.Duplicate.Capacity-used {
					return workflowError("insufficient_remaining", "重复组既有分配超过单笔容量", cur)
				}
				used += alloc
			}
			capacity[resource] = o.Duplicate.Capacity - used
		} else {
			var rem int64
			if e = c.QueryRowContext(ctx, "SELECT remaining FROM sale_balances WHERE run_id=? AND sale_id=?", cur.RunID, o.SaleID).Scan(&rem); e != nil {
				return e
			}
			capacity[resource] = rem
		}
	}
	for key, q := range demand {
		if q < 0 || q > capacity[key] {
			return workflowError("insufficient_remaining", "整组汇总退量超过当前可退余量，已跳过冲突组", WorkflowCase{})
		}
	}
	return nil
}

func activateDuplicate(ctx context.Context, c *sql.Conn, run, actor, batch string, d *DuplicateProposal) error {
	if d == nil {
		return nil
	}
	if len(d.Members) < 2 || d.Capacity <= 0 {
		return workflowError("invalid_candidate", "重复组证据不完整", WorkflowCase{})
	}
	var dataset string
	if e := c.QueryRowContext(ctx, "SELECT dataset_id FROM runs WHERE id=?", run).Scan(&dataset); e != nil {
		return e
	}
	for _, m := range d.Members {
		var raw string
		var qty int64
		if e := c.QueryRowContext(ctx, `SELECT s.raw_json,n.quantity FROM normalized_rows n JOIN source_rows s ON s.id=n.source_id WHERE n.dataset_id=? AND n.id=? AND n.kind='sale' AND n.excluded=''`, dataset, m.SaleID).Scan(&raw, &qty); e != nil {
			return e
		}
		fp, e := rawFingerprint(raw)
		if e != nil || fp != d.Fingerprint || qty != d.Capacity {
			return workflowError("invalid_candidate", "重复来源指纹或数量不一致", WorkflowCase{})
		}
	}
	if _, e := c.ExecContext(ctx, "INSERT INTO duplicate_sale_groups(run_id,id,canonical_id,capacity,actor,batch_id) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,id) DO NOTHING", run, d.ID, d.CanonicalID, d.Capacity, actor, batch); e != nil {
		return e
	}
	for _, m := range d.Members {
		if _, e := c.ExecContext(ctx, "INSERT INTO duplicate_sale_members(run_id,sale_id,group_id) VALUES(?,?,?) ON CONFLICT(run_id,sale_id) DO NOTHING", run, m.SaleID, d.ID); e != nil {
			return e
		}
	}
	return checkDuplicateCapacity(ctx, c, run, d.CanonicalID, 0)
}

func (s *Store) Batch(ctx context.Context, a WorkflowActor, id string) (out ReviewBatch, err error) {
	var body string
	err = s.DB.QueryRowContext(ctx, "SELECT body_json,status FROM review_batches WHERE id=? AND actor=?", id, a.ID).Scan(&body, &out.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return out, workflowError("not_found", "批次不存在", WorkflowCase{})
	}
	if err != nil {
		return
	}
	status := out.Status
	if err = json.Unmarshal([]byte(body), &out); err != nil {
		return
	}
	out.Status = status
	if !a.valid() || out.Actor != a {
		return ReviewBatch{}, workflowError("forbidden", "批次不属于当前身份", WorkflowCase{})
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT group_id,status,result_json FROM review_batch_groups WHERE batch_id=? ORDER BY group_id", id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var gid int
		var st, raw string
		if e = rows.Scan(&gid, &st, &raw); e != nil {
			return out, e
		}
		g := &out.Groups[gid-1]
		g.Status = st
		if raw != "{}" {
			var result BatchGroup
			if e = json.Unmarshal([]byte(raw), &result); e != nil {
				return out, e
			}
			g.Code, g.Message = result.Code, result.Message
		}
		switch st {
		case "succeeded":
			out.Succeeded += len(g.Items)
		case "skipped":
			out.Skipped += len(g.Items)
		case "failed":
			out.Failed += len(g.Items)
		}
	}
	return out, rows.Err()
}
func (s *Store) LatestBatch(ctx context.Context, a WorkflowActor, run string) (ReviewBatch, error) {
	var id string
	e := s.DB.QueryRowContext(ctx, "SELECT id FROM review_batches WHERE actor=? AND run_id=? ORDER BY created_at DESC,id DESC LIMIT 1", a.ID, run).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return ReviewBatch{}, nil
	}
	if e != nil {
		return ReviewBatch{}, e
	}
	return s.Batch(ctx, a, id)
}

func (s *Store) CommitBatch(ctx context.Context, a WorkflowActor, id string) (ReviewBatch, error) {
	b, e := s.Batch(ctx, a, id)
	if e != nil {
		return b, e
	}
	// Even an idempotent replay must pass the current transaction-bound session guard.
	if e = s.immediate(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, "UPDATE review_batches SET status='processing' WHERE id=? AND status<>'completed'", id)
		return e
	}); e != nil {
		return ReviewBatch{}, e
	}
	for _, group := range b.Groups {
		if e = ctx.Err(); e != nil {
			return ReviewBatch{}, e
		}
		e = s.immediate(ctx, func(c *sql.Conn) error {
			var st string
			if e := c.QueryRowContext(ctx, "SELECT status FROM review_batch_groups WHERE batch_id=? AND group_id=?", id, group.ID).Scan(&st); e != nil {
				return e
			}
			if st != "ready" {
				return nil
			}
			g := group
			if e := precheckBatchGroup(ctx, c, b, g); e != nil {
				batchGroupError(&g, e)
				return persistBatchGroup(ctx, c, id, g)
			}
			if _, e := c.ExecContext(ctx, "SAVEPOINT apply_review_group"); e != nil {
				return e
			}
			applyErr := applyBatchGroup(ctx, c, b, g)
			if applyErr != nil {
				if _, e := c.ExecContext(ctx, "ROLLBACK TO apply_review_group"); e != nil {
					return e
				}
				batchGroupError(&g, applyErr)
				var w *WorkflowError
				if !errors.As(applyErr, &w) {
					g.Status = "failed"
					g.Code = "transaction_failed"
					g.Message = "事务未完成，整组已回滚；请核查后重新预览"
				}
			} else {
				g.Status = "succeeded"
				g.Code = ""
				g.Message = "处理完成"
			}
			if _, e := c.ExecContext(ctx, "RELEASE apply_review_group"); e != nil {
				return e
			}
			return persistBatchGroup(ctx, c, id, g)
		})
		if e != nil {
			return ReviewBatch{}, e
		}
	}
	e = s.immediate(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, "UPDATE review_batches SET status='completed' WHERE id=? AND NOT EXISTS(SELECT 1 FROM review_batch_groups WHERE batch_id=? AND status='ready')", id, id)
		return e
	})
	if e != nil {
		return ReviewBatch{}, e
	}
	return s.Batch(ctx, a, id)
}
func persistBatchGroup(ctx context.Context, c *sql.Conn, id string, g BatchGroup) error {
	raw, e := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{g.Code, g.Message})
	if e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, "UPDATE review_batch_groups SET status=?,result_json=? WHERE batch_id=? AND group_id=?", g.Status, string(raw), id, g.ID)
	return e
}
func applyBatchGroup(ctx context.Context, c *sql.Conn, b ReviewBatch, g BatchGroup) error {
	if batchHasMatch(b.Action) {
		for _, entry := range g.Items {
			if entry.Option != nil {
				if e := activateDuplicate(ctx, c, b.RunID, b.Actor.ID, b.ID, entry.Option.Duplicate); e != nil {
					return e
				}
			}
		}
	}
	for _, entry := range g.Items {
		cur, e := readWorkflowCase(ctx, c, entry.Item.Case.ID)
		if e != nil {
			return e
		}
		switch b.Action {
		case "assign":
			_, e = assignCaseTx(ctx, c, b.Actor, b.Assignee, cur.ID)
		case "start":
			_, e = transitionCaseTx(ctx, c, b.Actor, cur.ID, "in_progress", cur.Version, "")
		case "close":
			_, e = transitionCaseTx(ctx, c, b.Actor, cur.ID, "closed", cur.Version, b.Conclusion)
		case "confirm", "approve":
			if b.Action == "approve" {
				if cur.Assignee == "" {
					cur, e = assignCaseTx(ctx, c, b.Actor, b.Actor, cur.ID)
					if e != nil {
						return e
					}
				}
				if cur.Status == "pending" {
					cur, e = transitionCaseTx(ctx, c, b.Actor, cur.ID, "in_progress", cur.Version, "")
					if e != nil {
						return e
					}
				}
			}
			if entry.Option == nil {
				return fmt.Errorf("missing frozen option")
			}
			confirmation, ce := confirmCandidateTx(ctx, c, b.Actor, cur.ID, entry.Option.SaleID, cur.Version)
			if ce != nil {
				return ce
			}
			cur = confirmation.Case
			raw, ce := json.Marshal(entry.Option)
			if ce != nil {
				return ce
			}
			if _, ce = c.ExecContext(ctx, "INSERT INTO review_confirmations(run_id,cancel_id,batch_id,recommendation_kind,policy_version,evidence_json) VALUES(?,?,?,?,?,?)", b.RunID, cur.CancelID, b.ID, entry.Option.Kind, ReviewPolicyVersion, string(raw)); ce != nil {
				return ce
			}
			if b.Action == "approve" {
				_, e = transitionCaseTx(ctx, c, b.Actor, cur.ID, "closed", cur.Version, b.Conclusion)
			}
		}
		if e != nil {
			return e
		}
	}
	return nil
}
