package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"scagent/internal/models"
	"scagent/internal/runtime"
	"scagent/internal/session"
	"scagent/internal/skill"
)

type Service struct {
	store    *session.Store
	skills   *skill.Registry
	runtime  runtime.Service
	planner  Planner
	dataRoot string
}

type PlanningRequest struct {
	Message      string               `json:"message"`
	Session      *models.Session      `json:"session,omitempty"`
	ActiveObject *models.ObjectMeta   `json:"active_object,omitempty"`
	Objects      []*models.ObjectMeta `json:"objects,omitempty"`
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
	return &Service{
		store:    store,
		skills:   skills,
		runtime:  runtimeClient,
		planner:  planner,
		dataRoot: dataRoot,
	}
}

func (s *Service) Skills() []skill.Definition {
	return s.skills.List()
}

func (s *Service) PlannerMode() string {
	if s.planner == nil {
		return ""
	}
	return s.planner.Mode()
}

func (s *Service) Status(ctx context.Context) *SystemStatus {
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
	plan, err := s.planner.Plan(ctx, PlanningRequest{Message: message})
	if err != nil {
		return models.Plan{}, err
	}
	plan = NormalizePlan(plan)
	if err := s.skills.ValidatePlan(plan); err != nil {
		return models.Plan{}, err
	}
	return plan, nil
}

func (s *Service) PreviewFakePlan(ctx context.Context, message string) (models.Plan, error) {
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

func (s *Service) CreateSession(ctx context.Context, label string) (*models.SessionSnapshot, error) {
	if label == "" {
		label = "植物单细胞分析会话"
	}

	sessionRecord := s.store.CreateSession(label)
	sessionRoot := s.sessionRoot(sessionRecord.ID)
	if err := os.MkdirAll(filepath.Join(sessionRoot, "artifacts"), 0o755); err != nil {
		return nil, fmt.Errorf("create artifacts directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(sessionRoot, "objects"), 0o755); err != nil {
		return nil, fmt.Errorf("create objects directory: %w", err)
	}

	response, err := s.runtime.InitSession(ctx, runtime.InitSessionRequest{
		SessionID:   sessionRecord.ID,
		DatasetID:   sessionRecord.DatasetID,
		Label:       label,
		SessionRoot: sessionRoot,
	})
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	rootObject := &models.ObjectMeta{
		ID:               s.store.NextID("obj"),
		SessionID:        sessionRecord.ID,
		DatasetID:        sessionRecord.DatasetID,
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

	safeName, err := sanitizeUploadName(filename)
	if err != nil {
		return nil, nil, err
	}

	sessionRoot := s.sessionRoot(sessionID)
	objectPath := filepath.Join(sessionRoot, "objects", safeName)
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
		ID:        s.store.NextID("job"),
		SessionID: sessionID,
		MessageID: message.ID,
		Status:    models.JobQueued,
		CreatedAt: now,
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

	plan, err := s.planner.Plan(ctx, planningRequest)
	if err != nil {
		s.failJob(sessionID, jobID, fmt.Errorf("规划器执行失败：%w", err))
		return
	}
	plan = NormalizePlan(plan)
	if err := s.skills.ValidatePlan(plan); err != nil {
		s.failJob(sessionID, jobID, fmt.Errorf("执行计划不合法：%w", err))
		return
	}

	job.Plan = &plan
	job.Summary = "编排器已接受执行计划。"
	s.store.SaveJob(job)
	s.publishJob(sessionID, jobID)

	prevObjectID := sessionRecord.ActiveObjectID
	stepSummaries := make([]string, 0, len(plan.Steps))

	for _, step := range plan.Steps {
		stepResult := models.JobStep{
			ID:             step.ID,
			Skill:          step.Skill,
			TargetObjectID: step.TargetObjectID,
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
			SessionRoot:      s.sessionRoot(sessionID),
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
				SessionID:        sessionID,
				DatasetID:        sessionRecord.DatasetID,
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
		s.store.SaveJob(job)
		s.publishJob(sessionID, jobID)
		s.publishSnapshot(sessionID)
	}

	finishedAt := time.Now().UTC()
	job.Status = models.JobSucceeded
	job.FinishedAt = &finishedAt
	job.Summary = strings.Join(stepSummaries, " ")
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

func (s *Service) failJob(sessionID, jobID string, err error) {
	job, ok := s.store.GetJob(jobID)
	if !ok {
		return
	}
	finishedAt := time.Now().UTC()
	job.Status = models.JobFailed
	job.Error = err.Error()
	job.FinishedAt = &finishedAt
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
	switch token {
	case "", "$active":
		return s.getRequiredObject(sessionRecord.ActiveObjectID)
	case "$prev":
		return s.getRequiredObject(prevObjectID)
	default:
		return s.getRequiredObject(token)
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
		Type:      "job_updated",
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
		Data:      job,
	})
}

func (s *Service) publishSnapshot(sessionID string) {
	snapshot, err := s.store.Snapshot(sessionID)
	if err != nil {
		return
	}
	s.store.Publish(sessionID, models.Event{
		Type:      "session_updated",
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
		Data:      snapshot,
	})
}

func (s *Service) sessionRoot(sessionID string) string {
	return filepath.Join(s.dataRoot, "sessions", sessionID)
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
		if object.ID == sessionRecord.ActiveObjectID {
			activeObject = object
			break
		}
	}

	return PlanningRequest{
		Message:      message,
		Session:      snapshot.Session,
		ActiveObject: activeObject,
		Objects:      snapshot.Objects,
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
