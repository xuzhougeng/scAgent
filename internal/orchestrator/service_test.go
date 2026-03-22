package orchestrator

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scagent/internal/models"
	runtimeclient "scagent/internal/runtime"
	"scagent/internal/session"
	"scagent/internal/skill"
)

type emptyLLMPlanner struct{}

func (p emptyLLMPlanner) Plan(context.Context, PlanningRequest) (models.Plan, error) {
	return models.Plan{}, nil
}

func (p emptyLLMPlanner) Mode() string {
	return "llm"
}

type failingLLMPlanner struct{}

func (p failingLLMPlanner) Plan(context.Context, PlanningRequest) (models.Plan, error) {
	return models.Plan{}, errors.New("planner request failed: context deadline exceeded")
}

func (p failingLLMPlanner) Mode() string {
	return "llm"
}

type scriptedPlanner struct {
	mode     string
	plans    []models.Plan
	errs     map[int]error
	requests []PlanningRequest
}

func (p *scriptedPlanner) Plan(_ context.Context, request PlanningRequest) (models.Plan, error) {
	callIndex := len(p.requests)
	p.requests = append(p.requests, request)
	if err := p.errs[callIndex]; err != nil {
		return models.Plan{}, err
	}
	if callIndex >= len(p.plans) {
		return models.Plan{}, errors.New("unexpected planner call")
	}
	return p.plans[callIndex], nil
}

func (p *scriptedPlanner) Mode() string {
	if p.mode != "" {
		return p.mode
	}
	return "llm"
}

type sequentialRuntime struct {
	nextBackendRef int
}

type scriptedEvaluator struct {
	results  []*CompletionEvaluation
	errs     map[int]error
	requests []EvaluationRequest
}

func (e *scriptedEvaluator) Evaluate(_ context.Context, request EvaluationRequest) (*CompletionEvaluation, error) {
	callIndex := len(e.requests)
	e.requests = append(e.requests, request)
	if err := e.errs[callIndex]; err != nil {
		return nil, err
	}
	if callIndex >= len(e.results) {
		return &CompletionEvaluation{}, nil
	}
	return e.results[callIndex], nil
}

func (e *scriptedEvaluator) Mode() string {
	return "fake"
}

func (r *sequentialRuntime) Health(context.Context) error {
	return nil
}

func (r *sequentialRuntime) Status(context.Context) (*runtimeclient.HealthStatus, error) {
	return &runtimeclient.HealthStatus{}, nil
}

func (r *sequentialRuntime) InitSession(context.Context, runtimeclient.InitSessionRequest) (*runtimeclient.InitSessionResponse, error) {
	return nil, errors.New("unexpected init session call")
}

func (r *sequentialRuntime) LoadFile(context.Context, runtimeclient.LoadFileRequest) (*runtimeclient.LoadFileResponse, error) {
	return nil, errors.New("unexpected load file call")
}

func (r *sequentialRuntime) Execute(_ context.Context, payload runtimeclient.ExecuteRequest) (*runtimeclient.ExecuteResponse, error) {
	r.nextBackendRef++
	return &runtimeclient.ExecuteResponse{
		Summary: "done " + payload.Skill,
		Object: &runtimeclient.ObjectDescriptor{
			BackendRef: "backend_" + strconv.Itoa(r.nextBackendRef),
			Kind:       models.ObjectFilteredDataset,
			Label:      payload.Skill + "_result",
			State:      models.ObjectResident,
			InMemory:   true,
			Metadata:   map[string]any{},
		},
	}, nil
}

func TestBuildExecutablePlanFallsBackWhenLLMReturnsEmptyPlan(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, emptyLLMPlanner{}, t.TempDir())
	plan, info, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err != nil {
		t.Fatalf("build executable plan: %v", err)
	}
	if !info.UsedFallback || info.Note == "" {
		t.Fatalf("expected fallback note when LLM returns empty plan")
	}

	wantSkills := []string{
		"normalize_total",
		"log1p_transform",
		"select_hvg",
		"run_pca",
		"compute_neighbors",
		"run_umap",
	}
	if len(plan.Steps) != len(wantSkills) {
		t.Fatalf("unexpected step count: got %d want %d", len(plan.Steps), len(wantSkills))
	}
	for index, want := range wantSkills {
		if plan.Steps[index].Skill != want {
			t.Fatalf("unexpected skill at %d: got %q want %q", index, plan.Steps[index].Skill, want)
		}
	}
}

func TestBuildExecutablePlanFallsBackWhenLLMRequestFails(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, failingLLMPlanner{}, t.TempDir())
	plan, info, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err != nil {
		t.Fatalf("build executable plan: %v", err)
	}
	if !info.UsedFallback || info.Note == "" {
		t.Fatalf("expected fallback note when LLM request fails")
	}
	if info.PlannerError == "" {
		t.Fatalf("expected original planner error in fallback info")
	}
	if len(plan.Steps) == 0 || plan.Steps[0].Skill != "normalize_total" {
		t.Fatalf("unexpected fallback plan: %+v", plan.Steps)
	}
}

func TestBuildPlanningRequestIncludesRecentContext(t *testing.T) {
	store := session.NewStore()
	service := NewService(store, nil, nil, NewFakePlanner(), t.TempDir())

	sessionRecord := store.CreateSession("test")
	now := time.Now().UTC()
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)

	store.SaveObject(&models.ObjectMeta{
		ID:        "obj_active",
		SessionID: sessionRecord.ID,
		Label:     "prepared_pbmc3k",
		Kind:      models.ObjectFilteredDataset,
		Metadata: map[string]any{
			"obsm_keys": []string{"X_umap"},
		},
		CreatedAt:      now,
		LastAccessedAt: now,
	})
	store.AddMessage(&models.Message{
		ID:        "msg_prev_user",
		SessionID: sessionRecord.ID,
		Role:      models.MessageUser,
		Content:   "画一下 UMAP 图",
		CreatedAt: now,
	})
	store.AddMessage(&models.Message{
		ID:        "msg_prev_assistant",
		SessionID: sessionRecord.ID,
		Role:      models.MessageAssistant,
		Content:   "执行完成：plot_umap：已生成真实 UMAP 图。",
		CreatedAt: now.Add(time.Second),
	})
	store.AddMessage(&models.Message{
		ID:        "msg_current",
		SessionID: sessionRecord.ID,
		Role:      models.MessageUser,
		Content:   "把这个图改一下",
		CreatedAt: now.Add(2 * time.Second),
	})
	store.SaveJob(&models.Job{
		ID:        "job_prev",
		SessionID: sessionRecord.ID,
		Status:    models.JobSucceeded,
		Summary:   "已生成 UMAP 图。",
		Steps: []models.JobStep{
			{Skill: "plot_umap"},
		},
		CreatedAt:  now,
		FinishedAt: ptrTime(now.Add(time.Second)),
	})
	store.SaveArtifact(&models.Artifact{
		ID:        "art_prev",
		SessionID: sessionRecord.ID,
		Kind:      models.ArtifactPlot,
		Title:     "prepared_pbmc3k 的 UMAP 图",
		Summary:   "prepared_pbmc3k 的真实 UMAP 散点图。",
		CreatedAt: now.Add(time.Second),
	})

	request, err := service.buildPlanningRequest(sessionRecord, "把这个图改一下")
	if err != nil {
		t.Fatalf("build planning request: %v", err)
	}

	if request.ActiveObject == nil || request.ActiveObject.ID != "obj_active" {
		t.Fatalf("expected active object in planning request, got %+v", request.ActiveObject)
	}
	if len(request.RecentMessages) != 2 {
		t.Fatalf("expected previous messages without current one, got %d", len(request.RecentMessages))
	}
	if request.RecentMessages[len(request.RecentMessages)-1].Content != "执行完成：plot_umap：已生成真实 UMAP 图。" {
		t.Fatalf("unexpected recent assistant message: %+v", request.RecentMessages)
	}
	if len(request.RecentJobs) != 1 || request.RecentJobs[0].ID != "job_prev" {
		t.Fatalf("unexpected recent jobs: %+v", request.RecentJobs)
	}
	if len(request.RecentArtifacts) != 1 || request.RecentArtifacts[0].ID != "art_prev" {
		t.Fatalf("unexpected recent artifacts: %+v", request.RecentArtifacts)
	}
	if request.WorkingMemory == nil {
		t.Fatalf("expected working memory in planning request")
	}
	if request.WorkingMemory.Focus == nil || request.WorkingMemory.Focus.ActiveObjectID != "obj_active" {
		t.Fatalf("expected working memory focus on obj_active, got %+v", request.WorkingMemory.Focus)
	}
}

func TestBuildExecutablePlanInheritsMissingLegendFromRecentPlotContext(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{
						ID:             "step_1",
						Skill:          "plot_umap",
						TargetObjectID: "$active",
						Params: map[string]any{
							"color_by": "louvain",
						},
					},
				},
			},
		},
	}
	service := NewService(session.NewStore(), registry, nil, planner, t.TempDir())

	plan, _, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "把这个图改一下",
		RecentJobs: []*models.Job{
			{
				ID:     "job_prev",
				Status: models.JobSucceeded,
				Steps: []models.JobStep{
					{
						Skill: "plot_umap",
						Metadata: map[string]any{
							"legend_loc": "on data",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("build executable plan: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("unexpected step count: %+v", plan.Steps)
	}
	if plan.Steps[0].Params["legend_loc"] != "on data" {
		t.Fatalf("expected missing legend_loc to inherit from recent plot, got %+v", plan.Steps[0].Params)
	}
}

func TestBuildExecutablePlanKeepsPlannerLegendChoiceWhenProvided(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{
						ID:             "step_1",
						Skill:          "plot_umap",
						TargetObjectID: "$active",
						Params: map[string]any{
							"color_by":   "louvain",
							"legend_loc": "right",
						},
					},
				},
			},
		},
	}
	service := NewService(session.NewStore(), registry, nil, planner, t.TempDir())

	plan, _, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "把图例放右边",
		RecentJobs: []*models.Job{
			{
				ID:     "job_prev",
				Status: models.JobSucceeded,
				Steps: []models.JobStep{
					{
						Skill: "plot_umap",
						Metadata: map[string]any{
							"legend_loc": "on data",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("build executable plan: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("unexpected step count: %+v", plan.Steps)
	}
	if plan.Steps[0].Params["legend_loc"] != "right" {
		t.Fatalf("expected explicit legend request to win, got %+v", plan.Steps[0].Params)
	}
}

func TestRunJobReplansRemainingStepsFromCurrentState(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	now := time.Now().UTC()
	sessionRecord := store.CreateSession("test")
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)
	store.SaveObject(&models.ObjectMeta{
		ID:             "obj_active",
		SessionID:      sessionRecord.ID,
		Label:          "pbmc3k",
		Kind:           models.ObjectRawDataset,
		BackendRef:     "backend_seed",
		State:          models.ObjectResident,
		InMemory:       true,
		CreatedAt:      now,
		LastAccessedAt: now,
	})
	store.SaveJob(&models.Job{
		ID:        "job_replan",
		SessionID: sessionRecord.ID,
		Status:    models.JobQueued,
		CreatedAt: now,
	})

	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "normalize_total", TargetObjectID: "$active", Params: map[string]any{"target_sum": 1e4}},
				},
			},
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "normalize_total", TargetObjectID: "$active", Params: map[string]any{"target_sum": 1e4}},
					{ID: "step_2", Skill: "log1p_transform", TargetObjectID: "$prev"},
				},
			},
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "normalize_total", TargetObjectID: "$active", Params: map[string]any{"target_sum": 1e4}},
					{ID: "step_2", Skill: "log1p_transform", TargetObjectID: "$prev"},
					{ID: "step_3", Skill: "run_pca", TargetObjectID: "$prev"},
				},
			},
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "normalize_total", TargetObjectID: "$active", Params: map[string]any{"target_sum": 1e4}},
					{ID: "step_2", Skill: "log1p_transform", TargetObjectID: "$prev"},
					{ID: "step_3", Skill: "run_pca", TargetObjectID: "$prev"},
				},
			},
		},
	}
	service := NewService(store, registry, &sequentialRuntime{}, planner, t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_replan", "完成常规的数据预处理")

	job, ok := store.GetJob("job_replan")
	if !ok {
		t.Fatalf("expected job to exist after run")
	}
	if job.Status != models.JobIncomplete {
		t.Fatalf("expected job to remain incomplete, got %s (%s)", job.Status, job.Error)
	}
	if len(job.Steps) != 3 {
		t.Fatalf("expected 3 executed steps after replanning, got %+v", job.Steps)
	}
	if job.Summary == "" {
		t.Fatalf("expected incomplete job summary to explain why the request is not done")
	}

	wantSkills := []string{"normalize_total", "log1p_transform", "run_pca"}
	for index, want := range wantSkills {
		if job.Steps[index].Skill != want {
			t.Fatalf("unexpected skill at %d: got %q want %q", index, job.Steps[index].Skill, want)
		}
	}

	if len(planner.requests) < 2 {
		t.Fatalf("expected replanning requests, got %d", len(planner.requests))
	}
	if !jobHasCheckpoint(job, "检查点重规划", "已更新计划") {
		t.Fatalf("expected checkpoint replan update to be recorded, got %+v", job.Checkpoints)
	}
	replanRequest := planner.requests[1]
	if len(replanRequest.RecentJobs) == 0 {
		t.Fatalf("expected current running job in replanning context")
	}
	currentJob := replanRequest.RecentJobs[len(replanRequest.RecentJobs)-1]
	if currentJob.Status != models.JobRunning {
		t.Fatalf("expected running current job context, got %+v", currentJob)
	}
	if len(currentJob.Steps) != 1 || currentJob.Steps[0].Skill != "normalize_total" {
		t.Fatalf("expected completed step in replanning context, got %+v", currentJob.Steps)
	}
}

func TestRunJobStopsWhenEvaluatorMarksRequestComplete(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	now := time.Now().UTC()
	sessionRecord := store.CreateSession("test")
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)
	store.SaveObject(&models.ObjectMeta{
		ID:             "obj_active",
		SessionID:      sessionRecord.ID,
		Label:          "pbmc3k",
		Kind:           models.ObjectRawDataset,
		BackendRef:     "backend_seed",
		State:          models.ObjectResident,
		InMemory:       true,
		CreatedAt:      now,
		LastAccessedAt: now,
	})
	store.SaveJob(&models.Job{
		ID:        "job_eval_complete",
		SessionID: sessionRecord.ID,
		Status:    models.JobQueued,
		CreatedAt: now,
	})

	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "assess_dataset", TargetObjectID: "$active"},
				},
			},
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "assess_dataset", TargetObjectID: "$active"},
					{ID: "step_2", Skill: "normalize_total", TargetObjectID: "$prev"},
				},
			},
		},
	}
	evaluator := &scriptedEvaluator{
		results: []*CompletionEvaluation{
			{Completed: true, Reason: "评估结果已满足当前请求。"},
		},
	}
	service := NewServiceWithEvaluator(store, registry, &sequentialRuntime{}, planner, evaluator, t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_eval_complete", "评估一下当前数据集")

	job, ok := store.GetJob("job_eval_complete")
	if !ok {
		t.Fatalf("expected job to exist after run")
	}
	if job.Status != models.JobSucceeded {
		t.Fatalf("expected job to succeed, got %s (%s)", job.Status, job.Error)
	}
	if len(job.Steps) != 1 || job.Steps[0].Skill != "assess_dataset" {
		t.Fatalf("expected evaluator to stop after assess_dataset, got %+v", job.Steps)
	}
	if job.Summary != "评估结果已满足当前请求。" {
		t.Fatalf("unexpected completion summary: %q", job.Summary)
	}
	if len(planner.requests) != 1 {
		t.Fatalf("expected evaluator to prevent checkpoint replanning, got %d planner calls", len(planner.requests))
	}
	if len(evaluator.requests) != 1 {
		t.Fatalf("expected one evaluator call, got %d", len(evaluator.requests))
	}
	if !jobHasCheckpoint(job, "完成判定", "已满足请求") {
		t.Fatalf("expected completion checkpoint to be recorded, got %+v", job.Checkpoints)
	}
}

func TestRunJobKeepsOriginalRemainingStepsWhenCheckpointReplanFails(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	now := time.Now().UTC()
	sessionRecord := store.CreateSession("test")
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)
	store.SaveObject(&models.ObjectMeta{
		ID:             "obj_active",
		SessionID:      sessionRecord.ID,
		Label:          "pbmc3k",
		Kind:           models.ObjectRawDataset,
		BackendRef:     "backend_seed",
		State:          models.ObjectResident,
		InMemory:       true,
		CreatedAt:      now,
		LastAccessedAt: now,
	})
	store.SaveJob(&models.Job{
		ID:        "job_keep_plan",
		SessionID: sessionRecord.ID,
		Status:    models.JobQueued,
		CreatedAt: now,
	})

	planner := &scriptedPlanner{
		mode: "fake",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "normalize_total", TargetObjectID: "$active"},
					{ID: "step_2", Skill: "log1p_transform", TargetObjectID: "$prev"},
				},
			},
		},
		errs: map[int]error{
			1: errors.New("checkpoint replan failed"),
			2: errors.New("checkpoint replan failed"),
		},
	}
	service := NewService(store, registry, &sequentialRuntime{}, planner, t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_keep_plan", "完成常规的数据预处理")

	job, ok := store.GetJob("job_keep_plan")
	if !ok {
		t.Fatalf("expected job to exist after run")
	}
	if job.Status != models.JobIncomplete {
		t.Fatalf("expected job to remain incomplete, got %s (%s)", job.Status, job.Error)
	}
	if len(job.Steps) != 2 {
		t.Fatalf("expected original remaining steps to continue after replan failure, got %+v", job.Steps)
	}
	if job.Steps[0].Skill != "normalize_total" || job.Steps[1].Skill != "log1p_transform" {
		t.Fatalf("unexpected executed steps: %+v", job.Steps)
	}
	if !jobHasCheckpoint(job, "检查点重规划", "沿用原计划") {
		t.Fatalf("expected fallback replan checkpoint to be recorded, got %+v", job.Checkpoints)
	}
}

func TestRunJobRecordsPlannerFallbackErrorAndIncompleteStatus(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	now := time.Now().UTC()
	sessionRecord := store.CreateSession("test")
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)
	store.SaveObject(&models.ObjectMeta{
		ID:             "obj_active",
		SessionID:      sessionRecord.ID,
		Label:          "pbmc3k",
		Kind:           models.ObjectRawDataset,
		BackendRef:     "backend_seed",
		State:          models.ObjectResident,
		InMemory:       true,
		CreatedAt:      now,
		LastAccessedAt: now,
	})
	store.SaveJob(&models.Job{
		ID:        "job_fallback_incomplete",
		SessionID: sessionRecord.ID,
		Status:    models.JobQueued,
		CreatedAt: now,
	})

	evaluator := &scriptedEvaluator{
		results: []*CompletionEvaluation{
			{Completed: false, Reason: "柱状图和统计结果都还没有生成。"},
		},
	}
	service := NewServiceWithEvaluator(store, registry, &sequentialRuntime{}, failingLLMPlanner{}, evaluator, t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_fallback_incomplete", "接受louvain统计各个类型，画图")

	job, ok := store.GetJob("job_fallback_incomplete")
	if !ok {
		t.Fatalf("expected job to exist after run")
	}
	if job.Status != models.JobIncomplete {
		t.Fatalf("expected job to be incomplete, got %s (%s)", job.Status, job.Error)
	}
	if job.Summary != "柱状图和统计结果都还没有生成。" {
		t.Fatalf("unexpected incomplete summary: %q", job.Summary)
	}
	if len(job.Checkpoints) == 0 {
		t.Fatalf("expected planning checkpoint to be recorded")
	}
	planningCheckpoint := job.Checkpoints[0]
	if planningCheckpoint.Title != "初始规划" || planningCheckpoint.Label != "规则兜底" {
		t.Fatalf("unexpected planning checkpoint: %+v", planningCheckpoint)
	}
	if planningCheckpoint.Metadata == nil {
		t.Fatalf("expected planning checkpoint metadata, got %+v", planningCheckpoint)
	}
	if planningCheckpoint.Metadata["planner_error"] == "" {
		t.Fatalf("expected planner_error in checkpoint metadata, got %+v", planningCheckpoint.Metadata)
	}
	if planningCheckpoint.Metadata["fallback_reason"] != "planner_request_failed" {
		t.Fatalf("unexpected fallback_reason: %+v", planningCheckpoint.Metadata)
	}
	if !strings.Contains(planningCheckpoint.Summary, "原始错误：") {
		t.Fatalf("expected planning checkpoint summary to expose original error, got %q", planningCheckpoint.Summary)
	}
}

func TestUploadPluginBundleRegistersSkills(t *testing.T) {
	dataRoot := t.TempDir()
	registry, err := skill.LoadRegistryWithPluginsAndState(
		skillsRegistryPath(),
		filepath.Join(dataRoot, "skill-hub", "plugins"),
		filepath.Join(dataRoot, "skill-hub", "state.json"),
	)
	if err != nil {
		t.Fatalf("load skills registry with plugin dir: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, NewFakePlanner(), dataRoot)

	var buffer bytes.Buffer
	archiveWriter := zip.NewWriter(&buffer)
	manifest, err := archiveWriter.Create("plugin.json")
	if err != nil {
		t.Fatalf("create manifest entry: %v", err)
	}
	_, _ = manifest.Write([]byte(`{
		"id": "demo-hub",
		"name": "Demo Hub",
		"skills": [
			{
				"name": "demo_runtime_skill",
				"label": "Demo Runtime Skill",
				"category": "custom",
				"support_level": "wired",
				"description": "Uploaded from test bundle.",
				"target_kinds": ["raw_dataset"],
				"input": {},
				"output": {"summary": "string"},
				"runtime": {"entrypoint": "plugin.py"}
			}
		]
	}`))
	script, err := archiveWriter.Create("plugin.py")
	if err != nil {
		t.Fatalf("create script entry: %v", err)
	}
	_, _ = script.Write([]byte("def run(context):\n    return {'summary': 'ok'}\n"))
	if err := archiveWriter.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	bundle, err := service.UploadPluginBundle("demo-hub.zip", bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("upload plugin bundle: %v", err)
	}
	if bundle.ID != "demo-hub" {
		t.Fatalf("unexpected bundle id: %q", bundle.ID)
	}

	if _, ok := service.skills.Get("demo_runtime_skill"); !ok {
		t.Fatalf("expected uploaded plugin skill to be registered")
	}
}

func TestSetPluginBundleEnabledDisablesBuiltinBundle(t *testing.T) {
	dataRoot := t.TempDir()
	registry, err := skill.LoadRegistryWithPluginsAndState(
		skillsRegistryPath(),
		filepath.Join(dataRoot, "skill-hub", "plugins"),
		filepath.Join(dataRoot, "skill-hub", "state.json"),
	)
	if err != nil {
		t.Fatalf("load skills registry with state: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, NewFakePlanner(), dataRoot)

	bundle, err := service.SetPluginBundleEnabled(skill.BuiltinBundleID, false)
	if err != nil {
		t.Fatalf("disable builtin bundle: %v", err)
	}
	if bundle.Enabled {
		t.Fatalf("expected builtin bundle to be disabled")
	}
	if _, ok := service.skills.Get("inspect_dataset"); ok {
		t.Fatalf("expected builtin inspect_dataset skill to be removed after disabling")
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func jobHasCheckpoint(job *models.Job, title, label string) bool {
	if job == nil {
		return false
	}
	for _, checkpoint := range job.Checkpoints {
		if checkpoint.Title == title && checkpoint.Label == label {
			return true
		}
	}
	return false
}
