package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"returnsrecon/internal/model"
)

type WorkflowVerificationStep struct {
	Operation    string         `json:"operation"`
	Actor        string         `json:"simulated_actor"`
	ExpectedCode string         `json:"expected_code"`
	ActualCode   string         `json:"actual_code"`
	Error        *WorkflowError `json:"rejection,omitempty"`
	Before       WorkflowCase   `json:"before"`
	After        WorkflowCase   `json:"after"`
	AuditBefore  int            `json:"audit_before"`
	AuditAfter   int            `json:"audit_after"`
	Passed       bool           `json:"passed"`
}

type WorkflowVerification struct {
	Scope            string                     `json:"scope"`
	Steps            []WorkflowVerificationStep `json:"steps"`
	History          []WorkflowAudit            `json:"history"`
	ConcurrentCase   WorkflowCase               `json:"concurrent_case"`
	ConcurrentPassed bool                       `json:"concurrent_passed"`
	ConcurrentOK     int                        `json:"concurrent_successes"`
	ConcurrentReject int                        `json:"concurrent_rejections"`
	NoNewAllocations bool                       `json:"no_new_allocations"`
	Idempotent       bool                       `json:"idempotent_history_preserved"`
	Passed           bool                       `json:"passed"`
}

// VerifyWorkflow runs only a fixed scenario in a fresh private database copy.
// The CLI validates and consumes its one-use marker before opening this store.
// It accepts no caller-supplied identity, role, action or conclusion.
func (s *Store) VerifyWorkflow(ctx context.Context, runID string) (report WorkflowVerification, err error) {
	finance := WorkflowActor{"verification-finance", "finance"}
	admin := WorkflowActor{"verification-admin", "admin"}
	owner := WorkflowActor{"verification-warehouse-a", "warehouse"}
	other := WorkflowActor{"verification-warehouse-b", "warehouse"}
	report.Scope = "isolated copy of official transactions; workflow identities/actions/conclusions are simulated; FR-14 is not implemented"
	var id string
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM cases WHERE run_id=? AND status='pending' AND (assignee IS NULL OR assignee='') ORDER BY (cancel_id='0:100124') DESC,id LIMIT 1`, runID).Scan(&id)
	if err != nil {
		return report, fmt.Errorf("need an existing unassigned official difference: %w", err)
	}
	var allocationsBefore int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM allocations WHERE run_id=?", runID).Scan(&allocationsBefore); err != nil {
		return report, err
	}
	step := func(op, who, expected string, call func(WorkflowCase) error) error {
		before, err := s.WorkflowCase(ctx, finance, id)
		if err != nil {
			return err
		}
		var countBefore, countAfter int
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE case_id=?", id).Scan(&countBefore); err != nil {
			return err
		}
		operationErr := call(before)
		actual := "ok"
		var rejection *WorkflowError
		if operationErr != nil {
			if !errors.As(operationErr, &rejection) {
				return operationErr
			}
			actual = rejection.Code
		}
		after, err := s.WorkflowCase(ctx, finance, id)
		if err != nil {
			return err
		}
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE case_id=?", id).Scan(&countAfter); err != nil {
			return err
		}
		passed := actual == expected
		if expected == "ok" {
			passed = passed && after.Version == before.Version+1 && countAfter == countBefore+1
		} else {
			passed = passed && before == after && countAfter == countBefore
		}
		report.Steps = append(report.Steps, WorkflowVerificationStep{op, who, expected, actual, rejection, before, after, countBefore, countAfter, passed})
		if !passed {
			return fmt.Errorf("workflow verification failed at %s", op)
		}
		return nil
	}
	operations := []struct {
		name, actor, expected string
		call                  func(WorkflowCase) error
	}{
		{"assign", finance.ID, "ok", func(c WorkflowCase) error { _, e := s.AssignCase(ctx, finance, owner, id); return e }},
		{"duplicate_assign", admin.ID, "already_assigned", func(c WorkflowCase) error { _, e := s.AssignCase(ctx, admin, other, id); return e }},
		{"skip_pending_to_closed", owner.ID, "invalid_transition", func(c WorkflowCase) error {
			_, e := s.TransitionCase(ctx, owner, id, "closed", c.Version, "资料不足")
			return e
		}},
		{"start", owner.ID, "ok", func(c WorkflowCase) error {
			_, e := s.TransitionCase(ctx, owner, id, "in_progress", c.Version, "")
			return e
		}},
		{"reverse_to_pending", owner.ID, "invalid_transition", func(c WorkflowCase) error {
			_, e := s.TransitionCase(ctx, owner, id, "pending", c.Version, "")
			return e
		}},
		{"empty_conclusion", owner.ID, "empty_conclusion", func(c WorkflowCase) error {
			_, e := s.TransitionCase(ctx, owner, id, "closed", c.Version, " \t\u3000")
			return e
		}},
		{"close_with_explanation", owner.ID, "ok", func(c WorkflowCase) error {
			_, e := s.TransitionCase(ctx, owner, id, "closed", c.Version, "隔离课程演示核查：现有资料不足以确定原销售，保留原候选和来源；说明后结案，不制造关联。")
			return e
		}},
		{"modify_after_close", owner.ID, "closed", func(c WorkflowCase) error {
			_, e := s.TransitionCase(ctx, owner, id, "closed", c.Version, "尝试改写")
			return e
		}},
	}
	for _, op := range operations {
		if err = step(op.name, op.actor, op.expected, op.call); err != nil {
			return report, err
		}
	}
	report.History, err = s.WorkflowHistory(ctx, finance, id)
	if err != nil || len(report.History) != 3 {
		return report, fmt.Errorf("expected 3 audit events: %v", err)
	}
	var concurrentID string
	if err = s.DB.QueryRowContext(ctx, `SELECT id FROM cases WHERE run_id=? AND status='pending' AND (assignee IS NULL OR assignee='') ORDER BY id LIMIT 1`, runID).Scan(&concurrentID); err != nil {
		return report, err
	}
	s2, err := Open(s.path)
	if err != nil {
		return report, err
	}
	defer s2.Close()
	gate := make(chan struct{})
	errorsOut := make(chan error, 2)
	var wg sync.WaitGroup
	for _, input := range []struct {
		db              *Store
		actor, assignee WorkflowActor
	}{{s, finance, owner}, {s2, admin, other}} {
		wg.Add(1)
		go func(db *Store, actor, assignee WorkflowActor) {
			defer wg.Done()
			<-gate
			_, e := db.AssignCase(ctx, actor, assignee, concurrentID)
			errorsOut <- e
		}(input.db, input.actor, input.assignee)
	}
	close(gate)
	wg.Wait()
	close(errorsOut)
	report.ConcurrentCase, err = s.WorkflowCase(ctx, finance, concurrentID)
	if err != nil {
		return report, err
	}
	for e := range errorsOut {
		if e == nil {
			report.ConcurrentOK++
		} else {
			var rejected *WorkflowError
			if !errors.As(e, &rejected) || rejected.Code != "already_assigned" || rejected.ExistingAssignee != report.ConcurrentCase.Assignee {
				return report, fmt.Errorf("unexpected concurrent result: %w", e)
			}
			report.ConcurrentReject++
		}
	}
	concurrentAudit, err := s.WorkflowHistory(ctx, finance, concurrentID)
	if err != nil {
		return report, err
	}
	report.ConcurrentPassed = report.ConcurrentOK == 1 && report.ConcurrentReject == 1 && len(concurrentAudit) == 1 && report.ConcurrentCase.Version == 2
	var allocationsAfter int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM allocations WHERE run_id=?", runID).Scan(&allocationsAfter); err != nil {
		return report, err
	}
	report.NoNewAllocations = allocationsBefore == allocationsAfter
	run, err := s.Run(ctx, report.History[0].After.DatasetID, 1)
	if err != nil {
		return report, err
	}
	afterReuse, err := s2.WorkflowHistory(ctx, finance, id)
	if err != nil {
		return report, err
	}
	report.Idempotent = run.Reused && run.RuleVersion == model.RuleVersion && reflect.DeepEqual(report.History, afterReuse)
	report.Passed = report.ConcurrentPassed && report.NoNewAllocations && report.Idempotent
	if !report.Passed {
		return report, fmt.Errorf("workflow final invariants failed")
	}
	return report, nil
}
