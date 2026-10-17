package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"returnsrecon/internal/auth"
	"returnsrecon/internal/store"
)

type Job struct {
	ID       string          `json:"id"`
	Scope    string          `json:"scope"`
	Status   string          `json:"status"`
	Stage    string          `json:"stage"`
	RunID    string          `json:"run_id"`
	Error    string          `json:"error,omitempty"`
	Progress json.RawMessage `json:"progress"`
}
type Jobs struct {
	Store            *store.Store
	Project, Runtime string
	Python           string
	ctx              context.Context
	cancel           context.CancelFunc
	gate             chan struct{}
	mu               sync.Mutex
	active           int
	wg               sync.WaitGroup
}

func NewJobs(s *store.Store, project, runtime string) (*Jobs, error) {
	_, e := s.DB.Exec(`CREATE TABLE IF NOT EXISTS web_jobs(id TEXT PRIMARY KEY,scope TEXT NOT NULL,status TEXT NOT NULL,stage TEXT NOT NULL,run_id TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',progress TEXT NOT NULL DEFAULT '{}'); UPDATE web_jobs SET status='failed',stage='interrupted',error='服务上次中断；请创建新的验证任务' WHERE status IN ('queued','running')`)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Jobs{Store: s, Project: project, Runtime: runtime, ctx: ctx, cancel: cancel, gate: make(chan struct{}, 1)}, nil
}
func (j *Jobs) Close() { j.cancel(); j.wg.Wait() }
func (j *Jobs) Get(ctx context.Context, id string) (v Job, e error) {
	var progress string
	e = j.Store.DB.QueryRowContext(ctx, "SELECT id,scope,status,stage,run_id,error,progress FROM web_jobs WHERE id=?", id).Scan(&v.ID, &v.Scope, &v.Status, &v.Stage, &v.RunID, &v.Error, &progress)
	v.Progress = json.RawMessage(progress)
	if e != nil {
		return v, &auth.Error{Code: "not_found", Message: "任务不存在", Status: 404}
	}
	return v, nil
}
func (j *Jobs) Start(ctx context.Context, scope string) (Job, error) {
	if scope != "benchmark" && scope != "year1" && scope != "all" {
		return Job{}, &auth.Error{Code: "invalid_input", Message: "未知数据范围", Status: 422}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.active >= 3 {
		return Job{}, &auth.Error{Code: "busy", Message: "已有任务运行或排队，请稍后再试", Status: 429}
	}
	random, e := auth.RandomToken()
	if e != nil {
		return Job{}, e
	}
	id := "job-" + time.Now().Format("20060102-150405") + "-" + random[:12]
	if e = j.Store.EnqueueWebJob(ctx, id, scope); e != nil {
		return Job{}, e
	}
	j.active++
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		defer func() { j.mu.Lock(); j.active--; j.mu.Unlock() }()
		select {
		case j.gate <- struct{}{}:
			defer func() { <-j.gate }()
		case <-j.ctx.Done():
			j.fail(id, j.ctx.Err())
			return
		}
		if e := j.process(id, scope); e != nil {
			j.fail(id, e)
		}
	}()
	return j.Get(ctx, id)
}
func (j *Jobs) fail(id string, e error) {
	message := "数据处理未完成；请查看项目外任务诊断并新建任务"
	if e == context.Canceled {
		message = "任务中断；未发布结果不作为完成"
	}
	j.Store.DB.Exec("UPDATE web_jobs SET status='failed',stage='failed',error=? WHERE id=?", message, id)
}
func (j *Jobs) process(id, scope string) error {
	dir := filepath.Join(j.Runtime, "jobs", id)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	j.Store.DB.Exec("UPDATE web_jobs SET status='running',stage='preparing' WHERE id=?", id)
	python := j.Python
	if python == "" {
		python = filepath.Join(j.Runtime, "tools", "python", "Scripts", "python.exe")
	}
	preparedRoot := filepath.Join(dir, "prepared")
	cmd := exec.CommandContext(j.ctx, python, filepath.Join(j.Project, "tools", "prepare.py"), "--scope", scope, "--output", preparedRoot)
	cmd.Dir = j.Project
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	log, e := os.OpenFile(filepath.Join(dir, "prepare.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd.Stderr = log
	if e = cmd.Start(); e != nil {
		return e
	}
	var prepared string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		log.Write(append(append([]byte{}, line...), '\n'))
		var value map[string]any
		if json.Unmarshal(line, &value) == nil {
			if p, ok := value["path"].(string); ok {
				prepared = p
			}
			safe := map[string]any{}
			for _, key := range []string{"stage", "raw", "accepted", "excluded", "rows_read", "rows", "total", "sheet", "sheet_index", "stats"} {
				if x, ok := value[key]; ok {
					safe[key] = x
				}
			}
			raw, _ := json.Marshal(safe)
			j.Store.DB.Exec("UPDATE web_jobs SET progress=? WHERE id=?", string(raw), id)
		}
	}
	scanErr := scanner.Err()
	e = cmd.Wait()
	if e != nil {
		return e
	}
	if scanErr != nil {
		return scanErr
	}
	if prepared == "" {
		return fmt.Errorf("preparation did not return a published directory")
	}
	j.Store.DB.Exec("UPDATE web_jobs SET stage='importing' WHERE id=?", id)
	m, e := j.Store.Import(j.ctx, prepared)
	if e != nil {
		return e
	}
	j.Store.DB.Exec("UPDATE web_jobs SET stage='reconciling' WHERE id=?", id)
	r, e := j.Store.Run(j.ctx, m.DatasetID, 0)
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(r)
	_, e = j.Store.DB.Exec("UPDATE web_jobs SET status='succeeded',stage='published',run_id=?,progress=? WHERE id=?", r.RunID, string(raw), id)
	return e
}
