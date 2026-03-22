package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
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
	dataRoot  string
}

type PlanningRequest struct {
	Message         string               `json:"message"`
	Session         *models.Session      `json:"session,omitempty"`
	Workspace       *models.Workspace    `json:"workspace,omitempty"`
	ActiveObject    *models.ObjectMeta   `json:"active_object,omitempty"`
	Objects         []*models.ObjectMeta `json:"objects,omitempty"`
	RecentMessages  []*models.Message    `json:"recent_messages,omitempty"`
	RecentJobs      []*models.Job        `json:"recent_jobs,omitempty"`
	RecentArtifacts []*models.Artifact   `json:"recent_artifacts,omitempty"`
}

type PlannerDebugPreview struct {
	PlannerMode           string          `json:"planner_mode"`
	PlanningRequest       PlanningRequest `json:"planning_request"`
	DeveloperInstructions string          `json:"developer_instructions,omitempty"`
	RequestBody           map[string]any  `json:"request_body,omitempty"`
	Note                  string          `json:"note,omitempty"`
}

type SystemStatus struct {
	SystemMode            string                `json:"system_mode"`
	Summary               string                `json:"summary"`
	PlannerMode           string                `json:"planner_mode"`
	PlannerReady          bool                  `json:"planner_ready"`
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
	return NewServiceWithEvaluator(store, skills, runtimeClient, planner, NewFakeEvaluator(), dataRoot)
}

func NewServiceWithEvaluator(store *session.Store, skills *skill.Registry, runtimeClient runtime.Service, planner Planner, evaluator Evaluator, dataRoot string) *Service {
	if evaluator == nil {
		evaluator = NewFakeEvaluator()
	}
	return &Service{
		store:     store,
		skills:    skills,
		runtime:   runtimeClient,
		planner:   planner,
		evaluator: evaluator,
		dataRoot:  dataRoot,
	}
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

func (s *Service) Status(ctx context.Context) *SystemStatus {
	if err := s.refreshSkills(); err != nil {
		return &SystemStatus{
			SystemMode:       "demo",
			Summary:          "当前处于演示模式：技能注册表加载失败。",
			PlannerMode:      s.PlannerMode(),
			PlannerReady:     s.planner != nil,
			LLMLoaded:        s.PlannerMode() == "llm",
			RuntimeConnected: false,
			Notes:            []string{err.Error()},
		}
	}

	status := &SystemStatus{
		PlannerMode:  s.PlannerMode(),
		PlannerReady: s.planner != nil,
		LLMLoaded:    s.PlannerMode() == "llm",
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

	if status.LLMLoaded && status.RealAnalysisExecution {
		status.SystemMode = "live"
		status.Summary = "当前处于正式模式：LLM 规划器已启用，分析执行为真实运行。"
		return status
	}

	status.SystemMode = "demo"
	switch {
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
	plan, _, err := s.buildExecutablePlan(ctx, PlanningRequest{Message: message})
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

func (s *Service) CreateSession(ctx context.Context, label string) (*models.SessionSnapshot, error) {
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

	response, err := s.runtime.InitSession(ctx, runtime.InitSessionRequest{
		SessionID:   sessionRecord.ID,
		DatasetID:   workspaceRecord.DatasetID,
		Label:       label,
		SessionRoot: workspaceRoot,
	})
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
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

	workspaceRecord.ActiveObjectID = rootObject.ID
	workspaceRecord.UpdatedAt = now
	workspaceRecord.LastAccessedAt = now
	s.store.SaveWorkspace(workspaceRecord)

	sessionRecord.DatasetID = workspaceRecord.DatasetID
	sessionRecord.ActiveObjectID = rootObject.ID
	sessionRecord.UpdatedAt = now
	sessionRecord.LastAccessedAt = now
	s.store.SaveSession(sessionRecord)

	s.store.AddMessage(&models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionRecord.ID,
		Role:      models.MessageSystem,
		Content:   response.Summary,
		CreatedAt: now,
	})

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
	workspaceRecord.ActiveObjectID = objectRecord.ID
	workspaceRecord.UpdatedAt = now
	workspaceRecord.LastAccessedAt = now
	s.store.SaveWorkspace(workspaceRecord)

	sessionRecord.DatasetID = datasetID
	sessionRecord.ActiveObjectID = objectRecord.ID
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

func (s *Service) SubmitMessage(ctx context.Context, sessionID, content string) (*models.Job, *models.SessionSnapshot, error) {
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return nil, nil, fmt.Errorf("未找到会话 %q", sessionID)
	}

	now := time.Now().UTC()
	message := &models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		Role:      models.MessageUser,
		Content:   strings.TrimSpace(content),
		CreatedAt: now,
	}
	s.store.AddMessage(message)

	job := &models.Job{
		ID:          s.store.NextID("job"),
		WorkspaceID: sessionRecord.WorkspaceID,
		SessionID:   sessionID,
		MessageID:   message.ID,
		Status:      models.JobQueued,
		CreatedAt:   now,
	}
	s.store.SaveJob(job)

	sessionRecord.UpdatedAt = now
	sessionRecord.LastAccessedAt = now
	s.store.SaveSession(sessionRecord)
	s.publishJob(sessionID, job.ID)
	s.publishSnapshot(sessionID)

	go s.runJob(context.Background(), sessionID, job.ID, content)

	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return nil, nil, err
	}
	return job, snapshot, nil
}

func (s *Service) runJob(ctx context.Context, sessionID, jobID, message string) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return
	}
	sessionRecord, ok := s.store.GetSession(sessionID)
	if !ok {
		return
	}
	workspaceRecord, ok := s.store.GetWorkspace(sessionRecord.WorkspaceID)
	if !ok {
		s.failJob(sessionID, jobID, fmt.Errorf("未找到 workspace %q", sessionRecord.WorkspaceID))
		return
	}

	startedAt := time.Now().UTC()
	job.Status = models.JobRunning
	job.StartedAt = &startedAt
	s.store.SaveJob(job)
	s.publishJob(sessionID, jobID)

	planningRequest, err := s.buildPlanningRequest(sessionRecord, message)
	if err != nil {
		s.failJob(sessionID, jobID, fmt.Errorf("规划上下文构建失败：%w", err))
		return
	}

	plan, fallbackNote, err := s.buildExecutablePlan(ctx, planningRequest)
	if err != nil {
		s.failJob(sessionID, jobID, fmt.Errorf("规划器执行失败：%w", err))
		return
	}

	job.Plan = &plan
	if fallbackNote != "" {
		job.Summary = fallbackNote
		s.appendJobCheckpoint(job, "planning", "warn", "初始规划", "规则兜底", fallbackNote)
	} else {
		job.Summary = "编排器已接受执行计划。"
		s.appendJobCheckpoint(
			job,
			"planning",
			"muted",
			"初始规划",
			"已生成计划",
			fmt.Sprintf("已生成初始执行计划，共 %d 步。", len(plan.Steps)),
		)
	}
	s.store.SaveJob(job)
	s.publishJob(sessionID, jobID)

	prevObjectID := sessionRecord.ActiveObjectID
	stepSummaries := make([]string, 0, len(plan.Steps))
	pendingPlan := clonePlan(plan)
	completionReason := ""

	for len(pendingPlan.Steps) > 0 {
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

		response, err := s.runtime.Execute(ctx, runtime.ExecuteRequest{
			SessionID:        sessionID,
			RequestID:        jobID + ":" + step.ID,
			Skill:            step.Skill,
			TargetBackendRef: backendRef(targetObject),
			Params:           step.Params,
			SessionRoot:      s.workspaceRoot(sessionRecord.WorkspaceID),
		})
		if err != nil {
			stepResult.Status = models.JobFailed
			stepResult.Summary = err.Error()
			finishedAt := time.Now().UTC()
			stepResult.FinishedAt = &finishedAt
			job.Steps = append(job.Steps, stepResult)
			s.store.SaveJob(job)
			s.failJob(sessionID, jobID, err)
			return
		}

		activeObjectID := sessionRecord.ActiveObjectID
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
			activeObjectID = newObject.ID
			sessionRecord.ActiveObjectID = newObject.ID
			workspaceRecord.ActiveObjectID = newObject.ID
		}

		artifactTargetID := activeObjectID
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

		stepResult.Status = models.JobSucceeded
		stepResult.Summary = response.Summary
		stepResult.Metadata = response.Metadata
		finishedAt := time.Now().UTC()
		stepResult.FinishedAt = &finishedAt
		job.Steps = append(job.Steps, stepResult)
		stepSummaries = append(stepSummaries, response.Summary)

		sessionRecord.UpdatedAt = finishedAt
		sessionRecord.LastAccessedAt = finishedAt
		s.store.SaveSession(sessionRecord)
		workspaceRecord.UpdatedAt = finishedAt
		workspaceRecord.LastAccessedAt = finishedAt
		s.store.SaveWorkspace(workspaceRecord)

		evaluation, evalErr := s.evaluateCompletion(ctx, sessionRecord, job, message)
		jobCompleted := false
		switch {
		case evalErr != nil:
			s.appendJobCheckpoint(
				job,
				"completion",
				"warn",
				"完成判定",
				"跳过判定",
				"完成判定暂时失败，继续按现有计划执行。",
			)
		case evaluation != nil && evaluation.Completed:
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
		replannedPlan, replanNote, replanErr := s.replanRemainingSteps(ctx, sessionRecord, job, message)
		switch {
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
					joinCheckpointSummary(replanNote, fmt.Sprintf("已根据最新对象状态更新剩余计划，当前还有 %d 步待执行。", len(replannedPlan.Steps))),
				)
			}
			if replanNote != "" {
				job.Summary = replanNote
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

	finishedAt := time.Now().UTC()
	job.Status = models.JobSucceeded
	job.FinishedAt = &finishedAt
	if completionReason != "" {
		job.Summary = completionReason
	} else {
		job.Summary = strings.Join(stepSummaries, " ")
	}
	s.store.SaveJob(job)

	s.store.AddMessage(&models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		JobID:     jobID,
		Role:      models.MessageAssistant,
		Content:   s.buildAssistantSummary(job),
		CreatedAt: finishedAt,
	})

	sessionRecord.UpdatedAt = finishedAt
	sessionRecord.LastAccessedAt = finishedAt
	s.store.SaveSession(sessionRecord)
	s.publishJob(sessionID, jobID)
	s.publishSnapshot(sessionID)
}

func (s *Service) evaluateCompletion(ctx context.Context, sessionRecord *models.Session, job *models.Job, message string) (*CompletionEvaluation, error) {
	if s == nil || s.evaluator == nil {
		return nil, nil
	}
	request, err := s.buildEvaluationRequest(sessionRecord, message, job)
	if err != nil {
		return nil, err
	}
	return s.evaluator.Evaluate(ctx, request)
}

func (s *Service) replanRemainingSteps(ctx context.Context, sessionRecord *models.Session, job *models.Job, message string) (models.Plan, string, error) {
	request, err := s.buildExecutionPlanningRequest(sessionRecord, message, job)
	if err != nil {
		return models.Plan{}, "", err
	}
	plan, note, err := s.buildExecutablePlan(ctx, request)
	if err != nil {
		return models.Plan{}, "", err
	}
	return trimCompletedPlanPrefix(plan, job.Steps), note, nil
}

func (s *Service) buildExecutablePlan(ctx context.Context, request PlanningRequest) (models.Plan, string, error) {
	if err := s.refreshSkills(); err != nil {
		return models.Plan{}, "", err
	}
	plan, err := s.planner.Plan(ctx, request)
	if err != nil {
		if s.PlannerMode() != "llm" {
			return models.Plan{}, "", err
		}

		fallbackPlan, fallbackErr := NewFakePlanner().Plan(ctx, request)
		if fallbackErr != nil {
			return models.Plan{}, "", fmt.Errorf("规划器执行失败：%w；规则兜底也失败：%v", err, fallbackErr)
		}
		fallbackPlan = NormalizePlan(fallbackPlan)
		if fallbackValidateErr := s.skills.ValidatePlan(fallbackPlan); fallbackValidateErr != nil {
			return models.Plan{}, "", fmt.Errorf("规划器执行失败：%w；规则兜底也失败：%v", err, fallbackValidateErr)
		}
		return fallbackPlan, "LLM 规划器请求失败，已切换到规则兜底计划。", nil
	}

	plan = NormalizePlan(plan)
	validateErr := s.skills.ValidatePlan(plan)
	if validateErr == nil {
		return plan, "", nil
	}

	if s.PlannerMode() != "llm" {
		return models.Plan{}, "", fmt.Errorf("执行计划不合法：%w", validateErr)
	}

	fallbackPlan, fallbackErr := NewFakePlanner().Plan(ctx, request)
	if fallbackErr != nil {
		return models.Plan{}, "", fmt.Errorf("执行计划不合法：%w；规则兜底也失败：%v", validateErr, fallbackErr)
	}
	fallbackPlan = NormalizePlan(fallbackPlan)
	if fallbackValidateErr := s.skills.ValidatePlan(fallbackPlan); fallbackValidateErr != nil {
		return models.Plan{}, "", fmt.Errorf("执行计划不合法：%w；规则兜底也失败：%v", validateErr, fallbackValidateErr)
	}

	return fallbackPlan, "LLM 规划结果为空或无效，已切换到规则兜底计划。", nil
}

func (s *Service) failJob(sessionID, jobID string, err error) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return
	}
	finishedAt := time.Now().UTC()
	job.Status = models.JobFailed
	job.Error = err.Error()
	job.FinishedAt = &finishedAt
	s.appendJobCheckpoint(job, "execution", "warn", "执行失败", "已终止", err.Error())
	s.store.SaveJob(job)

	s.store.AddMessage(&models.Message{
		ID:        s.store.NextID("msg"),
		SessionID: sessionID,
		JobID:     jobID,
		Role:      models.MessageAssistant,
		Content:   "执行失败：" + err.Error(),
		CreatedAt: finishedAt,
	})

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

	switch token {
	case "", "$active":
		return requireInWorkspace(sessionRecord.ActiveObjectID)
	case "$prev":
		return requireInWorkspace(prevObjectID)
	default:
		return requireInWorkspace(token)
	}
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

func (s *Service) buildAssistantSummary(job *models.Job) string {
	lines := make([]string, 0, len(job.Steps)+1)
	lines = append(lines, "执行完成：")
	for _, step := range job.Steps {
		lines = append(lines, fmt.Sprintf("%s：%s", step.Skill, step.Summary))
	}
	return strings.Join(lines, "\n")
}

func backendRef(object *models.ObjectMeta) string {
	if object == nil {
		return ""
	}
	return object.BackendRef
}

func (s *Service) appendJobCheckpoint(job *models.Job, kind, tone, title, label, summary string) {
	if job == nil {
		return
	}
	job.Checkpoints = append(job.Checkpoints, models.JobCheckpoint{
		Kind:      kind,
		Tone:      tone,
		Title:     title,
		Label:     label,
		Summary:   summary,
		CreatedAt: time.Now().UTC(),
	})
}

func objectID(object *models.ObjectMeta) string {
	if object == nil {
		return ""
	}
	return object.ID
}

func (s *Service) buildPlanningRequest(sessionRecord *models.Session, message string) (PlanningRequest, error) {
	snapshot, err := s.store.Snapshot(sessionRecord.ID)
	if err != nil {
		return PlanningRequest{}, err
	}

	var activeObject *models.ObjectMeta
	for _, object := range snapshot.Objects {
		if object.ID == snapshot.Session.ActiveObjectID {
			activeObject = object
			break
		}
	}

	return PlanningRequest{
		Message:         message,
		Session:         snapshot.Session,
		Workspace:       snapshot.Workspace,
		ActiveObject:    activeObject,
		Objects:         snapshot.Objects,
		RecentMessages:  trimRecentMessages(snapshot.Messages, message, 6),
		RecentJobs:      trimRecentJobs(snapshot.Jobs, 3),
		RecentArtifacts: trimRecentArtifacts(snapshot.Artifacts, 4),
	}, nil
}

func (s *Service) buildExecutionPlanningRequest(sessionRecord *models.Session, message string, currentJob *models.Job) (PlanningRequest, error) {
	request, err := s.buildPlanningRequest(sessionRecord, message)
	if err != nil {
		return PlanningRequest{}, err
	}
	if currentJob == nil || len(currentJob.Steps) == 0 {
		return request, nil
	}
	request.RecentJobs = append(request.RecentJobs, cloneJobForPlanning(currentJob))
	return request, nil
}

func (s *Service) buildEvaluationRequest(sessionRecord *models.Session, message string, currentJob *models.Job) (EvaluationRequest, error) {
	request, err := s.buildExecutionPlanningRequest(sessionRecord, message, currentJob)
	if err != nil {
		return EvaluationRequest{}, err
	}
	return EvaluationRequest{
		Message:         request.Message,
		Session:         request.Session,
		Workspace:       request.Workspace,
		ActiveObject:    request.ActiveObject,
		Objects:         request.Objects,
		RecentMessages:  request.RecentMessages,
		RecentJobs:      request.RecentJobs,
		RecentArtifacts: request.RecentArtifacts,
		CurrentJob:      cloneJobForPlanning(currentJob),
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
