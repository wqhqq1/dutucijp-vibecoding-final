package web

import (
	"database/sql"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"returnsrecon/internal/store"
)

func (s *Server) registerReview(r *gin.RouterGroup) {
	r.POST("/review-sets", func(c *gin.Context) {
		var b struct {
			RunID string `json:"run_id"`
		}
		if !bind(c, &b) {
			return
		}
		v, e := s.Store.GenerateReviewSet(c, actor(c), b.RunID)
		if e != nil {
			respond(c, nil, e)
			return
		}
		respond(c, gin.H{"id": v.ID, "run_id": v.RunID, "created_at": v.CreatedAt, "policy_version": v.PolicyVersion, "counts": v.Counts}, nil)
	})
	r.GET("/review-sets/latest", func(c *gin.Context) {
		a := actor(c)
		var id string
		e := s.Store.DB.QueryRowContext(c, "SELECT id FROM review_sets WHERE run_id=? AND actor=? ORDER BY created_at DESC,id DESC LIMIT 1", c.Query("run_id"), a.ID).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			respond(c, gin.H{"id": ""}, nil)
			return
		}
		if e != nil {
			respond(c, nil, e)
			return
		}
		v, e := s.Store.ListReview(c, a, id, store.QueryFilter{PageSize: 1}, false)
		respond(c, gin.H{"id": id, "policy_version": v.PolicyVersion, "counts": v.Counts}, e)
	})
	r.GET("/review-sets/:id", func(c *gin.Context) {
		f, e := filter(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		f.ReviewGroupBy = c.Query("group_by")
		f.ReviewGroupKey = c.Query("group_key")
		v, e := s.Store.ListReview(c, actor(c), c.Param("id"), f, c.Query("recommended_only") == "true")
		respond(c, v, e)
	})
	r.GET("/review-sets/:id/groups", func(c *gin.Context) {
		f, e := filter(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		v, e := s.Store.ReviewGroups(c, actor(c), c.Param("id"), c.Query("group_by"), f, c.Query("recommended_only") == "true")
		if e != nil {
			respond(c, nil, e)
			return
		}
		p, e := page(c)
		if e != nil {
			respond(c, nil, e)
			return
		}
		if p.Page < 0 || p.PageSize < 0 || p.Page > 10000000 {
			c.JSON(422, gin.H{"message": "分页参数不合法"})
			return
		}
		if p.Page == 0 {
			p.Page = 1
		}
		if p.PageSize == 0 {
			p.PageSize = 50
		}
		if p.PageSize > 100 {
			p.PageSize = 100
		}
		start := (p.Page - 1) * p.PageSize
		if start > len(v) {
			start = len(v)
		}
		end := min(start+p.PageSize, len(v))
		respond(c, gin.H{"items": v[start:end], "total": len(v), "page": p.Page, "page_size": p.PageSize}, nil)
	})
	r.POST("/case-batches/preview", func(c *gin.Context) {
		var req store.BatchRequest
		if !bind(c, &req) {
			return
		}
		var target store.WorkflowActor
		if req.Action == "assign" {
			if !s.finance(c) {
				return
			}
			users, e := s.Auth.Users(c)
			if e != nil {
				respond(c, nil, e)
				return
			}
			for _, u := range users {
				if u.ID == req.Assignee {
					target = store.WorkflowActor{ID: u.ID, Role: u.Role}
					break
				}
			}
		}
		v, e := s.Store.PreviewBatch(c, actor(c), req, target)
		if e != nil {
			respond(c, nil, e)
			return
		}
		respond(c, batchView(v, 1, ""), nil)
	})
	r.GET("/case-batches/latest", func(c *gin.Context) {
		v, e := s.Store.LatestBatch(c, actor(c), c.Query("run_id"))
		respond(c, batchView(v, 1, ""), e)
	})
	r.GET("/case-batches/:id", func(c *gin.Context) {
		p, e := strconv.Atoi(c.DefaultQuery("page", "1"))
		if e != nil || p < 1 || p > 10000000 {
			c.JSON(422, gin.H{"message": "页码不合法"})
			return
		}
		view := c.Query("status")
		if view != "" && !batchStatuses[view] {
			c.JSON(422, gin.H{"message": "结果状态不合法"})
			return
		}
		v, e := s.Store.Batch(c, actor(c), c.Param("id"))
		respond(c, batchView(v, p, view), e)
	})
	r.POST("/case-batches/:id/commit", func(c *gin.Context) {
		var b struct{}
		if !bind(c, &b) {
			return
		}
		v, e := s.Store.CommitBatch(c, actor(c), c.Param("id"))
		respond(c, batchView(v, 1, ""), e)
	})
}

var batchStatuses = map[string]bool{"ready": true, "succeeded": true, "skipped": true, "failed": true}

type batchReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// batchView pages one result status at a time so ready rows are never mixed
// with rows that will be skipped. An empty view picks the most useful
// non-empty status: what can be submitted in a preview, what changed after it.
func batchView(b store.ReviewBatch, page int, view string) gin.H {
	counts := map[string]int{}
	reasons := []*batchReason{}
	byMessage := map[string]*batchReason{}
	for _, g := range b.Groups {
		counts[g.Status] += len(g.Items)
		if g.Status == "skipped" || g.Status == "failed" {
			r := byMessage[g.Message]
			if r == nil {
				r = &batchReason{Code: g.Code, Message: g.Message}
				byMessage[g.Message] = r
				reasons = append(reasons, r)
			}
			r.Count += len(g.Items)
		}
	}
	if view == "" {
		order := []string{"succeeded", "ready", "failed", "skipped"}
		if b.Status == "preview" {
			order = []string{"ready", "skipped", "failed"}
		}
		for _, st := range order {
			if counts[st] > 0 {
				view = st
				break
			}
		}
	}
	items := []gin.H{}
	idx := 0
	for _, g := range b.Groups {
		if view != "" && g.Status != view {
			continue
		}
		for _, entry := range g.Items {
			if idx >= (page-1)*50 && len(items) < 50 {
				it := entry.Item
				items = append(items, gin.H{"case_id": it.Case.ID, "cancel_id": it.Case.CancelID, "invoice": it.Cancel.Invoice, "customer": it.Cancel.Customer, "stock": it.Cancel.Stock, "version": it.Case.Version, "option": entry.Option, "status": g.Status, "code": g.Code, "message": g.Message, "group_id": g.ID, "group_size": len(g.Items)})
			}
			idx++
		}
	}
	return gin.H{"id": b.ID, "set_id": b.SetID, "action": b.Action, "status": b.Status, "created_at": b.CreatedAt, "total": b.Total, "succeeded": counts["succeeded"], "skipped": counts["skipped"], "failed": counts["failed"], "ready": counts["ready"], "readonly": b.ReadOnly, "groups": len(b.Groups), "reasons": reasons, "view": view, "view_total": idx, "items": items, "page": page, "page_size": 50}
}
