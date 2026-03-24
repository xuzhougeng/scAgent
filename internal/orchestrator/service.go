package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"scagent/internal/models"
	"scagent/internal/runtime"
	"scagent/internal/session"
	"scagent/internal/skill"
)

type Service struct {
	store     *session.Store
	skills    *skill.Registry
	runtime   runtime.Service
	planner   Planner
	evaluator Evaluator
	answerer  Answerer
	dataRoot  string

	jobMu      sync.Mutex
	jobCancels map[string]context.CancelFunc
}

type PlanningRequest struct {
	Message         string                `json:"message"`
	Session         *models.Session       `json:"session,omitempty"`
	Workspace       *models.Workspace     `json:"workspace,omitempty"`
	FocusObject     *models.ObjectMeta    `json:"focus_object,omitempty"`
	GlobalObject    *models.ObjectMeta    `json:"global_object,omitempty"`
	RootObject      *models.ObjectMeta    `json:"root_object,omitempty"`
	Objects         []*models.ObjectMeta  `json:"objects,omitempty"`
	InputArtifacts  []*models.Artifact    `json:"input_artifacts,omitempty"`
	RecentMessages  []*models.Message     `json:"recent_messages,omitempty"`
	RecentJobs      []*models.Job         `json:"recent_jobs,omitempty"`
	RecentArtifacts []*models.Artifact    `json:"recent_artifacts,omitempty"`
	RecentTurns     []*models.Turn        `json:"recent_turns,omitempty"`
	CurrentTurn     *models.Turn          `json:"current_turn,omitempty"`
	WorkingMemory   *models.WorkingMemory `json:"working_memory,omitempty"`
}

type PlannerDebugPreview struct {
	PlannerMode           string          `json:"planner_mode"`
	PlanningRequest       PlanningRequest `json:"planning_request"`
	PlannerContext        []string        `json:"planner_context,omitempty"`
	DeveloperInstructions string          `json:"developer_instructions,omitempty"`
	RequestBody           map[string]any  `json:"request_body,omitempty"`
	Note                  string          `json:"note,omitempty"`
}

type SystemStatus struct {
	SystemMode            string                `json:"system_mode"`
	Summary               string                `json:"summary"`
	PlannerMode           string                `json:"planner_mode"`
	PlannerReady          bool                  `json:"planner_ready"`
	PlannerReachable      bool                  `json:"planner_reachable"`
	LLMLoaded             bool                  `json:"llm_loaded"`
	RuntimeConnected      bool                  `json:"runtime_connected"`
	RuntimeMode           string                `json:"runtime_mode,omitempty"`
	RealH5ADInspection    bool                  `json:"real_h5ad_inspection"`
	RealAnalysisExecution bool                  `json:"real_analysis_execution"`
	ExecutableSkills      []string              `json:"executable_skills,omitempty"`
	Notes                 []string              `json:"notes,omitempty"`
	Runtime               *runtime.HealthStatus `json:"runtime,omitempty"`
}

func NewService(store *session.Store, skills *skill.Registry, runtimeClient runtime.Service, planner Planner, dataRoot string) *Service {
	return NewServiceWithEvaluator(store, skills, runtimeClient, planner, NewNoopEvaluator(), dataRoot)
}

func NewServiceWithEvaluator(store *session.Store, skills *skill.Registry, runtimeClient runtime.Service, planner Planner, evaluator Evaluator, dataRoot string) *Service {
	return NewServiceWithComponents(store, skills, runtimeClient, planner, evaluator, NewNoopAnswerer(), dataRoot)
}

func NewServiceWithComponents(store *session.Store, skills *skill.Registry, runtimeClient runtime.Service, planner Planner, evaluator Evaluator, answerer Answerer, dataRoot string) *Service {
	if evaluator == nil {
		evaluator = NewNoopEvaluator()
	}
	if answerer == nil {
		answerer = NewNoopAnswerer()
	}
	return &Service{
		store:      store,
		skills:     skills,
		runtime:    runtimeClient,
		planner:    planner,
		evaluator:  evaluator,
		answerer:   answerer,
		dataRoot:   dataRoot,
		jobCancels: make(map[string]context.CancelFunc),
	}
}

func (s *Service) setJobCancel(jobID string, cancel context.CancelFunc) {
	if s == nil || strings.TrimSpace(jobID) == "" || cancel == nil {
		return
	}
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	s.jobCancels[jobID] = cancel
}

func (s *Service) clearJobCancel(jobID string) {
	if s == nil || strings.TrimSpace(jobID) == "" {
		return
	}
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	delete(s.jobCancels, jobID)
}

func (s *Service) requestJobCancel(jobID string) bool {
	if s == nil || strings.TrimSpace(jobID) == "" {
		return false
	}
	s.jobMu.Lock()
	cancel := s.jobCancels[jobID]
	s.jobMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Service) activeSessionJob(sessionID string) *models.Job {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	jobs := s.store.ListSessionJobs(sessionID)
	for index := len(jobs) - 1; index >= 0; index-- {
		job := jobs[index]
		if job == nil {
			continue
		}
		if job.Status == models.JobQueued || job.Status == models.JobRunning {
			return job
		}
	}
	return nil
}

func (s *Service) Skills() []skill.Definition {
	_ = s.refreshSkills()
	return s.skills.List()
}

func (s *Service) PlannerMode() string {
	if s.planner == nil {
		return ""
	}
	return s.planner.Mode()
}

func (s *Service) checkPlannerHealth(ctx context.Context) error {
	if s == nil || s.planner == nil {
		return fmt.Errorf("planner is not configured")
	}

	checker, ok := s.planner.(PlannerHealthChecker)
	if !ok {
		return nil
	}

	healthCtx := ctx
	if healthCtx == nil {
		healthCtx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(healthCtx, 1500*time.Millisecond)
	defer cancel()
	return checker.Health(probeCtx)
}

func (s *Service) Status(ctx context.Context) *SystemStatus {
	if err := s.refreshSkills(); err != nil {
		return &SystemStatus{
			SystemMode:       "demo",
			Summary:          "当前处于演示模式：技能注册表加载失败。",
			PlannerMode:      s.PlannerMode(),
			PlannerReady:     s.planner != nil,
			PlannerReachable: s.planner != nil,
			LLMLoaded:        s.PlannerMode() == "llm",
			RuntimeConnected: false,
			Notes:            []string{err.Error()},
		}
	}

	status := &SystemStatus{
		PlannerMode:      s.PlannerMode(),
		PlannerReady:     s.planner != nil,
		PlannerReachable: s.planner != nil,
		LLMLoaded:        s.PlannerMode() == "llm",
	}
	if status.PlannerReady {
		if err := s.checkPlannerHealth(ctx); err != nil {
			status.PlannerReachable = false
			status.Notes = append(status.Notes, "规划器连通性检查失败："+err.Error())
		}
	}

	runtimeStatus, err := s.runtime.Status(ctx)
	if err != nil {
		status.SystemMode = "demo"
		status.RuntimeConnected = false
		status.Summary = "当前处于演示模式：运行时暂时不可达。"
		status.Notes = []string{err.Error()}
		return status
	}

	status.RuntimeConnected = true
	status.Runtime = runtimeStatus
	status.RuntimeMode = runtimeStatus.RuntimeMode
	status.RealH5ADInspection = runtimeStatus.RealH5ADInspection
	status.RealAnalysisExecution = runtimeStatus.RealAnalysisExecution
	status.ExecutableSkills = runtimeStatus.ExecutableSkills
	status.Notes = append(status.Notes, runtimeStatus.Notes...)

	if status.LLMLoaded && status.PlannerReachable && status.RealAnalysisExecution {
		status.SystemMode = "live"
		status.Summary = "当前处于正式模式：LLM 规划器已启用，分析执行为真实运行。"
		return status
	}

	status.SystemMode = "demo"
	switch {
	case status.LLMLoaded && !status.PlannerReachable:
		status.Summary = "当前处于演示模式：LLM 规划器已加载，但连通性检查失败。"
	case !status.LLMLoaded && status.RealH5ADInspection && !status.RealAnalysisExecution:
		status.Summary = "当前处于演示模式：规则规划器生效，h5ad 检查为真实执行，但分析步骤仍为占位实现。"
	case status.LLMLoaded && !status.RealAnalysisExecution:
		status.Summary = "当前处于演示模式：LLM 规划器已启用，但分析执行仍为占位实现。"
	default:
		status.Summary = "当前处于演示模式：生产组件尚未全部启用。"
	}
	return status
}

func (s *Service) PreviewPlan(ctx context.Context, message string) (models.Plan, error) {
	if err := s.refreshSkills(); err != nil {
		return models.Plan{}, err
	}
	plan, err := s.buildExecutablePlan(ctx, PlanningRequest{Message: message})
	return plan, err
}

func (s *Service) PreviewFakePlan(ctx context.Context, message string) (models.Plan, error) {
	if err := s.refreshSkills(); err != nil {
		return models.Plan{}, err
	}
	planner := NewFakePlanner()
	plan, err := planner.Plan(ctx, PlanningRequest{Message: message})
	if err != nil {
		return models.Plan{}, err
	}
	plan = NormalizePlan(plan)
	if err := s.skills.ValidatePlan(plan); err != nil {
		return models.Plan{}, err
	}
	return plan, nil
}

func (s *Service) PreviewPlannerDebug(ctx context.Context, sessionID, message string) (*PlannerDebugPreview, error) {
	if err := s.refreshSkills(); err != nil {
		return nil, err
	}
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("未找到会话 %q", sessionID)
	}

	request, err := s.buildPlanningRequest(sessionRecord, message)
	if err != nil {
		return nil, err
	}

	if debugger, ok := s.planner.(PlannerDebugger); ok {
		return debugger.DebugPreview(ctx, request)
	}

	return &PlannerDebugPreview{
		PlannerMode:     s.PlannerMode(),
		PlanningRequest: request,
		Note:            "当前规划器没有提供更详细的调试预览。",
	}, nil
}

func (s *Service) Subscribe(sessionID string) (<-chan models.Event, func()) {
	return s.store.Subscribe(sessionID)
}

func (s *Service) GetSnapshot(sessionID string) (*models.SessionSnapshot, error) {
	return s.store.Snapshot(sessionID)
}

func (s *Service) GetWorkspaceSnapshot(workspaceID string) (*models.WorkspaceSnapshot, error) {
	return s.store.WorkspaceSnapshot(workspaceID)
}

func (s *Service) ListWorkspaces() *models.WorkspaceList {
	return &models.WorkspaceList{
		Workspaces: s.store.ListWorkspaces(),
	}
}

func (s *Service) DeleteConversation(_ context.Context, sessionID string) error {
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("未找到会话 %q", sessionID)
	}

	workspaceSnapshot, err := s.store.WorkspaceSnapshot(sessionRecord.WorkspaceID)
	if err != nil {
		return err
	}

	if err := s.store.DeleteSession(sessionID); err != nil {
		return err
	}

	for _, conversation := range workspaceSnapshot.Conversations {
		if conversation == nil || conversation.ID == sessionID {
			continue
		}
		s.publishSnapshot(conversation.ID)
	}
	return nil
}

func (s *Service) DeleteWorkspace(_ context.Context, workspaceID string) error {
	if _, err := s.store.WorkspaceSnapshot(workspaceID); err != nil {
		return err
	}
	if err := os.RemoveAll(s.workspaceRoot(workspaceID)); err != nil {
		return fmt.Errorf("删除 workspace 数据目录失败: %w", err)
	}
	return s.store.DeleteWorkspace(workspaceID)
}

func (s *Service) RenameWorkspace(_ context.Context, workspaceID, label string) (*models.WorkspaceSnapshot, error) {
	record, ok := s.store.GetWorkspace(workspaceID)
	if !ok {
		return nil, fmt.Errorf("未找到 workspace %q", workspaceID)
	}
	record.Label = label
	record.UpdatedAt = time.Now().UTC()
	s.store.SaveWorkspace(record)
	return s.store.WorkspaceSnapshot(workspaceID)
}

func (s *Service) RenameConversation(_ context.Context, sessionID, label string) (*models.SessionSnapshot, error) {
	record, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("未找到会话 %q", sessionID)
	}
	record.Label = label
	record.UpdatedAt = time.Now().UTC()
	s.store.SaveSession(record)
	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return nil, err
	}
	s.publishSnapshot(sessionID)
	return snapshot, nil
}

func (s *Service) CreateConversation(_ context.Context, workspaceID, label string) (*models.SessionSnapshot, error) {
	workspaceRecord, ok := s.store.GetWorkspace(workspaceID)
	if !ok {
		return nil, fmt.Errorf("未找到 workspace %q", workspaceID)
	}
	if label == "" {
		label = workspaceRecord.Label + " 对话"
	}

	sessionRecord, err := s.store.CreateConversation(workspaceID, label)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	workspaceRecord.LastAccessedAt = now
	workspaceRecord.UpdatedAt = now
	s.store.SaveWorkspace(workspaceRecord)

	return s.store.Snapshot(sessionRecord.ID)
}

func (s *Service) CreateSession(ctx context.Context, label string, withSample bool) (*models.SessionSnapshot, error) {
	if label == "" {
		label = "植物单细胞分析会话"
	}

	sessionRecord := s.store.CreateSession(label)
	if sessionRecord == nil {
		return nil, fmt.Errorf("创建会话失败")
	}
	workspaceRecord, ok := s.store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		return nil, fmt.Errorf("未找到 workspace %q", sessionRecord.WorkspaceID)
	}

	workspaceRoot := s.workspaceRoot(sessionRecord.WorkspaceID)
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "artifacts"), 0o755); err != nil {
		return nil, fmt.Errorf("create artifacts directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "objects"), 0o755); err != nil {
		return nil, fmt.Errorf("create objects directory: %w", err)
	}

	now := time.Now().UTC()

	if withSample {
		response, err := s.runtime.InitSession(ctx, runtime.InitSessionRequest{
			SessionID:     sessionRecord.ID,
			DatasetID:     workspaceRecord.DatasetID,
			Label:         label,
			WorkspaceRoot: workspaceRoot,
		})
		if err != nil {
			return nil, err
		}

		rootObject := &models.ObjectMeta{
			ID:               s.store.NextID("obj"),
			WorkspaceID:      workspaceRecord.ID,
			SessionID:        sessionRecord.ID,
			DatasetID:        workspaceRecord.DatasetID,
			Kind:             response.Object.Kind,
			Label:            response.Object.Label,
			BackendRef:       response.Object.BackendRef,
			NObs:             response.Object.NObs,
			NVars:            response.Object.NVars,
			State:            response.Object.State,
			InMemory:         response.Object.InMemory,
			MaterializedPath: response.Object.MaterializedPath,
			MaterializedURL:  s.pathToURL(response.Object.MaterializedPath),
			Metadata:         response.Object.Metadata,
			CreatedAt:        now,
			LastAccessedAt:   now,
		}
		s.store.SaveObject(rootObject)

		workspaceRecord.FocusObjectID = rootObject.ID
		sessionRecord.FocusObjectID = rootObject.ID
		sessionRecord.DatasetID = workspaceRecord.DatasetID

		s.store.AddMessage(&models.Message{
			ID:        s.store.NextID("msg"),
			SessionID: sessionRecord.ID,
			Role:      models.MessageSystem,
			Content:   response.Summary,
			CreatedAt: now,
		})
	} else {
		s.store.AddMessage(&models.Message{
			ID:        s.store.NextID("msg"),
			SessionID: sessionRecord.ID,
			Role:      models.MessageSystem,
			Content:   "新工作区已创建。请上传 .h5ad 文件以开始分析。",
			CreatedAt: now,
		})
	}

	workspaceRecord.UpdatedAt = now
	workspaceRecord.LastAccessedAt = now
	s.store.SaveWorkspace(workspaceRecord)

	sessionRecord.UpdatedAt = now
	sessionRecord.LastAccessedAt = now
	s.store.SaveSession(sessionRecord)

	s.publishSnapshot(sessionRecord.ID)
	return s.store.Snapshot(sessionRecord.ID)
}

func (s *Service) UploadH5AD(ctx context.Context, sessionID, filename string, content io.Reader) (*models.ObjectMeta, *models.SessionSnapshot, error) {
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到会话 %q", sessionID)
	}
	workspaceRecord, ok := s.store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到 workspace %q", sessionRecord.WorkspaceID)
	}

	safeName, err := sanitizeUploadName(filename)
	if err != nil {
		return nil, nil, err
	}

	workspaceRoot := s.workspaceRoot(sessionRecord.WorkspaceID)
	objectPath := filepath.Join(workspaceRoot, "objects", safeName)
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create object directory: %w", err)
	}

	fileHandle, err := os.Create(objectPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create upload target: %w", err)
	}
	if _, err := io.Copy(fileHandle, content); err != nil {
		_ = fileHandle.Close()
		return nil, nil, fmt.Errorf("write upload target: %w", err)
	}
	if err := fileHandle.Close(); err != nil {
		return nil, nil, fmt.Errorf("close upload target: %w", err)
	}

	runtimeResponse, err := s.runtime.LoadFile(ctx, runtime.LoadFileRequest{
		SessionID: sessionID,
		FilePath:  objectPath,
		Label:     strings.TrimSuffix(filepath.Base(safeName), filepath.Ext(safeName)),
	})
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	datasetID := s.store.NextID("ds")
	objectRecord := &models.ObjectMeta{
		ID:               s.store.NextID("obj"),
		WorkspaceID:      workspaceRecord.ID,
		SessionID:        sessionID,
		DatasetID:        datasetID,
		Kind:             runtimeResponse.Object.Kind,
		Label:            runtimeResponse.Object.Label,
		BackendRef:       runtimeResponse.Object.BackendRef,
		NObs:             runtimeResponse.Object.NObs,
		NVars:            runtimeResponse.Object.NVars,
		State:            runtimeResponse.Object.State,
		InMemory:         runtimeResponse.Object.InMemory,
		MaterializedPath: runtimeResponse.Object.MaterializedPath,
		MaterializedURL:  s.pathToURL(runtimeResponse.Object.MaterializedPath),
		Metadata:         runtimeResponse.Object.Metadata,
		CreatedAt:        now,
		LastAccessedAt:   now,
	}
	s.store.SaveObject(objectRecord)

	workspaceRecord.DatasetID = datasetID
	workspaceRecord.FocusObjectID = objectRecord.ID
	workspaceRecord.UpdatedAt = now
	workspaceRecord.LastAccessedAt = now
	s.store.SaveWorkspace(workspaceRecord)

	sessionRecord.DatasetID = datasetID
	sessionRecord.FocusObjectID = objectRecord.ID
	sessionRecord.UpdatedAt = now
	sessionRecord.LastAccessedAt = now
	s.store.SaveSession(sessionRecord)

	s.store.AddMessage(&models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		Role:      models.MessageSystem,
		Content:   runtimeResponse.Summary,
		CreatedAt: now,
	})

	s.publishSnapshot(sessionID)
	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return nil, nil, err
	}
	return objectRecord, snapshot, nil
}

func (s *Service) RegisterExternalArtifact(ctx context.Context, sessionID string, kind models.ArtifactKind, filename, contentType, title, summary string, content io.Reader) (*models.Artifact, *models.SessionSnapshot, error) {
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到会话 %q", sessionID)
	}
	workspaceRecord, ok := s.store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到 workspace %q", sessionRecord.WorkspaceID)
	}

	safeName, err := sanitizeArtifactName(filename)
	if err != nil {
		return nil, nil, err
	}

	workspaceRoot := s.workspaceRoot(sessionRecord.WorkspaceID)
	artifactPath := filepath.Join(workspaceRoot, "artifacts", safeName)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create artifact directory: %w", err)
	}

	fileHandle, err := os.Create(artifactPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create artifact target: %w", err)
	}
	if _, err := io.Copy(fileHandle, content); err != nil {
		_ = fileHandle.Close()
		return nil, nil, fmt.Errorf("write artifact target: %w", err)
	}
	if err := fileHandle.Close(); err != nil {
		return nil, nil, fmt.Errorf("close artifact target: %w", err)
	}

	now := time.Now().UTC()
	record := &models.Artifact{
		ID:          s.store.NextID("artifact"),
		WorkspaceID: workspaceRecord.ID,
		SessionID:   sessionRecord.ID,
		ObjectID:    sessionRecord.FocusObjectID,
		Kind:        kind,
		Title:       strings.TrimSpace(title),
		Path:        artifactPath,
		URL:         s.pathToURL(artifactPath),
		ContentType: strings.TrimSpace(contentType),
		Summary:     strings.TrimSpace(summary),
		CreatedAt:   now,
	}
	if record.Title == "" {
		record.Title = strings.TrimSuffix(filepath.Base(safeName), filepath.Ext(safeName))
	}
	s.store.SaveArtifact(record)

	sessionRecord.UpdatedAt = now
	sessionRecord.LastAccessedAt = now
	s.store.SaveSession(sessionRecord)

	workspaceRecord.UpdatedAt = now
	workspaceRecord.LastAccessedAt = now
	s.store.SaveWorkspace(workspaceRecord)

	s.publishSnapshot(sessionID)
	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return nil, nil, err
	}
	return record, snapshot, nil
}

func (s *Service) SubmitMessage(ctx context.Context, sessionID, content string) (*models.Job, *models.SessionSnapshot, error) {
	return s.SubmitMessageWithArtifacts(ctx, sessionID, content, nil)
}

func (s *Service) RetryJob(ctx context.Context, jobID, overrideContent string) (*models.Job, *models.SessionSnapshot, error) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到任务 %q", jobID)
	}
	if job.Status == models.JobRunning || job.Status == models.JobQueued {
		return nil, nil, fmt.Errorf("任务仍在执行中，无法重发")
	}
	msg, ok := s.store.GetMessage(job.SessionID, job.MessageID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到原始消息 %q", job.MessageID)
	}
	nextContent := strings.TrimSpace(overrideContent)
	if nextContent == "" {
		nextContent = msg.Content
	}

	// Delete the old assistant messages, job, and user message to avoid
	// duplicates — SubmitMessage will re-create the user message.
	s.store.DeleteMessagesByJobID(job.SessionID, jobID)
	s.store.DeleteMessage(job.SessionID, job.MessageID)
	s.store.DeleteJob(jobID)
	if job.TurnID != "" {
		s.store.DeleteTurn(job.TurnID)
	}
	s.publishSnapshot(job.SessionID)

	return s.SubmitMessage(ctx, job.SessionID, nextContent)
}

func (s *Service) RegenerateResponse(ctx context.Context, jobID string) (*models.Job, *models.SessionSnapshot, error) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到任务 %q", jobID)
	}
	if job.Status == models.JobRunning || job.Status == models.JobQueued {
		return nil, nil, fmt.Errorf("任务仍在执行中，无法重新生成")
	}
	msg, ok := s.store.GetMessage(job.SessionID, job.MessageID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到原始消息 %q", job.MessageID)
	}

	// Remove the old assistant messages, job, and the original user message
	// to avoid duplicates — SubmitMessage will re-create the user message.
	s.store.DeleteMessagesByJobID(job.SessionID, jobID)
	s.store.DeleteMessage(job.SessionID, job.MessageID)
	s.store.DeleteJob(jobID)
	if job.TurnID != "" {
		s.store.DeleteTurn(job.TurnID)
	}
	s.publishSnapshot(job.SessionID)

	return s.SubmitMessage(ctx, job.SessionID, msg.Content)
}

func (s *Service) CancelJob(_ context.Context, jobID string) (*models.Job, *models.SessionSnapshot, error) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到任务 %q", jobID)
	}
	if job.Status != models.JobQueued && job.Status != models.JobRunning {
		return nil, nil, fmt.Errorf("只能停止排队中或运行中的任务")
	}
	if !s.requestJobCancel(jobID) {
		return nil, nil, fmt.Errorf("当前任务暂时无法停止，请稍后重试")
	}

	cancelSummary := ""
	cancelWarn := ""
	if s.runtime != nil {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		response, err := s.runtime.CancelExecution(cancelCtx, runtime.CancelExecutionRequest{
			SessionID: job.SessionID,
		})
		switch {
		case err != nil:
			cancelWarn = fmt.Sprintf("运行时强制停止未确认：%v", err)
		case response != nil:
			cancelSummary = strings.TrimSpace(response.Summary)
		}
	}

	currentJob, ok := s.store.GetJob(jobID)
	if ok && (currentJob.Status == models.JobQueued || currentJob.Status == models.JobRunning) {
		currentJob.Summary = "正在停止当前任务..."
		detail := "已收到停止请求，正在终止当前任务。"
		if cancelSummary != "" {
			detail = cancelSummary
		}
		if cancelWarn != "" {
			detail = joinCheckpointSummary(detail, cancelWarn)
		}
		s.appendJobCheckpoint(
			currentJob,
			"execution",
			"muted",
			"停止请求",
			"正在停止",
			detail,
		)
		s.store.SaveJob(currentJob)
		s.publishJob(currentJob.SessionID, currentJob.ID)
		s.publishSnapshot(currentJob.SessionID)
		job = currentJob
	}

	snapshot, err := s.store.Snapshot(job.SessionID)
	if err != nil {
		return nil, nil, err
	}
	return job, snapshot, nil
}

func (s *Service) SubmitMessageWithArtifacts(ctx context.Context, sessionID, content string, inputArtifacts []*models.Artifact) (*models.Job, *models.SessionSnapshot, error) {
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到会话 %q", sessionID)
	}
	if activeJob := s.activeSessionJob(sessionID); activeJob != nil {
		return nil, nil, fmt.Errorf("当前已有任务正在运行，请先等待完成或停止当前任务")
	}

	trimmedContent := strings.TrimSpace(content)
	now := time.Now().UTC()
	turn := &models.Turn{
		ID:        s.store.NextID("turn"),
		SessionID: sessionID,
		Status:    models.TurnPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	message := &models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		TurnID:    turn.ID,
		Role:      models.MessageUser,
		Content:   trimmedContent,
		CreatedAt: now,
	}
	turn.UserMessageID = message.ID
	s.store.AddMessage(message)
	s.store.SaveTurn(turn)

	sessionRecord.UpdatedAt = now
	sessionRecord.LastAccessedAt = now
	s.store.SaveSession(sessionRecord)

	planningRequest, err := s.buildPlanningRequestWithInputsAndTurn(sessionRecord, trimmedContent, inputArtifacts, turn)
	if err != nil {
		return nil, nil, err
	}

	resolvedTurn, resolveErr := s.resolveTurn(ctx, planningRequest)
	if resolveErr != nil {
		resolvedTurn = normalizeTurnResolveResult(planningRequest, buildFallbackTurnResolveResult(planningRequest))
	}
	turn.Strategy = resolvedTurn.Strategy
	turn.Contract = cloneTurnContractForPlanning(resolvedTurn.Contract)
	if strings.TrimSpace(resolvedTurn.Summary) != "" {
		turn.Summary = strings.TrimSpace(resolvedTurn.Summary)
	}
	s.store.SaveTurn(turn)

	switch resolvedTurn.Strategy {
	case models.TurnStrategyAnswerText:
		answer := strings.TrimSpace(resolvedTurn.Answer)
		if answer == "" {
			answer = turn.Summary
		}
		if answer == "" {
			answer = "本次请求已完成。"
		}
		resultRefs := selectTurnResultRefsFromDecision(resolvedTurn)
		if len(resultRefs) == 0 {
			resultRefs = []models.TurnResultRef{{
				Kind: models.TurnResultText,
				Text: answer,
			}}
		}
		return nil, s.finishTurnWithoutJob(sessionRecord, turn, answer, resultRefs, models.TurnFulfilled), nil
	case models.TurnStrategyReuseExistingArtifact:
		snapshot, err := s.store.Snapshot(sessionID)
		if err != nil {
			return nil, nil, err
		}
		resultRefs := selectTurnResultRefs(snapshot, resolvedTurn.ResultRefs)
		if len(resultRefs) > 0 {
			answer := strings.TrimSpace(resolvedTurn.Answer)
			if answer == "" {
				answer = defaultReuseTurnAnswer(resultRefs, trimmedContent)
			}
			return nil, s.finishTurnWithoutJob(sessionRecord, turn, answer, resultRefs, models.TurnFulfilled), nil
		}
		turn.Strategy = models.TurnStrategyExecute
		if strings.TrimSpace(turn.Summary) == "" {
			turn.Summary = "当前还没有可直接复用的结果，转入执行。"
		}
		s.store.SaveTurn(turn)
	case models.TurnStrategyAskClarification:
		answer := strings.TrimSpace(resolvedTurn.Answer)
		if answer == "" {
			answer = "请再具体说明你希望我交付的结果。"
		}
		return nil, s.finishTurnWithoutJob(sessionRecord, turn, answer, nil, models.TurnPending), nil
	}

	job := &models.Job{
		ID:           s.store.NextID("job"),
		WorkspaceID:  sessionRecord.WorkspaceID,
		SessionID:    sessionID,
		TurnID:       turn.ID,
		MessageID:    message.ID,
		Status:       models.JobQueued,
		CurrentPhase: models.JobPhaseInvestigate,
		Summary:      defaultTurnExecutionSummary(turn),
		CreatedAt:    now,
	}
	turn.JobID = job.ID
	turn.UpdatedAt = now
	s.store.SaveTurn(turn)
	initializeInvestigationPhases(job, time.Now().UTC())
	s.store.SaveJob(job)
	s.publishJob(sessionID, job.ID)
	s.publishSnapshot(sessionID)

	jobCtx, cancel := context.WithCancel(context.Background())
	s.setJobCancel(job.ID, cancel)
	go s.runJob(jobCtx, sessionID, job.ID, trimmedContent, inputArtifacts)

	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return nil, nil, err
	}
	return job, snapshot, nil
}

func (s *Service) resolveTurn(ctx context.Context, request PlanningRequest) (*TurnResolveResult, error) {
	if s.answerer == nil {
		return normalizeTurnResolveResult(request, buildFallbackTurnResolveResult(request)), nil
	}
	resolved, err := s.answerer.ResolveTurn(ctx, request)
	if err != nil {
		return nil, err
	}
	return normalizeTurnResolveResult(request, resolved), nil
}

func selectTurnResultRefsFromDecision(result *TurnResolveResult) []models.TurnResultRef {
	if result == nil || len(result.ResultRefs) == 0 {
		return nil
	}
	return uniqueTurnResultRefs(result.ResultRefs)
}

func (s *Service) syncTurnResultRefs(sessionID string, turn *models.Turn, job *models.Job, updatedAt time.Time) {
	if s == nil || turn == nil || job == nil {
		return
	}
	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return
	}
	turn.ResultRefs = deriveTurnResultRefsFromSnapshot(turn, job, snapshot)
	turn.UpdatedAt = updatedAt
	s.store.SaveTurn(turn)
}

func defaultTurnExecutionSummary(turn *models.Turn) string {
	if turn == nil {
		return "当前需要进一步执行后才能完成请求。"
	}
	if strings.TrimSpace(turn.Summary) != "" {
		return strings.TrimSpace(turn.Summary)
	}
	switch turn.Contract.DeliverableKind {
	case models.TurnDeliverablePlot:
		return "当前需要生成图像结果后才能完成请求。"
	case models.TurnDeliverableFile:
		return "当前需要生成导出文件后才能完成请求。"
	case models.TurnDeliverableTable:
		return "当前需要生成表格结果后才能完成请求。"
	case models.TurnDeliverableObject:
		return "当前需要生成新的对象结果后才能完成请求。"
	default:
		return "当前需要进一步执行后才能完成请求。"
	}
}

func (s *Service) finishTurnWithoutJob(sessionRecord *models.Session, turn *models.Turn, answer string, resultRefs []models.TurnResultRef, status models.TurnStatus) *models.SessionSnapshot {
	if sessionRecord == nil || turn == nil {
		return nil
	}
	finishedAt := time.Now().UTC()
	assistant := &models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionRecord.ID,
		TurnID:    turn.ID,
		Role:      models.MessageAssistant,
		Content:   strings.TrimSpace(answer),
		CreatedAt: finishedAt,
	}

	turn.AssistantMessageID = assistant.ID
	turn.Status = status
	turn.ResultRefs = uniqueTurnResultRefs(resultRefs)
	if status == models.TurnFulfilled || status == models.TurnFailed || status == models.TurnCanceled {
		turn.FinishedAt = &finishedAt
	}
	turn.UpdatedAt = finishedAt
	if strings.TrimSpace(turn.Summary) == "" {
		turn.Summary = strings.TrimSpace(answer)
	}
	s.store.SaveTurn(turn)
	s.store.AddMessage(assistant)

	sessionRecord.UpdatedAt = finishedAt
	sessionRecord.LastAccessedAt = finishedAt
	s.store.SaveSession(sessionRecord)

	s.publishSnapshot(sessionRecord.ID)
	snapshot, err := s.store.Snapshot(sessionRecord.ID)
	if err != nil {
		return nil
	}
	return snapshot
}

func (s *Service) runJob(ctx context.Context, sessionID, jobID, message string, inputArtifacts []*models.Artifact) {
	defer s.clearJobCancel(jobID)
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
		return
	}

	job, ok := s.store.GetJob(jobID)
	if !ok {
		return
	}
	turn, _ := s.store.GetTurn(job.TurnID)
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return
	}
	workspaceRecord, ok := s.store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
			return
		}
		s.failJob(sessionID, jobID, fmt.Errorf("未找到 workspace %q", sessionRecord.WorkspaceID))
		return
	}

	startedAt := time.Now().UTC()
	job.Status = models.JobRunning
	job.StartedAt = &startedAt
	startJobPhase(job, models.JobPhaseInvestigate, "正在规划并收集与问题相关的信息。", nil)
	s.store.SaveJob(job)
	s.publishJob(sessionID, jobID)
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
		return
	}

	planningRequest, err := s.buildPlanningRequestWithInputs(sessionRecord, message, inputArtifacts)
	if err != nil {
		if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
			return
		}
		s.failJob(sessionID, jobID, fmt.Errorf("规划上下文构建失败：%w", err))
		return
	}
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
		return
	}

	plan, err := s.buildExecutablePlan(ctx, planningRequest)
	if err != nil {
		if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) || errors.Is(err, context.Canceled) {
			s.cancelJobExecution(sessionID, jobID)
			return
		}
		s.failJob(sessionID, jobID, fmt.Errorf("规划器执行失败：%w", err))
		return
	}

	job.Plan = &plan
	job.Summary = fmt.Sprintf("已生成 %d 步信息收集计划。", len(plan.Steps))
	updateJobPhaseSummary(job, models.JobPhaseInvestigate, job.Summary)
	s.appendJobCheckpoint(
		job,
		"planning",
		"muted",
		"初始规划",
		"已生成计划",
		fmt.Sprintf("已生成初始执行计划，共 %d 步。", len(plan.Steps)),
	)
	s.store.SaveJob(job)
	s.publishJob(sessionID, jobID)
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
		return
	}

	prevObjectID := sessionRecord.FocusObjectID
	stepSummaries := make([]string, 0, len(plan.Steps))
	pendingPlan := clonePlan(plan)
	completionReason := ""
	lastIncompleteReason := ""
	jobCompleted := false

	for len(pendingPlan.Steps) > 0 {
		if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
			return
		}

		step := pendingPlan.Steps[0]
		stepResult := models.JobStep{
			ID:             step.ID,
			Skill:          step.Skill,
			TargetObjectID: step.TargetObjectID,
			Params:         cloneParams(step.Params),
			Status:         models.JobRunning,
			StartedAt:      time.Now().UTC(),
		}

		targetObject, err := s.resolveTargetObject(sessionRecord, prevObjectID, step.TargetObjectID)
		if err != nil {
			if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
				return
			}
			stepResult.Status = models.JobFailed
			stepResult.Summary = err.Error()
			finishedAt := time.Now().UTC()
			stepResult.FinishedAt = &finishedAt
			job.Steps = append(job.Steps, stepResult)
			s.store.SaveJob(job)
			s.failJob(sessionID, jobID, err)
			return
		}
		if targetObject != nil {
			stepResult.ResolvedTargetObjectID = targetObject.ID
			targetObject.LastAccessedAt = time.Now().UTC()
			s.store.SaveObject(targetObject)
		}
		if err := s.ensureRuntimeObject(ctx, sessionRecord.ID, targetObject); err != nil {
			if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) || errors.Is(err, context.Canceled) {
				s.cancelJobExecution(sessionID, jobID)
				return
			}
			stepResult.Status = models.JobFailed
			stepResult.Summary = err.Error()
			finishedAt := time.Now().UTC()
			stepResult.FinishedAt = &finishedAt
			job.Steps = append(job.Steps, stepResult)
			s.store.SaveJob(job)
			s.failJob(sessionID, jobID, err)
			return
		}

		execParams := step.Params
		if step.Skill == "write_method" {
			execParams = s.injectAnalysisHistory(sessionID, jobID, execParams)
		}

		response, err := s.runtime.Execute(ctx, runtime.ExecuteRequest{
			SessionID:        sessionID,
			RequestID:        jobID + ":" + step.ID,
			Skill:            step.Skill,
			TargetBackendRef: backendRef(targetObject),
			Params:           execParams,
			WorkspaceRoot:    s.workspaceRoot(sessionRecord.WorkspaceID),
		})
		if err != nil {
			if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) || errors.Is(err, context.Canceled) {
				s.cancelJobExecution(sessionID, jobID)
				return
			}
			stepResult.Status = models.JobFailed
			stepResult.Summary = err.Error()
			finishedAt := time.Now().UTC()
			stepResult.FinishedAt = &finishedAt
			job.Steps = append(job.Steps, stepResult)
			s.store.SaveJob(job)
			s.failJob(sessionID, jobID, err)
			return
		}

		focusObjectID := sessionRecord.FocusObjectID
		if response.Object != nil {
			newObject := &models.ObjectMeta{
				ID:               s.store.NextID("obj"),
				WorkspaceID:      sessionRecord.WorkspaceID,
				SessionID:        sessionID,
				DatasetID:        workspaceRecord.DatasetID,
				ParentID:         objectID(targetObject),
				Kind:             response.Object.Kind,
				Label:            response.Object.Label,
				BackendRef:       response.Object.BackendRef,
				NObs:             response.Object.NObs,
				NVars:            response.Object.NVars,
				State:            response.Object.State,
				InMemory:         response.Object.InMemory,
				MaterializedPath: response.Object.MaterializedPath,
				MaterializedURL:  s.pathToURL(response.Object.MaterializedPath),
				Metadata:         response.Object.Metadata,
				CreatedAt:        time.Now().UTC(),
				LastAccessedAt:   time.Now().UTC(),
			}
			s.store.SaveObject(newObject)
			stepResult.OutputObjectID = newObject.ID
			prevObjectID = newObject.ID
			focusObjectID = newObject.ID
			sessionRecord.FocusObjectID = newObject.ID
			workspaceRecord.FocusObjectID = newObject.ID
		}

		artifactTargetID := focusObjectID
		if stepResult.OutputObjectID != "" {
			artifactTargetID = stepResult.OutputObjectID
		} else if targetObject != nil {
			artifactTargetID = targetObject.ID
		}
		for _, artifact := range response.Artifacts {
			record := &models.Artifact{
				ID:          s.store.NextID("artifact"),
				WorkspaceID: sessionRecord.WorkspaceID,
				SessionID:   sessionID,
				ObjectID:    artifactTargetID,
				JobID:       jobID,
				Kind:        artifact.Kind,
				Title:       artifact.Title,
				Path:        artifact.Path,
				URL:         s.pathToURL(artifact.Path),
				ContentType: artifact.ContentType,
				Summary:     artifact.Summary,
				CreatedAt:   time.Now().UTC(),
			}
			s.store.SaveArtifact(record)
			stepResult.ArtifactIDs = append(stepResult.ArtifactIDs, record.ID)
		}
		if len(stepResult.ArtifactIDs) > 0 {
			if stepResult.Metadata == nil {
				stepResult.Metadata = make(map[string]any, 1)
			}
			stepResult.Metadata["artifact_ids"] = append([]string(nil), stepResult.ArtifactIDs...)
		}

		stepResult.Status = models.JobSucceeded
		stepResult.Summary = response.Summary
		stepResult.Facts = cloneParams(response.Facts)
		if len(response.Metadata) > 0 {
			if stepResult.Metadata == nil {
				stepResult.Metadata = make(map[string]any, len(response.Metadata))
			}
			for key, value := range response.Metadata {
				stepResult.Metadata[key] = value
			}
		}
		finishedAt := time.Now().UTC()
		stepResult.FinishedAt = &finishedAt
		job.Steps = append(job.Steps, stepResult)
		stepSummaries = append(stepSummaries, response.Summary)
		updateJobPhaseSummary(job, models.JobPhaseInvestigate, fmt.Sprintf("已完成 %d/%d 个信息收集步骤。", len(job.Steps), len(job.Steps)+len(pendingPlan.Steps)-1))

		sessionRecord.UpdatedAt = finishedAt
		sessionRecord.LastAccessedAt = finishedAt
		s.store.SaveSession(sessionRecord)
		workspaceRecord.UpdatedAt = finishedAt
		workspaceRecord.LastAccessedAt = finishedAt
		s.store.SaveWorkspace(workspaceRecord)
		if turn != nil {
			if strings.TrimSpace(turn.Summary) == "" && strings.TrimSpace(response.Summary) != "" {
				turn.Summary = strings.TrimSpace(response.Summary)
			}
			s.syncTurnResultRefs(sessionID, turn, job, finishedAt)
		}
		if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
			return
		}

		evaluation, evalErr := s.evaluateCompletion(ctx, sessionRecord, job, message, inputArtifacts)
		switch {
		case s.finishCanceledJobIfNeeded(ctx, sessionID, jobID):
			return
		case evalErr != nil:
			lastIncompleteReason = ""
			s.appendJobCheckpoint(
				job,
				"completion",
				"warn",
				"完成判定",
				"跳过判定",
				"完成判定暂时失败，继续按现有计划执行。",
			)
		case evaluation != nil && evaluation.Completed:
			lastIncompleteReason = ""
			s.appendJobCheckpoint(
				job,
				"completion",
				"ok",
				"完成判定",
				"已满足请求",
				defaultCheckpointSummary(evaluation.Reason, "执行结果已满足当前请求。"),
			)
			if strings.TrimSpace(evaluation.Reason) != "" {
				completionReason = evaluation.Reason
				job.Summary = evaluation.Reason
			}
			mergedPlan := mergeExecutedAndPendingPlan(job.Steps, models.Plan{})
			job.Plan = &mergedPlan
			s.store.SaveJob(job)
			s.publishJob(sessionID, jobID)
			s.publishSnapshot(sessionID)
			jobCompleted = true
		case evaluation != nil:
			lastIncompleteReason = strings.TrimSpace(evaluation.Reason)
			s.appendJobCheckpoint(
				job,
				"completion",
				"warn",
				"完成判定",
				"继续执行",
				defaultCheckpointSummary(evaluation.Reason, "当前请求尚未完成，需要继续执行或重规划。"),
			)
		}
		if jobCompleted {
			break
		}

		remainingPlan := clonePlanWithSteps(pendingPlan.Steps[1:])
		replannedPlan, replanErr := s.replanRemainingSteps(ctx, sessionRecord, job, message, inputArtifacts)
		switch {
		case s.finishCanceledJobIfNeeded(ctx, sessionID, jobID):
			return
		case replanErr == nil && len(replannedPlan.Steps) > 0:
			pendingPlan = replannedPlan
			if plansEquivalent(replannedPlan, remainingPlan) {
				s.appendJobCheckpoint(
					job,
					"replan",
					"muted",
					"检查点重规划",
					"计划不变",
					fmt.Sprintf("检查点已确认后续 %d 步仍可继续执行。", len(replannedPlan.Steps)),
				)
			} else {
				s.appendJobCheckpoint(
					job,
					"replan",
					"ok",
					"检查点重规划",
					"已更新计划",
					fmt.Sprintf("已根据最新对象状态更新剩余计划，当前还有 %d 步待执行。", len(replannedPlan.Steps)),
				)
			}
		case replanErr != nil && len(remainingPlan.Steps) > 0:
			pendingPlan = remainingPlan
			s.appendJobCheckpoint(
				job,
				"replan",
				"warn",
				"检查点重规划",
				"沿用原计划",
				joinCheckpointSummary(replanErr.Error(), "检查点重规划失败，继续沿用原剩余计划。"),
			)
		case replanErr == nil && len(remainingPlan.Steps) > 0:
			pendingPlan = remainingPlan
			s.appendJobCheckpoint(
				job,
				"replan",
				"muted",
				"检查点重规划",
				"沿用原计划",
				"检查点未生成新的剩余步骤，继续沿用原计划。",
			)
		default:
			pendingPlan = remainingPlan
		}
		mergedPlan := mergeExecutedAndPendingPlan(job.Steps, pendingPlan)
		job.Plan = &mergedPlan
		s.store.SaveJob(job)
		s.publishJob(sessionID, jobID)
		s.publishSnapshot(sessionID)
	}
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
		return
	}

	finishedAt := time.Now().UTC()
	job.FinishedAt = &finishedAt
	switch {
	case jobCompleted:
		job.Status = models.JobSucceeded
		job.Summary = completionReason
	case lastIncompleteReason != "":
		job.Status = models.JobIncomplete
		job.Summary = lastIncompleteReason
	case completionReason != "":
		job.Status = models.JobSucceeded
		job.Summary = completionReason
	default:
		job.Status = models.JobSucceeded
		job.Summary = strings.Join(stepSummaries, " ")
	}
	if turn != nil {
		s.syncTurnResultRefs(sessionID, turn, job, finishedAt)
		if strings.TrimSpace(turn.Summary) == "" {
			turn.Summary = strings.TrimSpace(job.Summary)
		}
		s.store.SaveTurn(turn)
	}
	completeJobPhase(job, models.JobPhaseInvestigate, defaultPhaseCompletionSummary(job))
	startJobPhase(job, models.JobPhaseRespond, "正在确认收集结果并生成最终回答。", nil)
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
		return
	}

	responseRequest, responseErr := s.buildResponseComposeRequest(sessionRecord, message, job, inputArtifacts)
	var composed *ResponseComposeResult
	if responseErr == nil && s.answerer != nil {
		composed, responseErr = s.answerer.BuildInvestigationResponse(ctx, responseRequest)
	}
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) || errors.Is(responseErr, context.Canceled) {
		s.cancelJobExecution(sessionID, jobID)
		return
	}
	if responseErr != nil || composed == nil || strings.TrimSpace(composed.Answer) == "" {
		composed, _ = NewNoopAnswerer().BuildInvestigationResponse(ctx, responseRequest)
	}
	finalAnswer := ""
	if composed != nil {
		finalAnswer = strings.TrimSpace(composed.Answer)
		if strings.TrimSpace(composed.Summary) != "" {
			job.Summary = strings.TrimSpace(composed.Summary)
		}
	}
	if finalAnswer == "" {
		finalAnswer = job.Summary
	}
	if s.finishCanceledJobIfNeeded(ctx, sessionID, jobID) {
		return
	}
	completeJobPhase(job, models.JobPhaseRespond, "已根据收集到的信息生成最终回答。")
	s.store.SaveJob(job)

	assistantMessage := &models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		TurnID:    job.TurnID,
		JobID:     jobID,
		Role:      models.MessageAssistant,
		Content:   finalAnswer,
		CreatedAt: finishedAt,
	}
	s.store.AddMessage(assistantMessage)

	if turn != nil {
		turn.AssistantMessageID = assistantMessage.ID
		s.syncTurnResultRefs(sessionID, turn, job, finishedAt)
		turn.FinishedAt = &finishedAt
		if turnMeetsCompletionCriteria(turn, turn.ResultRefs) {
			turn.Status = models.TurnFulfilled
		} else {
			turn.Status = models.TurnFailed
		}
		if strings.TrimSpace(job.Summary) != "" {
			turn.Summary = strings.TrimSpace(job.Summary)
		} else if strings.TrimSpace(finalAnswer) != "" {
			turn.Summary = strings.TrimSpace(finalAnswer)
		}
		s.store.SaveTurn(turn)
	}

	sessionRecord.UpdatedAt = finishedAt
	sessionRecord.LastAccessedAt = finishedAt
	s.store.SaveSession(sessionRecord)
	s.publishJob(sessionID, jobID)
	s.publishSnapshot(sessionID)
}

func (s *Service) evaluateCompletion(ctx context.Context, sessionRecord *models.Session, job *models.Job, message string, inputArtifacts []*models.Artifact) (*CompletionEvaluation, error) {
	if s == nil || s.evaluator == nil {
		return nil, nil
	}
	request, err := s.buildEvaluationRequest(sessionRecord, message, job, inputArtifacts)
	if err != nil {
		return nil, err
	}
	return s.evaluator.Evaluate(ctx, request)
}

func (s *Service) replanRemainingSteps(ctx context.Context, sessionRecord *models.Session, job *models.Job, message string, inputArtifacts []*models.Artifact) (models.Plan, error) {
	request, err := s.buildExecutionPlanningRequest(sessionRecord, message, job, inputArtifacts)
	if err != nil {
		return models.Plan{}, err
	}
	plan, err := s.buildExecutablePlan(ctx, request)
	if err != nil {
		return models.Plan{}, err
	}
	return trimCompletedPlanPrefix(plan, job.Steps), nil
}

func (s *Service) buildExecutablePlan(ctx context.Context, request PlanningRequest) (models.Plan, error) {
	if err := s.refreshSkills(); err != nil {
		return models.Plan{}, err
	}
	plan, err := s.planner.Plan(ctx, request)
	if err != nil {
		return models.Plan{}, err
	}

	plan = NormalizePlan(plan)
	plan = applyRecentPlotContext(request, plan)
	validateErr := s.skills.ValidatePlan(plan)
	if validateErr == nil {
		return plan, nil
	}
	return models.Plan{}, fmt.Errorf("执行计划不合法：%w", validateErr)
}

func (s *Service) finishCanceledJobIfNeeded(ctx context.Context, sessionID, jobID string) bool {
	if ctx == nil || !errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	s.cancelJobExecution(sessionID, jobID)
	return true
}

func (s *Service) cancelJobExecution(sessionID, jobID string) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return
	}
	if job.Status != models.JobQueued && job.Status != models.JobRunning {
		return
	}

	finishedAt := time.Now().UTC()
	summary := "当前任务已停止。"
	job.Status = models.JobCanceled
	job.Error = ""
	job.Summary = summary
	job.FinishedAt = &finishedAt
	cancelJobPhase(job, job.CurrentPhase, summary)
	if job.CurrentPhase != models.JobPhaseRespond {
		skipJobPhase(job, models.JobPhaseRespond, "任务已取消，未生成最终回答。")
	}
	s.appendJobCheckpoint(job, "execution", "muted", "任务已停止", "已取消", "用户已停止当前任务。")
	s.store.SaveJob(job)

	assistantMessage := &models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		TurnID:    job.TurnID,
		JobID:     jobID,
		Role:      models.MessageAssistant,
		Content:   summary,
		CreatedAt: finishedAt,
	}
	s.store.AddMessage(assistantMessage)

	if turn, ok := s.store.GetTurn(job.TurnID); ok {
		turn.Status = models.TurnCanceled
		turn.AssistantMessageID = assistantMessage.ID
		turn.UpdatedAt = finishedAt
		turn.FinishedAt = &finishedAt
		turn.Summary = summary
		s.store.SaveTurn(turn)
	}

	if sessionRecord, ok := s.store.GetSession(sessionID); ok {
		sessionRecord.UpdatedAt = finishedAt
		sessionRecord.LastAccessedAt = finishedAt
		s.store.SaveSession(sessionRecord)
	}

	s.publishJob(sessionID, jobID)
	s.publishSnapshot(sessionID)
}

func (s *Service) failJob(sessionID, jobID string, err error) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return
	}
	finishedAt := time.Now().UTC()
	publicError := err.Error()
	if s.answerer != nil {
		publicError = s.answerer.BuildFailureAnswer(err)
	}
	job.Status = models.JobFailed
	job.Error = publicError
	job.FinishedAt = &finishedAt
	failJobPhase(job, job.CurrentPhase, publicError, map[string]any{"raw_error": err.Error()})
	skipJobPhase(job, models.JobPhaseRespond, "本次执行在信息收集阶段终止，未进入最终回答。")
	s.appendJobCheckpointWithMetadata(job, "execution", "warn", "执行失败", "已终止", publicError, map[string]any{
		"raw_error": err.Error(),
	})
	s.store.SaveJob(job)

	assistantMessage := &models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		TurnID:    job.TurnID,
		JobID:     jobID,
		Role:      models.MessageAssistant,
		Content:   publicError,
		CreatedAt: finishedAt,
	}
	s.store.AddMessage(assistantMessage)

	if turn, ok := s.store.GetTurn(job.TurnID); ok {
		turn.Status = models.TurnFailed
		turn.AssistantMessageID = assistantMessage.ID
		turn.UpdatedAt = finishedAt
		turn.FinishedAt = &finishedAt
		turn.Summary = publicError
		s.store.SaveTurn(turn)
	}

	s.publishJob(sessionID, jobID)
	s.publishSnapshot(sessionID)
}

func (s *Service) resolveTargetObject(sessionRecord *models.Session, prevObjectID, token string) (*models.ObjectMeta, error) {
	requireInWorkspace := func(objectID string) (*models.ObjectMeta, error) {
		object, err := s.getRequiredObject(objectID)
		if err != nil {
			return nil, err
		}
		if object.WorkspaceID != "" && object.WorkspaceID != sessionRecord.WorkspaceID {
			return nil, fmt.Errorf("对象 %q 不属于当前 workspace", objectID)
		}
		return object, nil
	}

	roles, err := s.resolveSessionObjectRoles(sessionRecord)
	if err != nil {
		return nil, err
	}

	switch token {
	case "", "$focus":
		if roles.FocusObject == nil {
			return nil, fmt.Errorf("未找到当前 focus 对象")
		}
		return requireInWorkspace(roles.FocusObject.ID)
	case "$global":
		if roles.GlobalObject == nil {
			return nil, fmt.Errorf("未找到当前 lineage 的全量对象")
		}
		return requireInWorkspace(roles.GlobalObject.ID)
	case "$root":
		if roles.RootObject == nil {
			return nil, fmt.Errorf("未找到当前 lineage root 对象")
		}
		return requireInWorkspace(roles.RootObject.ID)
	case "$prev":
		return requireInWorkspace(prevObjectID)
	default:
		return requireInWorkspace(token)
	}
}

func (s *Service) resolveSessionObjectRoles(sessionRecord *models.Session) (ResolvedObjectRoles, error) {
	if sessionRecord == nil {
		return ResolvedObjectRoles{}, nil
	}
	snapshot, err := s.store.Snapshot(sessionRecord.ID)
	if err != nil {
		return ResolvedObjectRoles{}, err
	}
	return resolveObjectRoles(snapshot.Session, snapshot.Objects), nil
}

func (s *Service) getRequiredObject(objectID string) (*models.ObjectMeta, error) {
	object, ok := s.store.GetObject(objectID)
	if !ok {
		return nil, fmt.Errorf("未找到对象 %q", objectID)
	}
	return object, nil
}

func (s *Service) publishJob(sessionID, jobID string) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return
	}
	s.store.Publish(sessionID, models.Event{
		Type:        "job_updated",
		SessionID:   sessionID,
		WorkspaceID: job.WorkspaceID,
		Timestamp:   time.Now().UTC(),
		Data:        job,
	})
}

func (s *Service) publishSnapshot(sessionID string) {
	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return
	}
	s.store.Publish(sessionID, models.Event{
		Type:        "session_updated",
		SessionID:   sessionID,
		WorkspaceID: snapshot.Workspace.ID,
		Timestamp:   time.Now().UTC(),
		Data:        snapshot,
	})
}

func (s *Service) workspaceRoot(workspaceID string) string {
	return filepath.Join(s.dataRoot, "workspaces", workspaceID)
}

func (s *Service) pathToURL(path string) string {
	if path == "" {
		return ""
	}
	relative, err := filepath.Rel(s.dataRoot, path)
	if err != nil {
		return ""
	}
	return "/data/" + filepath.ToSlash(relative)
}

func backendRef(object *models.ObjectMeta) string {
	if object == nil {
		return ""
	}
	return object.BackendRef
}

func (s *Service) injectAnalysisHistory(sessionID string, currentJobID string, params map[string]any) map[string]any {
	merged := make(map[string]any, len(params)+1)
	for k, v := range params {
		merged[k] = v
	}

	jobs := s.store.ListSessionJobs(sessionID)
	history := make([]map[string]any, 0)
	for _, job := range jobs {
		if job.ID == currentJobID {
			continue
		}
		if job.Status != models.JobSucceeded {
			continue
		}
		for _, step := range job.Steps {
			if step.Status != models.JobSucceeded {
				continue
			}
			entry := map[string]any{
				"skill":   step.Skill,
				"summary": step.Summary,
			}
			if step.Params != nil {
				filtered := make(map[string]any)
				for k, v := range step.Params {
					if k == "code" {
						continue
					}
					filtered[k] = v
				}
				if len(filtered) > 0 {
					entry["params"] = filtered
				}
			}
			history = append(history, entry)
		}
	}

	merged["_analysis_history"] = history
	return merged
}

func (s *Service) ensureRuntimeObject(ctx context.Context, sessionID string, object *models.ObjectMeta) error {
	if object == nil || s.runtime == nil {
		return nil
	}

	response, err := s.runtime.EnsureObject(ctx, runtime.EnsureObjectRequest{
		SessionID: sessionID,
		Object: runtime.ObjectDescriptor{
			BackendRef:       object.BackendRef,
			Kind:             object.Kind,
			Label:            object.Label,
			NObs:             object.NObs,
			NVars:            object.NVars,
			State:            object.State,
			InMemory:         object.InMemory,
			MaterializedPath: object.MaterializedPath,
			Metadata:         cloneParams(object.Metadata),
		},
	})
	if err != nil {
		return err
	}

	refreshed := response.Object
	updated := false
	if refreshed.BackendRef != "" && refreshed.BackendRef != object.BackendRef {
		object.BackendRef = refreshed.BackendRef
		updated = true
	}
	if refreshed.Kind != "" && refreshed.Kind != object.Kind {
		object.Kind = refreshed.Kind
		updated = true
	}
	if refreshed.Label != "" && refreshed.Label != object.Label {
		object.Label = refreshed.Label
		updated = true
	}
	if refreshed.NObs > 0 && refreshed.NObs != object.NObs {
		object.NObs = refreshed.NObs
		updated = true
	}
	if refreshed.NVars > 0 && refreshed.NVars != object.NVars {
		object.NVars = refreshed.NVars
		updated = true
	}
	if refreshed.State != "" && refreshed.State != object.State {
		object.State = refreshed.State
		updated = true
	}
	if refreshed.InMemory != object.InMemory {
		object.InMemory = refreshed.InMemory
		updated = true
	}
	if refreshed.MaterializedPath != "" && refreshed.MaterializedPath != object.MaterializedPath {
		object.MaterializedPath = refreshed.MaterializedPath
		object.MaterializedURL = s.pathToURL(refreshed.MaterializedPath)
		updated = true
	}
	if !reflect.DeepEqual(refreshed.Metadata, object.Metadata) {
		object.Metadata = cloneParams(refreshed.Metadata)
		updated = true
	}
	if updated {
		s.store.SaveObject(object)
	}
	return nil
}

func (s *Service) appendJobCheckpoint(job *models.Job, kind, tone, title, label, summary string) {
	s.appendJobCheckpointWithMetadata(job, kind, tone, title, label, summary, nil)
}

func (s *Service) appendJobCheckpointWithMetadata(job *models.Job, kind, tone, title, label, summary string, metadata map[string]any) {
	if job == nil {
		return
	}
	job.Checkpoints = append(job.Checkpoints, models.JobCheckpoint{
		Kind:      kind,
		Tone:      tone,
		Title:     title,
		Label:     label,
		Summary:   summary,
		Metadata:  cloneParams(metadata),
		CreatedAt: time.Now().UTC(),
	})
}

func initializeInvestigationPhases(job *models.Job, now time.Time) {
	if job == nil {
		return
	}
	job.Phases = []models.JobPhase{
		{
			Kind:       models.JobPhaseDecide,
			Title:      "快速判断",
			Status:     models.JobPhaseCompleted,
			Summary:    "当前上下文不足以直接回答，转入信息收集。",
			StartedAt:  &now,
			FinishedAt: &now,
		},
		{
			Kind:    models.JobPhaseInvestigate,
			Title:   "信息收集",
			Status:  models.JobPhasePending,
			Summary: "等待执行。",
		},
		{
			Kind:    models.JobPhaseRespond,
			Title:   "确认与回答",
			Status:  models.JobPhasePending,
			Summary: "等待信息收集完成。",
		},
	}
}

func startJobPhase(job *models.Job, kind models.JobPhaseKind, summary string, metadata map[string]any) {
	phase := ensureJobPhase(job, kind)
	if phase == nil {
		return
	}
	now := time.Now().UTC()
	if phase.StartedAt == nil {
		phase.StartedAt = &now
	}
	phase.Status = models.JobPhaseRunning
	phase.Summary = strings.TrimSpace(summary)
	phase.Metadata = cloneParams(metadata)
	phase.FinishedAt = nil
	job.CurrentPhase = kind
}

func updateJobPhaseSummary(job *models.Job, kind models.JobPhaseKind, summary string) {
	phase := ensureJobPhase(job, kind)
	if phase == nil || strings.TrimSpace(summary) == "" {
		return
	}
	phase.Summary = strings.TrimSpace(summary)
}

func completeJobPhase(job *models.Job, kind models.JobPhaseKind, summary string) {
	phase := ensureJobPhase(job, kind)
	if phase == nil {
		return
	}
	now := time.Now().UTC()
	if phase.StartedAt == nil {
		phase.StartedAt = &now
	}
	phase.Status = models.JobPhaseCompleted
	if strings.TrimSpace(summary) != "" {
		phase.Summary = strings.TrimSpace(summary)
	}
	phase.FinishedAt = &now
	job.CurrentPhase = kind
}

func failJobPhase(job *models.Job, kind models.JobPhaseKind, summary string, metadata map[string]any) {
	phase := ensureJobPhase(job, kind)
	if phase == nil {
		return
	}
	now := time.Now().UTC()
	if phase.StartedAt == nil {
		phase.StartedAt = &now
	}
	phase.Status = models.JobPhaseFailed
	phase.Summary = strings.TrimSpace(summary)
	phase.Metadata = cloneParams(metadata)
	phase.FinishedAt = &now
	job.CurrentPhase = kind
}

func cancelJobPhase(job *models.Job, kind models.JobPhaseKind, summary string) {
	phase := ensureJobPhase(job, kind)
	if phase == nil {
		return
	}
	now := time.Now().UTC()
	if phase.StartedAt == nil {
		phase.StartedAt = &now
	}
	phase.Status = models.JobPhaseCanceled
	phase.Summary = strings.TrimSpace(summary)
	phase.FinishedAt = &now
	job.CurrentPhase = kind
}

func skipJobPhase(job *models.Job, kind models.JobPhaseKind, summary string) {
	phase := ensureJobPhase(job, kind)
	if phase == nil || phase.Status == models.JobPhaseCompleted || phase.Status == models.JobPhaseRunning || phase.Status == models.JobPhaseCanceled {
		return
	}
	now := time.Now().UTC()
	phase.Status = models.JobPhaseSkipped
	phase.Summary = strings.TrimSpace(summary)
	phase.FinishedAt = &now
}

func ensureJobPhase(job *models.Job, kind models.JobPhaseKind) *models.JobPhase {
	if job == nil {
		return nil
	}
	for i := range job.Phases {
		if job.Phases[i].Kind == kind {
			return &job.Phases[i]
		}
	}
	title := jobPhaseTitle(kind)
	job.Phases = append(job.Phases, models.JobPhase{
		Kind:   kind,
		Title:  title,
		Status: models.JobPhasePending,
	})
	return &job.Phases[len(job.Phases)-1]
}

func jobPhaseTitle(kind models.JobPhaseKind) string {
	switch kind {
	case models.JobPhaseDecide:
		return "快速判断"
	case models.JobPhaseInvestigate:
		return "信息收集"
	case models.JobPhaseRespond:
		return "确认与回答"
	default:
		return "阶段"
	}
}

func defaultPhaseCompletionSummary(job *models.Job) string {
	if job == nil {
		return ""
	}
	switch job.Status {
	case models.JobSucceeded:
		return "信息收集已完成，已具备回答所需证据。"
	case models.JobIncomplete:
		return "信息收集已结束，但当前证据仍不足以完整回答。"
	case models.JobCanceled:
		return "信息收集已停止。"
	default:
		return "信息收集阶段已结束。"
	}
}

func objectID(object *models.ObjectMeta) string {
	if object == nil {
		return ""
	}
	return object.ID
}

func (s *Service) buildPlanningRequest(sessionRecord *models.Session, message string) (PlanningRequest, error) {
	return s.buildPlanningRequestWithInputsAndTurn(sessionRecord, message, nil, nil)
}

func (s *Service) buildPlanningRequestWithInputs(sessionRecord *models.Session, message string, inputArtifacts []*models.Artifact) (PlanningRequest, error) {
	return s.buildPlanningRequestWithInputsAndTurn(sessionRecord, message, inputArtifacts, nil)
}

func (s *Service) buildPlanningRequestWithInputsAndTurn(sessionRecord *models.Session, message string, inputArtifacts []*models.Artifact, currentTurn *models.Turn) (PlanningRequest, error) {
	snapshot, err := s.store.Snapshot(sessionRecord.ID)
	if err != nil {
		return PlanningRequest{}, err
	}

	resolvedObjects := resolveObjectRoles(snapshot.Session, snapshot.Objects)
	currentTurnCopy := cloneTurnForPlanning(currentTurn)
	currentTurnID := ""
	if currentTurnCopy != nil {
		currentTurnID = currentTurnCopy.ID
	}

	return PlanningRequest{
		Message:         message,
		Session:         snapshot.Session,
		Workspace:       snapshot.Workspace,
		FocusObject:     resolvedObjects.FocusObject,
		GlobalObject:    resolvedObjects.GlobalObject,
		RootObject:      resolvedObjects.RootObject,
		Objects:         snapshot.Objects,
		InputArtifacts:  inputArtifacts,
		RecentMessages:  trimRecentMessages(snapshot.Messages, message, 6),
		RecentJobs:      trimRecentJobs(snapshot.Jobs, 3),
		RecentArtifacts: trimRecentArtifacts(snapshot.Artifacts, 4),
		RecentTurns:     trimRecentTurns(snapshot.Turns, currentTurnID, 4),
		CurrentTurn:     currentTurnCopy,
		WorkingMemory:   snapshot.WorkingMemory,
	}, nil
}

func (s *Service) buildExecutionPlanningRequest(sessionRecord *models.Session, message string, currentJob *models.Job, inputArtifacts []*models.Artifact) (PlanningRequest, error) {
	var currentTurn *models.Turn
	if currentJob != nil && currentJob.TurnID != "" {
		currentTurn, _ = s.store.GetTurn(currentJob.TurnID)
	}
	request, err := s.buildPlanningRequestWithInputsAndTurn(sessionRecord, message, inputArtifacts, currentTurn)
	if err != nil {
		return PlanningRequest{}, err
	}
	if currentJob == nil || len(currentJob.Steps) == 0 {
		return request, nil
	}
	request.RecentJobs = append(request.RecentJobs, cloneJobForPlanning(currentJob))
	return request, nil
}

func (s *Service) buildEvaluationRequest(sessionRecord *models.Session, message string, currentJob *models.Job, inputArtifacts []*models.Artifact) (EvaluationRequest, error) {
	request, err := s.buildExecutionPlanningRequest(sessionRecord, message, currentJob, inputArtifacts)
	if err != nil {
		return EvaluationRequest{}, err
	}
	return EvaluationRequest{
		Message:         request.Message,
		Session:         request.Session,
		Workspace:       request.Workspace,
		FocusObject:     request.FocusObject,
		GlobalObject:    request.GlobalObject,
		RootObject:      request.RootObject,
		Objects:         request.Objects,
		InputArtifacts:  request.InputArtifacts,
		RecentMessages:  request.RecentMessages,
		RecentJobs:      request.RecentJobs,
		RecentArtifacts: request.RecentArtifacts,
		RecentTurns:     request.RecentTurns,
		CurrentJob:      cloneJobForPlanning(currentJob),
		CurrentTurn:     cloneTurnForPlanning(request.CurrentTurn),
		WorkingMemory:   request.WorkingMemory,
	}, nil
}

func (s *Service) buildResponseComposeRequest(sessionRecord *models.Session, message string, currentJob *models.Job, inputArtifacts []*models.Artifact) (ResponseComposeRequest, error) {
	request, err := s.buildExecutionPlanningRequest(sessionRecord, message, currentJob, inputArtifacts)
	if err != nil {
		return ResponseComposeRequest{}, err
	}
	return ResponseComposeRequest{
		Message:         request.Message,
		Session:         request.Session,
		Workspace:       request.Workspace,
		FocusObject:     request.FocusObject,
		GlobalObject:    request.GlobalObject,
		RootObject:      request.RootObject,
		Objects:         request.Objects,
		InputArtifacts:  request.InputArtifacts,
		RecentMessages:  request.RecentMessages,
		RecentJobs:      request.RecentJobs,
		RecentArtifacts: request.RecentArtifacts,
		RecentTurns:     request.RecentTurns,
		CurrentJob:      cloneJobForPlanning(currentJob),
		CurrentTurn:     cloneTurnForPlanning(request.CurrentTurn),
		WorkingMemory:   request.WorkingMemory,
	}, nil
}

var uploadNamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeUploadName(name string) (string, error) {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", fmt.Errorf("上传文件名不合法")
	}

	ext := strings.ToLower(filepath.Ext(base))
	if ext != ".h5ad" && ext != ".ha5d" {
		return "", fmt.Errorf("当前仅支持 .h5ad 文件")
	}

	safe := uploadNamePattern.ReplaceAllString(base, "_")
	if safe == "" {
		return "", fmt.Errorf("上传文件名不合法")
	}
	return safe, nil
}

func sanitizeArtifactName(name string) (string, error) {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", fmt.Errorf("artifact 文件名不合法")
	}

	safe := uploadNamePattern.ReplaceAllString(base, "_")
	if safe == "" {
		return "", fmt.Errorf("artifact 文件名不合法")
	}
	return safe, nil
}

func trimRecentMessages(messages []*models.Message, currentMessage string, limit int) []*models.Message {
	if len(messages) == 0 || limit <= 0 {
		return nil
	}

	end := len(messages)
	if last := messages[end-1]; last != nil && last.Role == models.MessageUser && strings.TrimSpace(last.Content) == strings.TrimSpace(currentMessage) {
		end--
	}
	if end <= 0 {
		return nil
	}

	start := end - limit
	if start < 0 {
		start = 0
	}
	return messages[start:end]
}

func trimRecentJobs(jobs []*models.Job, limit int) []*models.Job {
	if len(jobs) == 0 || limit <= 0 {
		return nil
	}

	out := make([]*models.Job, 0, limit)
	for index := len(jobs) - 1; index >= 0 && len(out) < limit; index-- {
		job := jobs[index]
		if job == nil {
			continue
		}
		if job.Status == models.JobQueued || job.Status == models.JobRunning {
			continue
		}
		out = append(out, job)
	}
	slices.Reverse(out)
	return out
}

func trimRecentArtifacts(artifacts []*models.Artifact, limit int) []*models.Artifact {
	if len(artifacts) == 0 || limit <= 0 {
		return nil
	}

	start := len(artifacts) - limit
	if start < 0 {
		start = 0
	}
	return artifacts[start:]
}

func trimRecentTurns(turns []*models.Turn, currentTurnID string, limit int) []*models.Turn {
	if len(turns) == 0 || limit <= 0 {
		return nil
	}

	end := len(turns)
	if currentTurnID != "" && turns[end-1] != nil && turns[end-1].ID == currentTurnID {
		end--
	}
	if end <= 0 {
		return nil
	}

	start := end - limit
	if start < 0 {
		start = 0
	}
	result := make([]*models.Turn, 0, end-start)
	for _, turn := range turns[start:end] {
		result = append(result, cloneTurnForPlanning(turn))
	}
	return result
}

func clonePlan(in models.Plan) models.Plan {
	return clonePlanWithSteps(in.Steps)
}

func clonePlanWithSteps(steps []models.PlanStep) models.Plan {
	if len(steps) == 0 {
		return models.Plan{}
	}
	out := models.Plan{
		Steps: make([]models.PlanStep, len(steps)),
	}
	for i, step := range steps {
		out.Steps[i] = clonePlanStepData(step)
	}
	return out
}

func clonePlanStepData(in models.PlanStep) models.PlanStep {
	out := in
	out.Params = cloneParams(in.Params)
	if len(in.MemoryRefs) > 0 {
		out.MemoryRefs = append([]string(nil), in.MemoryRefs...)
	}
	return out
}

func cloneParams(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneJobForPlanning(in *models.Job) *models.Job {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.Steps) > 0 {
		out.Steps = make([]models.JobStep, len(in.Steps))
		for i, step := range in.Steps {
			out.Steps[i] = cloneJobStepForPlanning(step)
		}
	}
	if len(in.Phases) > 0 {
		out.Phases = make([]models.JobPhase, len(in.Phases))
		for i, phase := range in.Phases {
			out.Phases[i] = cloneJobPhaseForPlanning(phase)
		}
	}
	if len(in.Checkpoints) > 0 {
		out.Checkpoints = append([]models.JobCheckpoint(nil), in.Checkpoints...)
	}
	return &out
}

func cloneJobStepForPlanning(in models.JobStep) models.JobStep {
	out := in
	out.Params = cloneParams(in.Params)
	if len(in.ArtifactIDs) > 0 {
		out.ArtifactIDs = append([]string(nil), in.ArtifactIDs...)
	}
	if len(in.Facts) > 0 {
		out.Facts = cloneParams(in.Facts)
	}
	if len(in.Metadata) > 0 {
		out.Metadata = cloneParams(in.Metadata)
	}
	return out
}

func cloneJobPhaseForPlanning(in models.JobPhase) models.JobPhase {
	out := in
	if len(in.Metadata) > 0 {
		out.Metadata = cloneParams(in.Metadata)
	}
	return out
}

func mergeExecutedAndPendingPlan(executed []models.JobStep, pending models.Plan) models.Plan {
	merged := models.Plan{
		Steps: make([]models.PlanStep, 0, len(executed)+len(pending.Steps)),
	}
	for _, step := range executed {
		merged.Steps = append(merged.Steps, models.PlanStep{
			ID:             step.ID,
			Skill:          step.Skill,
			TargetObjectID: step.TargetObjectID,
			Params:         cloneParams(step.Params),
		})
	}
	for _, step := range pending.Steps {
		merged.Steps = append(merged.Steps, clonePlanStepData(step))
	}
	return merged
}

func trimCompletedPlanPrefix(plan models.Plan, executed []models.JobStep) models.Plan {
	if len(plan.Steps) == 0 || len(executed) == 0 {
		return clonePlan(plan)
	}

	maxDrop := min(len(plan.Steps), len(executed))
	bestDrop := 0
	for drop := maxDrop; drop > 0; drop-- {
		if executedSequenceContainsPrefix(executed, plan.Steps[:drop]) {
			bestDrop = drop
			break
		}
	}
	return clonePlanWithSteps(plan.Steps[bestDrop:])
}

func executedSequenceContainsPrefix(executed []models.JobStep, prefix []models.PlanStep) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(executed) < len(prefix) {
		return false
	}
	for start := 0; start+len(prefix) <= len(executed); start++ {
		if matchingExecutedWindow(executed[start:start+len(prefix)], prefix) {
			return true
		}
	}
	return false
}

func matchingExecutedWindow(executed []models.JobStep, prefix []models.PlanStep) bool {
	for i := range prefix {
		if !planStepMatchesJobStep(prefix[i], executed[i]) {
			return false
		}
	}
	return true
}

func planStepMatchesJobStep(planStep models.PlanStep, jobStep models.JobStep) bool {
	return planStep.Skill == jobStep.Skill &&
		planStep.TargetObjectID == jobStep.TargetObjectID &&
		mapsEqual(planStep.Params, jobStep.Params)
}

func mapsEqual(left, right map[string]any) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, ok := right[key]
		if !ok || !reflect.DeepEqual(leftValue, rightValue) {
			return false
		}
	}
	return true
}

func plansEquivalent(left, right models.Plan) bool {
	if len(left.Steps) != len(right.Steps) {
		return false
	}
	for index := range left.Steps {
		if !planStepMatchesPlanStep(left.Steps[index], right.Steps[index]) {
			return false
		}
	}
	return true
}

func planStepMatchesPlanStep(left, right models.PlanStep) bool {
	return left.Skill == right.Skill &&
		left.TargetObjectID == right.TargetObjectID &&
		mapsEqual(left.Params, right.Params)
}

func defaultCheckpointSummary(summary, fallback string) string {
	if strings.TrimSpace(summary) != "" {
		return summary
	}
	return fallback
}

func joinCheckpointSummary(primary, fallback string) string {
	primary = strings.TrimSpace(primary)
	fallback = strings.TrimSpace(fallback)
	switch {
	case primary == "":
		return fallback
	case fallback == "":
		return primary
	default:
		return primary + " " + fallback
	}
}
