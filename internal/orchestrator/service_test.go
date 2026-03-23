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

type unhealthyLLMPlanner struct {
	planCalls int
}

func (p *unhealthyLLMPlanner) Plan(context.Context, PlanningRequest) (models.Plan, error) {
	p.planCalls++
	return models.Plan{
		Steps: []models.PlanStep{
			{ID: "step_1", Skill: "normalize_total", TargetObjectID: "$active"},
		},
	}, nil
}

func (p *unhealthyLLMPlanner) Mode() string {
	return "llm"
}

func (p *unhealthyLLMPlanner) Health(context.Context) error {
	return errors.New("planner request failed: context deadline exceeded")
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

type restoringRuntime struct {
	ensuredRefs map[string]struct{}
	ensureCalls []runtimeclient.EnsureObjectRequest
	execCalls   []runtimeclient.ExecuteRequest
}

type scalarResultRuntime struct {
	ensureCalls []runtimeclient.EnsureObjectRequest
	execCalls   []runtimeclient.ExecuteRequest
}

type blockingRuntime struct {
	started     chan struct{}
	cancelCalls []runtimeclient.CancelExecutionRequest
}

type scriptedEvaluator struct {
	results  []*CompletionEvaluation
	errs     map[int]error
	requests []EvaluationRequest
}

type scriptedAnswerer struct {
	directAnswer string
	directOK     bool
	directErr    error
	response     *ResponseComposeResult
	requests     []PlanningRequest
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

func (a *scriptedAnswerer) BuildDirectAnswer(_ context.Context, request PlanningRequest) (string, bool, error) {
	a.requests = append(a.requests, request)
	if a.directErr != nil {
		return "", false, a.directErr
	}
	return a.directAnswer, a.directOK, nil
}

func (a *scriptedAnswerer) BuildInvestigationResponse(_ context.Context, request ResponseComposeRequest) (*ResponseComposeResult, error) {
	if a.response != nil {
		return a.response, nil
	}
	return NewNoopAnswerer().BuildInvestigationResponse(context.Background(), request)
}

func (a *scriptedAnswerer) BuildFailureAnswer(err error) string {
	return NewNoopAnswerer().BuildFailureAnswer(err)
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

func (r *sequentialRuntime) EnsureObject(_ context.Context, payload runtimeclient.EnsureObjectRequest) (*runtimeclient.EnsureObjectResponse, error) {
	return &runtimeclient.EnsureObjectResponse{
		Object:  payload.Object,
		Summary: "already available",
	}, nil
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

func (r *sequentialRuntime) CancelExecution(context.Context, runtimeclient.CancelExecutionRequest) (*runtimeclient.CancelExecutionResponse, error) {
	return &runtimeclient.CancelExecutionResponse{
		Summary:  "stopped",
		Stopped:  true,
		Isolated: false,
	}, nil
}

func (r *restoringRuntime) Health(context.Context) error {
	return nil
}

func (r *restoringRuntime) Status(context.Context) (*runtimeclient.HealthStatus, error) {
	return &runtimeclient.HealthStatus{}, nil
}

func (r *restoringRuntime) InitSession(context.Context, runtimeclient.InitSessionRequest) (*runtimeclient.InitSessionResponse, error) {
	return nil, errors.New("unexpected init session call")
}

func (r *restoringRuntime) LoadFile(context.Context, runtimeclient.LoadFileRequest) (*runtimeclient.LoadFileResponse, error) {
	return nil, errors.New("unexpected load file call")
}

func (r *restoringRuntime) EnsureObject(_ context.Context, payload runtimeclient.EnsureObjectRequest) (*runtimeclient.EnsureObjectResponse, error) {
	r.ensureCalls = append(r.ensureCalls, payload)
	if r.ensuredRefs == nil {
		r.ensuredRefs = make(map[string]struct{})
	}

	descriptor := payload.Object
	if descriptor.BackendRef == "" {
		descriptor.BackendRef = "rehydrated_backend"
	}
	r.ensuredRefs[descriptor.BackendRef] = struct{}{}
	return &runtimeclient.EnsureObjectResponse{
		Object:  descriptor,
		Summary: "rehydrated",
	}, nil
}

func (r *restoringRuntime) Execute(_ context.Context, payload runtimeclient.ExecuteRequest) (*runtimeclient.ExecuteResponse, error) {
	r.execCalls = append(r.execCalls, payload)
	if _, ok := r.ensuredRefs[payload.TargetBackendRef]; !ok {
		return nil, errors.New("target object was not rehydrated")
	}
	return &runtimeclient.ExecuteResponse{
		Summary: "inspected dataset",
		Metadata: map[string]any{
			"available_obs": []string{"cell_type"},
		},
	}, nil
}

func (r *restoringRuntime) CancelExecution(context.Context, runtimeclient.CancelExecutionRequest) (*runtimeclient.CancelExecutionResponse, error) {
	return &runtimeclient.CancelExecutionResponse{
		Summary:  "stopped",
		Stopped:  true,
		Isolated: false,
	}, nil
}

func (r *scalarResultRuntime) Health(context.Context) error {
	return nil
}

func (r *scalarResultRuntime) Status(context.Context) (*runtimeclient.HealthStatus, error) {
	return &runtimeclient.HealthStatus{}, nil
}

func (r *scalarResultRuntime) InitSession(context.Context, runtimeclient.InitSessionRequest) (*runtimeclient.InitSessionResponse, error) {
	return nil, errors.New("unexpected init session call")
}

func (r *scalarResultRuntime) LoadFile(context.Context, runtimeclient.LoadFileRequest) (*runtimeclient.LoadFileResponse, error) {
	return nil, errors.New("unexpected load file call")
}

func (r *scalarResultRuntime) EnsureObject(_ context.Context, payload runtimeclient.EnsureObjectRequest) (*runtimeclient.EnsureObjectResponse, error) {
	r.ensureCalls = append(r.ensureCalls, payload)
	return &runtimeclient.EnsureObjectResponse{
		Object:  payload.Object,
		Summary: "already available",
	}, nil
}

func (r *scalarResultRuntime) Execute(_ context.Context, payload runtimeclient.ExecuteRequest) (*runtimeclient.ExecuteResponse, error) {
	r.execCalls = append(r.execCalls, payload)
	return &runtimeclient.ExecuteResponse{
		Summary: "已完成针对 pbmc3k 的自定义 Python 分析。",
		Facts: map[string]any{
			"analysis_kind": "custom_python",
			"result_value":  float64(4848644),
			"stdout_text":   "4848644",
		},
		Metadata: map[string]any{
			"stdout": "4848644",
		},
	}, nil
}

func (r *scalarResultRuntime) CancelExecution(context.Context, runtimeclient.CancelExecutionRequest) (*runtimeclient.CancelExecutionResponse, error) {
	return &runtimeclient.CancelExecutionResponse{
		Summary:  "stopped",
		Stopped:  true,
		Isolated: false,
	}, nil
}

func (r *blockingRuntime) Health(context.Context) error {
	return nil
}

func (r *blockingRuntime) Status(context.Context) (*runtimeclient.HealthStatus, error) {
	return &runtimeclient.HealthStatus{}, nil
}

func (r *blockingRuntime) InitSession(context.Context, runtimeclient.InitSessionRequest) (*runtimeclient.InitSessionResponse, error) {
	return nil, errors.New("unexpected init session call")
}

func (r *blockingRuntime) LoadFile(context.Context, runtimeclient.LoadFileRequest) (*runtimeclient.LoadFileResponse, error) {
	return nil, errors.New("unexpected load file call")
}

func (r *blockingRuntime) EnsureObject(_ context.Context, payload runtimeclient.EnsureObjectRequest) (*runtimeclient.EnsureObjectResponse, error) {
	return &runtimeclient.EnsureObjectResponse{
		Object:  payload.Object,
		Summary: "already available",
	}, nil
}

func (r *blockingRuntime) Execute(ctx context.Context, payload runtimeclient.ExecuteRequest) (*runtimeclient.ExecuteResponse, error) {
	if r.started != nil {
		select {
		case <-r.started:
		default:
			close(r.started)
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *blockingRuntime) CancelExecution(_ context.Context, payload runtimeclient.CancelExecutionRequest) (*runtimeclient.CancelExecutionResponse, error) {
	r.cancelCalls = append(r.cancelCalls, payload)
	return &runtimeclient.CancelExecutionResponse{
		Summary:  "worker stopped",
		Stopped:  true,
		Isolated: false,
	}, nil
}

func TestBuildExecutablePlanRejectsEmptyLLMPlan(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, emptyLLMPlanner{}, t.TempDir())
	_, err = service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err == nil {
		t.Fatalf("expected empty LLM plan to be rejected")
	}
	if !strings.Contains(err.Error(), "plan has no steps") {
		t.Fatalf("expected validation error for empty plan, got %v", err)
	}
}

func TestBuildExecutablePlanReturnsPlannerErrorWhenLLMRequestFails(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, failingLLMPlanner{}, t.TempDir())
	_, err = service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err == nil {
		t.Fatalf("expected planner request failure to be returned")
	}
	if !strings.Contains(err.Error(), "planner request failed") {
		t.Fatalf("expected original planner error, got %v", err)
	}
}

func TestRetryJobCanOverrideOriginalMessage(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	answerer := &scriptedAnswerer{
		directAnswer: "已按修改后的请求处理。",
		directOK:     true,
	}
	service := NewServiceWithComponents(store, registry, &sequentialRuntime{}, NewFakePlanner(), NewFakeEvaluator(), answerer, t.TempDir())

	sessionRecord := store.CreateSession("retry-edit")
	now := time.Now().UTC()
	finishedAt := now

	originalMessage := &models.Message{
		ID:        "msg_original",
		SessionID: sessionRecord.ID,
		Role:      models.MessageUser,
		Content:   "旧请求",
		CreatedAt: now,
	}
	store.AddMessage(originalMessage)
	store.SaveJob(&models.Job{
		ID:          "job_original",
		WorkspaceID: sessionRecord.WorkspaceID,
		SessionID:   sessionRecord.ID,
		MessageID:   originalMessage.ID,
		Status:      models.JobCanceled,
		CreatedAt:   now,
		FinishedAt:  &finishedAt,
	})
	store.AddMessage(&models.Message{
		ID:        "msg_old_assistant",
		SessionID: sessionRecord.ID,
		JobID:     "job_original",
		Role:      models.MessageAssistant,
		Content:   "当前任务已停止。",
		CreatedAt: now,
	})

	job, snapshot, err := service.RetryJob(context.Background(), "job_original", "新请求")
	if err != nil {
		t.Fatalf("retry with override: %v", err)
	}
	if job != nil {
		t.Fatalf("expected direct answer path without background job, got %+v", job)
	}
	if _, ok := store.GetJob("job_original"); ok {
		t.Fatalf("expected original job to be removed")
	}
	if len(answerer.requests) != 1 || answerer.requests[0].Message != "新请求" {
		t.Fatalf("expected retry to use override message, got %+v", answerer.requests)
	}
	if snapshot == nil || len(snapshot.Messages) != 2 {
		t.Fatalf("expected replacement user/assistant messages, got %+v", snapshot)
	}
	if snapshot.Messages[0].Role != models.MessageUser || snapshot.Messages[0].Content != "新请求" {
		t.Fatalf("expected new user message, got %+v", snapshot.Messages[0])
	}
	if snapshot.Messages[1].Role != models.MessageAssistant || snapshot.Messages[1].Content != "已按修改后的请求处理。" {
		t.Fatalf("expected replacement assistant message, got %+v", snapshot.Messages[1])
	}
}

func TestRetryJobCanEditSucceededJob(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	answerer := &scriptedAnswerer{
		directAnswer: "已按编辑后的成功任务重新处理。",
		directOK:     true,
	}
	service := NewServiceWithComponents(store, registry, &sequentialRuntime{}, NewFakePlanner(), NewFakeEvaluator(), answerer, t.TempDir())

	sessionRecord := store.CreateSession("retry-succeeded")
	now := time.Now().UTC()
	finishedAt := now

	originalMessage := &models.Message{
		ID:        "msg_succeeded",
		SessionID: sessionRecord.ID,
		Role:      models.MessageUser,
		Content:   "成功任务的原请求",
		CreatedAt: now,
	}
	store.AddMessage(originalMessage)
	store.SaveJob(&models.Job{
		ID:          "job_succeeded",
		WorkspaceID: sessionRecord.WorkspaceID,
		SessionID:   sessionRecord.ID,
		MessageID:   originalMessage.ID,
		Status:      models.JobSucceeded,
		CreatedAt:   now,
		FinishedAt:  &finishedAt,
	})
	store.AddMessage(&models.Message{
		ID:        "msg_succeeded_assistant",
		SessionID: sessionRecord.ID,
		JobID:     "job_succeeded",
		Role:      models.MessageAssistant,
		Content:   "第一次成功执行的回答。",
		CreatedAt: now,
	})

	job, snapshot, err := service.RetryJob(context.Background(), "job_succeeded", "修改后的请求")
	if err != nil {
		t.Fatalf("retry succeeded job: %v", err)
	}
	if job != nil {
		t.Fatalf("expected direct answer path without background job, got %+v", job)
	}
	if _, ok := store.GetJob("job_succeeded"); ok {
		t.Fatalf("expected original succeeded job to be removed")
	}
	if len(answerer.requests) != 1 || answerer.requests[0].Message != "修改后的请求" {
		t.Fatalf("expected retry to use edited message, got %+v", answerer.requests)
	}
	if snapshot == nil || len(snapshot.Messages) != 2 {
		t.Fatalf("expected replacement user/assistant messages, got %+v", snapshot)
	}
	if snapshot.Messages[0].Role != models.MessageUser || snapshot.Messages[0].Content != "修改后的请求" {
		t.Fatalf("expected new user message, got %+v", snapshot.Messages[0])
	}
	if snapshot.Messages[1].Role != models.MessageAssistant || snapshot.Messages[1].Content != "已按编辑后的成功任务重新处理。" {
		t.Fatalf("expected replacement assistant message, got %+v", snapshot.Messages[1])
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

func TestSubmitMessageAnswersSimpleDatasetQuestionWithoutJob(t *testing.T) {
	store := session.NewStore()
	answerer := &scriptedAnswerer{
		directAnswer: "当前对象 pbmc3k 有 2638 个细胞。",
		directOK:     true,
	}
	service := NewServiceWithComponents(store, nil, nil, NewFakePlanner(), NewFakeEvaluator(), answerer, t.TempDir())

	sessionRecord := store.CreateSession("test")
	now := time.Now().UTC()
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)

	workspaceRecord, ok := store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		t.Fatalf("expected workspace to exist")
	}
	workspaceRecord.ActiveObjectID = "obj_active"
	store.SaveWorkspace(workspaceRecord)

	store.SaveObject(&models.ObjectMeta{
		ID:        "obj_active",
		SessionID: sessionRecord.ID,
		Label:     "pbmc3k",
		Kind:      models.ObjectRawDataset,
		NObs:      2638,
		NVars:     1838,
		Metadata: map[string]any{
			"obsm_keys": []string{"X_umap"},
			"assessment": map[string]any{
				"has_umap": true,
			},
		},
		CreatedAt:      now,
		LastAccessedAt: now,
	})

	job, snapshot, err := service.SubmitMessage(context.Background(), sessionRecord.ID, "有多少细胞")
	if err != nil {
		t.Fatalf("submit message: %v", err)
	}
	if job != nil {
		t.Fatalf("expected direct answer without job, got %+v", job)
	}
	if len(snapshot.Jobs) != 0 {
		t.Fatalf("expected no jobs in snapshot, got %+v", snapshot.Jobs)
	}
	lastMessage := snapshot.Messages[len(snapshot.Messages)-1]
	if lastMessage.Role != models.MessageAssistant {
		t.Fatalf("expected assistant reply, got %+v", lastMessage)
	}
	if lastMessage.Content != "当前对象 pbmc3k 有 2638 个细胞。" {
		t.Fatalf("unexpected assistant content: %q", lastMessage.Content)
	}
	if len(answerer.requests) != 1 {
		t.Fatalf("expected answerer to receive one semantic direct-answer request, got %d", len(answerer.requests))
	}
}

func TestSubmitMessageWithArtifactsPassesInputArtifactsToAnswerer(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	answerer := &scriptedAnswerer{
		directAnswer: "已根据图片生成直接回答。",
		directOK:     true,
	}
	service := NewServiceWithComponents(store, registry, nil, emptyLLMPlanner{}, NewNoopEvaluator(), answerer, t.TempDir())

	sessionRecord := store.CreateSession("vision")
	if sessionRecord == nil {
		t.Fatalf("expected session to be created")
	}

	artifact, _, err := service.RegisterExternalArtifact(
		context.Background(),
		sessionRecord.ID,
		models.ArtifactFile,
		"weixin_inbound.png",
		"image/png",
		"微信图片",
		"用户上传的图片",
		bytes.NewReader(tinyPNG()),
	)
	if err != nil {
		t.Fatalf("register external artifact: %v", err)
	}

	job, snapshot, err := service.SubmitMessageWithArtifacts(
		context.Background(),
		sessionRecord.ID,
		"请解释这张图",
		[]*models.Artifact{artifact},
	)
	if err != nil {
		t.Fatalf("submit message with artifacts: %v", err)
	}
	if job != nil {
		t.Fatalf("expected direct answer without job, got %+v", job)
	}
	if snapshot == nil || len(snapshot.Messages) == 0 {
		t.Fatalf("expected snapshot with reply message")
	}
	if len(answerer.requests) != 1 {
		t.Fatalf("expected one answerer request, got %d", len(answerer.requests))
	}
	if len(answerer.requests[0].InputArtifacts) != 1 || answerer.requests[0].InputArtifacts[0].ID != artifact.ID {
		t.Fatalf("unexpected input artifacts: %+v", answerer.requests[0].InputArtifacts)
	}
}

func TestSubmitMessageRejectsConcurrentActiveJob(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	runtime := &blockingRuntime{started: make(chan struct{})}
	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{
						ID:             "step_1",
						Skill:          "inspect_dataset",
						TargetObjectID: "$active",
					},
				},
			},
		},
	}
	service := NewServiceWithComponents(store, registry, runtime, planner, NewFakeEvaluator(), NewNoopAnswerer(), t.TempDir())

	sessionRecord := store.CreateSession("test")
	now := time.Now().UTC()
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)

	workspaceRecord, ok := store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		t.Fatalf("expected workspace to exist")
	}
	workspaceRecord.ActiveObjectID = "obj_active"
	store.SaveWorkspace(workspaceRecord)

	store.SaveObject(&models.ObjectMeta{
		ID:             "obj_active",
		WorkspaceID:    sessionRecord.WorkspaceID,
		SessionID:      sessionRecord.ID,
		DatasetID:      workspaceRecord.DatasetID,
		Label:          "pbmc3k",
		BackendRef:     "py:test:adata_1",
		Kind:           models.ObjectRawDataset,
		NObs:           2638,
		NVars:          1838,
		State:          models.ObjectResident,
		InMemory:       true,
		CreatedAt:      now,
		LastAccessedAt: now,
	})

	job, _, err := service.SubmitMessage(context.Background(), sessionRecord.ID, "查看当前数据集")
	if err != nil {
		t.Fatalf("submit first message: %v", err)
	}
	if job == nil {
		t.Fatalf("expected running job to be created")
	}

	select {
	case <-runtime.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for runtime execution to start")
	}

	if _, _, err := service.SubmitMessage(context.Background(), sessionRecord.ID, "再来一个任务"); err == nil {
		t.Fatalf("expected concurrent submit to be rejected")
	} else if !strings.Contains(err.Error(), "当前已有任务正在运行") {
		t.Fatalf("unexpected concurrent submit error: %v", err)
	}

	if _, _, err := service.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if len(runtime.cancelCalls) != 1 || runtime.cancelCalls[0].SessionID != sessionRecord.ID {
		t.Fatalf("expected runtime cancel request for session %s, got %+v", sessionRecord.ID, runtime.cancelCalls)
	}

	for range 100 {
		currentJob, ok := store.GetJob(job.ID)
		if ok && currentJob.Status == models.JobCanceled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	currentJob, _ := store.GetJob(job.ID)
	t.Fatalf("expected job to become canceled, got %+v", currentJob)
}

func TestCancelJobAddsCanceledAssistantMessage(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	runtime := &blockingRuntime{started: make(chan struct{})}
	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{
						ID:             "step_1",
						Skill:          "inspect_dataset",
						TargetObjectID: "$active",
					},
				},
			},
		},
	}
	service := NewServiceWithComponents(store, registry, runtime, planner, NewFakeEvaluator(), NewNoopAnswerer(), t.TempDir())

	sessionRecord := store.CreateSession("test")
	now := time.Now().UTC()
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)

	workspaceRecord, ok := store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		t.Fatalf("expected workspace to exist")
	}
	workspaceRecord.ActiveObjectID = "obj_active"
	store.SaveWorkspace(workspaceRecord)

	store.SaveObject(&models.ObjectMeta{
		ID:             "obj_active",
		WorkspaceID:    sessionRecord.WorkspaceID,
		SessionID:      sessionRecord.ID,
		DatasetID:      workspaceRecord.DatasetID,
		Label:          "pbmc3k",
		BackendRef:     "py:test:adata_1",
		Kind:           models.ObjectRawDataset,
		NObs:           2638,
		NVars:          1838,
		State:          models.ObjectResident,
		InMemory:       true,
		CreatedAt:      now,
		LastAccessedAt: now,
	})

	job, _, err := service.SubmitMessage(context.Background(), sessionRecord.ID, "查看当前数据集")
	if err != nil {
		t.Fatalf("submit message: %v", err)
	}

	select {
	case <-runtime.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for runtime execution to start")
	}

	if _, _, err := service.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if len(runtime.cancelCalls) != 1 || runtime.cancelCalls[0].SessionID != sessionRecord.ID {
		t.Fatalf("expected runtime cancel request for session %s, got %+v", sessionRecord.ID, runtime.cancelCalls)
	}

	for range 100 {
		snapshot, err := store.Snapshot(sessionRecord.ID)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if len(snapshot.Messages) > 0 {
			lastMessage := snapshot.Messages[len(snapshot.Messages)-1]
			if lastMessage.JobID == job.ID && lastMessage.Role == models.MessageAssistant && lastMessage.Content == "当前任务已停止。" {
				if snapshot.Jobs[len(snapshot.Jobs)-1].Status != models.JobCanceled {
					t.Fatalf("expected canceled job status, got %+v", snapshot.Jobs[len(snapshot.Jobs)-1])
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	snapshot, _ := store.Snapshot(sessionRecord.ID)
	t.Fatalf("expected canceled assistant message, got %+v", snapshot.Messages)
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

	plan, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
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

	plan, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
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
	store.SaveJob(newQueuedInvestigationJob("job_replan", sessionRecord.ID, now))

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
	service := NewServiceWithEvaluator(store, registry, &sequentialRuntime{}, planner, NewFakeEvaluator(), t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_replan", "完成常规的数据预处理", nil)

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
	store.SaveJob(newQueuedInvestigationJob("job_eval_complete", sessionRecord.ID, now))

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

	service.runJob(context.Background(), sessionRecord.ID, "job_eval_complete", "评估一下当前数据集", nil)

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

func TestRunJobRespondsFromStructuredEvidenceAfterInvestigation(t *testing.T) {
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
		NObs:           2638,
		NVars:          1838,
		CreatedAt:      now,
		LastAccessedAt: now,
	})
	store.SaveJob(newQueuedInvestigationJob("job_scalar_result", sessionRecord.ID, now))

	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{
						ID:             "step_1",
						Skill:          "run_python_analysis",
						TargetObjectID: "$active",
						Params: map[string]any{
							"code":            "result_value = int(adata.n_obs * adata.n_vars)",
							"output_label":    "cell_gene_product",
							"persist_output":  false,
							"result_summary":  "",
							"result_text":     "",
							"result_value":    nil,
							"result_metadata": nil,
						},
					},
				},
			},
		},
	}
	evaluator := &scriptedEvaluator{
		results: []*CompletionEvaluation{
			{Completed: true},
		},
	}
	runtimeStub := &scalarResultRuntime{}
	service := NewServiceWithEvaluator(store, registry, runtimeStub, planner, evaluator, t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_scalar_result", "细胞x基因=？", nil)

	job, ok := store.GetJob("job_scalar_result")
	if !ok {
		t.Fatalf("expected job to exist after run")
	}
	if job.Status != models.JobSucceeded {
		t.Fatalf("expected job to succeed, got %s (%s)", job.Status, job.Error)
	}
	if len(job.Steps) != 1 {
		t.Fatalf("expected one executed step, got %+v", job.Steps)
	}
	if got := renderEvidenceValue(job.Steps[0].Facts["result_value"]); got != "4848644" {
		t.Fatalf("expected structured scalar result to be captured, got %q", got)
	}
	if job.CurrentPhase != models.JobPhaseRespond {
		t.Fatalf("expected current phase to end at respond, got %q", job.CurrentPhase)
	}
	if len(job.Phases) != 3 {
		t.Fatalf("expected three job phases, got %+v", job.Phases)
	}
	if job.Phases[0].Kind != models.JobPhaseDecide || job.Phases[0].Status != models.JobPhaseCompleted {
		t.Fatalf("unexpected decide phase: %+v", job.Phases[0])
	}
	if job.Phases[1].Kind != models.JobPhaseInvestigate || job.Phases[1].Status != models.JobPhaseCompleted {
		t.Fatalf("unexpected investigate phase: %+v", job.Phases[1])
	}
	if job.Phases[2].Kind != models.JobPhaseRespond || job.Phases[2].Status != models.JobPhaseCompleted {
		t.Fatalf("unexpected respond phase: %+v", job.Phases[2])
	}

	snapshot, err := store.Snapshot(sessionRecord.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	lastMessage := snapshot.Messages[len(snapshot.Messages)-1]
	if lastMessage.Role != models.MessageAssistant {
		t.Fatalf("expected assistant message, got %+v", lastMessage)
	}
	if lastMessage.Content != "结果是 4848644。" {
		t.Fatalf("expected final answer to come from structured evidence, got %q", lastMessage.Content)
	}
	if len(runtimeStub.execCalls) != 1 || runtimeStub.execCalls[0].Skill != "run_python_analysis" {
		t.Fatalf("expected one run_python_analysis execution, got %+v", runtimeStub.execCalls)
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
	store.SaveJob(newQueuedInvestigationJob("job_keep_plan", sessionRecord.ID, now))

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
	service := NewServiceWithEvaluator(store, registry, &sequentialRuntime{}, planner, NewFakeEvaluator(), t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_keep_plan", "完成常规的数据预处理", nil)

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

func TestRunJobRehydratesSharedActiveObjectBeforeExecution(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	store := session.NewStore()
	now := time.Now().UTC()
	seedSession := store.CreateSession("seed")
	sharedObject := &models.ObjectMeta{
		ID:               "obj_shared",
		WorkspaceID:      seedSession.WorkspaceID,
		SessionID:        seedSession.ID,
		DatasetID:        seedSession.DatasetID,
		Label:            "pbmc3k",
		Kind:             models.ObjectRawDataset,
		BackendRef:       "py:sess_legacy:adata_1",
		NObs:             2638,
		NVars:            1838,
		State:            models.ObjectResident,
		InMemory:         true,
		MaterializedPath: filepath.Join(t.TempDir(), "pbmc3k.h5ad"),
		CreatedAt:        now,
		LastAccessedAt:   now,
	}
	store.SaveObject(sharedObject)

	workspaceRecord, ok := store.GetWorkspace(seedSession.WorkspaceID)
	if !ok {
		t.Fatalf("expected workspace to exist")
	}
	workspaceRecord.ActiveObjectID = sharedObject.ID
	store.SaveWorkspace(workspaceRecord)

	seedSession.ActiveObjectID = sharedObject.ID
	store.SaveSession(seedSession)

	followupSession, err := store.CreateConversation(seedSession.WorkspaceID, "followup")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	store.SaveJob(newQueuedInvestigationJob("job_rehydrate", followupSession.ID, now))

	planner := &scriptedPlanner{
		mode: "llm",
		plans: []models.Plan{
			{
				Steps: []models.PlanStep{
					{ID: "step_1", Skill: "inspect_dataset", TargetObjectID: "$active"},
				},
			},
		},
	}
	runtimeStub := &restoringRuntime{}
	service := NewService(store, registry, runtimeStub, planner, t.TempDir())

	service.runJob(context.Background(), followupSession.ID, "job_rehydrate", "检查数据", nil)

	job, ok := store.GetJob("job_rehydrate")
	if !ok {
		t.Fatalf("expected job to exist after run")
	}
	if job.Status != models.JobSucceeded {
		t.Fatalf("expected job to succeed, got %s (%s)", job.Status, job.Error)
	}
	if len(runtimeStub.ensureCalls) != 1 {
		t.Fatalf("expected one runtime rehydration call, got %d", len(runtimeStub.ensureCalls))
	}
	if runtimeStub.ensureCalls[0].SessionID != followupSession.ID {
		t.Fatalf("expected rehydration to use current session id, got %+v", runtimeStub.ensureCalls[0])
	}
	if runtimeStub.ensureCalls[0].Object.BackendRef != "py:sess_legacy:adata_1" {
		t.Fatalf("expected stored backend ref to be rehydrated, got %+v", runtimeStub.ensureCalls[0].Object)
	}
	if len(runtimeStub.execCalls) != 1 {
		t.Fatalf("expected one execute call, got %d", len(runtimeStub.execCalls))
	}
	if runtimeStub.execCalls[0].TargetBackendRef != "py:sess_legacy:adata_1" {
		t.Fatalf("expected execute to target the rehydrated backend ref, got %+v", runtimeStub.execCalls[0])
	}
}

func TestRunJobFailsCleanlyWhenPlannerIsUnavailable(t *testing.T) {
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
	store.SaveJob(newQueuedInvestigationJob("job_fallback_incomplete", sessionRecord.ID, now))

	evaluator := &scriptedEvaluator{
		results: []*CompletionEvaluation{
			{Completed: false, Reason: "柱状图和统计结果都还没有生成。"},
		},
	}
	service := NewServiceWithEvaluator(store, registry, &sequentialRuntime{}, failingLLMPlanner{}, evaluator, t.TempDir())

	service.runJob(context.Background(), sessionRecord.ID, "job_fallback_incomplete", "接受louvain统计各个类型，画图", nil)

	job, ok := store.GetJob("job_fallback_incomplete")
	if !ok {
		t.Fatalf("expected job to exist after run")
	}
	if job.Status != models.JobFailed {
		t.Fatalf("expected job to fail, got %s (%s)", job.Status, job.Error)
	}
	if job.Error != "规划器暂时不可用，本次执行未开始。请稍后重试。" {
		t.Fatalf("unexpected public planner error: %q", job.Error)
	}
	if len(job.Checkpoints) == 0 {
		t.Fatalf("expected failure checkpoint to be recorded")
	}
	failureCheckpoint := job.Checkpoints[0]
	if failureCheckpoint.Title != "执行失败" || failureCheckpoint.Label != "已终止" {
		t.Fatalf("unexpected failure checkpoint: %+v", failureCheckpoint)
	}
	if failureCheckpoint.Metadata == nil {
		t.Fatalf("expected failure checkpoint metadata, got %+v", failureCheckpoint)
	}
	if failureCheckpoint.Metadata["raw_error"] == "" {
		t.Fatalf("expected raw_error in checkpoint metadata, got %+v", failureCheckpoint.Metadata)
	}
	if strings.Contains(failureCheckpoint.Summary, "context deadline exceeded") {
		t.Fatalf("expected public checkpoint summary to hide raw planner error, got %q", failureCheckpoint.Summary)
	}
	snapshot, err := store.Snapshot(sessionRecord.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	lastMessage := snapshot.Messages[len(snapshot.Messages)-1]
	if lastMessage.Role != models.MessageAssistant || lastMessage.Content != "规划器暂时不可用，本次执行未开始。请稍后重试。" {
		t.Fatalf("unexpected assistant failure message: %+v", lastMessage)
	}
}

func TestBuildExecutablePlanDoesNotBlockOnPlannerHealthFailure(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	planner := &unhealthyLLMPlanner{}
	service := NewService(session.NewStore(), registry, nil, planner, t.TempDir())

	plan, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err != nil {
		t.Fatalf("expected execution path to ignore planner health failure, got %v", err)
	}
	if planner.planCalls != 1 {
		t.Fatalf("expected planner Plan to be called once, got %d", planner.planCalls)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Skill != "normalize_total" {
		t.Fatalf("unexpected plan: %+v", plan)
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

func newQueuedInvestigationJob(id, sessionID string, now time.Time) *models.Job {
	job := &models.Job{
		ID:           id,
		SessionID:    sessionID,
		Status:       models.JobQueued,
		CurrentPhase: models.JobPhaseInvestigate,
		Summary:      "当前上下文不足以直接回答，开始收集相关信息。",
		CreatedAt:    now,
	}
	initializeInvestigationPhases(job, now)
	return job
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
