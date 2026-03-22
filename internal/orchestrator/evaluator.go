package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"scagent/internal/models"
)

type CompletionEvaluation struct {
	Completed bool   `json:"completed"`
	Reason    string `json:"reason,omitempty"`
}

type EvaluationRequest struct {
	Message         string               `json:"message"`
	Session         *models.Session      `json:"session,omitempty"`
	Workspace       *models.Workspace    `json:"workspace,omitempty"`
	ActiveObject    *models.ObjectMeta   `json:"active_object,omitempty"`
	Objects         []*models.ObjectMeta `json:"objects,omitempty"`
	RecentMessages  []*models.Message    `json:"recent_messages,omitempty"`
	RecentJobs      []*models.Job        `json:"recent_jobs,omitempty"`
	RecentArtifacts []*models.Artifact   `json:"recent_artifacts,omitempty"`
	CurrentJob      *models.Job          `json:"current_job,omitempty"`
}

type Evaluator interface {
	Evaluate(ctx context.Context, request EvaluationRequest) (*CompletionEvaluation, error)
	Mode() string
}

type EvaluatorConfig struct {
	Mode            string
	OpenAIAPIKey    string
	OpenAIBaseURL   string
	OpenAIModel     string
	ReasoningEffort string
}

func NewEvaluator(config EvaluatorConfig) (Evaluator, error) {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode == "" || mode == "fake" {
		return NewFakeEvaluator(), nil
	}
	if mode != "llm" {
		return nil, fmt.Errorf("unsupported evaluator mode %q", config.Mode)
	}

	primary, err := NewLLMEvaluator(LLMEvaluatorConfig{
		APIKey:          config.OpenAIAPIKey,
		BaseURL:         config.OpenAIBaseURL,
		Model:           config.OpenAIModel,
		ReasoningEffort: config.ReasoningEffort,
	}, nil)
	if err != nil {
		return nil, err
	}
	return &FallbackEvaluator{
		primary:  primary,
		fallback: NewFakeEvaluator(),
	}, nil
}

type FallbackEvaluator struct {
	primary  Evaluator
	fallback Evaluator
}

func (e *FallbackEvaluator) Mode() string {
	if e == nil || e.primary == nil {
		return "fake"
	}
	return e.primary.Mode()
}

func (e *FallbackEvaluator) Evaluate(ctx context.Context, request EvaluationRequest) (*CompletionEvaluation, error) {
	if e == nil || e.primary == nil {
		if e != nil && e.fallback != nil {
			return e.fallback.Evaluate(ctx, request)
		}
		return &CompletionEvaluation{}, nil
	}

	result, err := e.primary.Evaluate(ctx, request)
	if err == nil || e.fallback == nil {
		return result, err
	}
	return e.fallback.Evaluate(ctx, request)
}

type FakeEvaluator struct{}

func NewFakeEvaluator() *FakeEvaluator {
	return &FakeEvaluator{}
}

func (e *FakeEvaluator) Mode() string {
	return "fake"
}

func (e *FakeEvaluator) Evaluate(_ context.Context, request EvaluationRequest) (*CompletionEvaluation, error) {
	if request.CurrentJob == nil || len(request.CurrentJob.Steps) == 0 {
		return &CompletionEvaluation{
			Completed: false,
			Reason:    "尚未产生可评估的执行结果。",
		}, nil
	}

	lower := strings.ToLower(request.Message)
	switch {
	case asksForAssessment(lower, request.Message):
		return completionFromSkill(request.CurrentJob, "评估结果已返回，当前请求已完成。", "assess_dataset", "inspect_dataset"), nil
	case asksForPreprocessing(lower, request.Message):
		if objectIsAnalysisReady(request.ActiveObject) {
			return &CompletionEvaluation{
				Completed: true,
				Reason:    "当前对象已达到 analysis_ready，预处理目标已完成。",
			}, nil
		}
		return &CompletionEvaluation{
			Completed: false,
			Reason:    "当前对象仍未达到 analysis_ready，需要继续执行后续预处理步骤。",
		}, nil
	case asksForMarkerAnalysis(lower, request.Message):
		return completionFromSkill(request.CurrentJob, "marker 结果已生成，当前请求已完成。", "find_markers"), nil
	case asksForExport(lower, request.Message):
		return completionFromSkill(request.CurrentJob, "导出文件已生成，当前请求已完成。", "export_h5ad"), nil
	case asksForPlot(lower, request.Message):
		if len(inferGeneNames(request.Message)) > 0 {
			return completionFromSkill(request.CurrentJob, "目标图像已生成，当前请求已完成。", "plot_gene_umap"), nil
		}
		return completionFromSkill(request.CurrentJob, "目标图像已生成，当前请求已完成。", "plot_umap", "plot_gene_umap"), nil
	case asksForSubcluster(lower, request.Message):
		return completionFromSkill(request.CurrentJob, "亚群分析结果已生成，当前请求已完成。", "subcluster_from_global", "reanalyze_subset"), nil
	case asksForRecluster(lower, request.Message):
		return completionFromSkill(request.CurrentJob, "重新聚类结果已生成，当前请求已完成。", "recluster", "reanalyze_subset", "subcluster_from_global"), nil
	case asksForSubsetOnly(lower, request.Message):
		return completionFromSkill(request.CurrentJob, "目标子集对象已生成，当前请求已完成。", "subset_cells"), nil
	default:
		return &CompletionEvaluation{
			Completed: false,
			Reason:    "当前请求看起来仍需要结合后续步骤或重规划继续推进。",
		}, nil
	}
}

func completionFromSkill(job *models.Job, successReason string, skills ...string) *CompletionEvaluation {
	if jobHasSucceededSkill(job, skills...) {
		return &CompletionEvaluation{
			Completed: true,
			Reason:    successReason,
		}
	}
	return &CompletionEvaluation{
		Completed: false,
		Reason:    "还没有拿到能直接满足请求的关键结果。",
	}
}

func jobHasSucceededSkill(job *models.Job, skills ...string) bool {
	if job == nil || len(job.Steps) == 0 || len(skills) == 0 {
		return false
	}
	allowed := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		allowed[skill] = struct{}{}
	}
	for _, step := range job.Steps {
		if step.Status != models.JobSucceeded {
			continue
		}
		if _, ok := allowed[step.Skill]; ok {
			return true
		}
	}
	return false
}

func asksForAssessment(lower, message string) bool {
	return strings.Contains(lower, "assess") ||
		strings.Contains(lower, "inspect") ||
		strings.Contains(message, "评估") ||
		strings.Contains(message, "检查数据")
}

func asksForPreprocessing(lower, message string) bool {
	return strings.Contains(lower, "preprocess") || strings.Contains(message, "预处理")
}

func asksForMarkerAnalysis(lower, message string) bool {
	return strings.Contains(lower, "marker") || strings.Contains(message, "标记")
}

func asksForExport(lower, message string) bool {
	return strings.Contains(lower, "export") || strings.Contains(message, "导出")
}

func asksForPlot(lower, message string) bool {
	return strings.Contains(lower, "plot") ||
		strings.Contains(lower, "umap") ||
		strings.Contains(message, "画") ||
		strings.Contains(message, "绘") ||
		strings.Contains(message, "图例") ||
		strings.Contains(message, "UMAP")
}

func asksForSubcluster(lower, message string) bool {
	return strings.Contains(lower, "subcluster") || strings.Contains(message, "亚群")
}

func asksForRecluster(lower, message string) bool {
	return strings.Contains(lower, "recluster") || strings.Contains(message, "重新聚类")
}

func asksForSubsetOnly(lower, message string) bool {
	if !(strings.Contains(lower, "subset") || strings.Contains(message, "提取") || strings.Contains(message, "拿出来") || strings.Contains(message, "筛")) {
		return false
	}
	return !asksForPlot(lower, message) && !asksForMarkerAnalysis(lower, message) && !asksForRecluster(lower, message) && !asksForSubcluster(lower, message)
}

func objectIsAnalysisReady(object *models.ObjectMeta) bool {
	if object == nil || len(object.Metadata) == 0 {
		return false
	}
	assessment, ok := object.Metadata["assessment"].(map[string]any)
	if !ok {
		return objectHasEmbedding(object, "X_umap")
	}
	if state, ok := assessment["preprocessing_state"].(string); ok && state == "analysis_ready" {
		return true
	}
	if hasUMAP, ok := assessment["has_umap"].(bool); ok && hasUMAP {
		return true
	}
	return objectHasEmbedding(object, "X_umap")
}

type LLMEvaluatorConfig struct {
	APIKey          string
	BaseURL         string
	Model           string
	ReasoningEffort string
}

type LLMEvaluator struct {
	apiKey          string
	baseURL         string
	model           string
	reasoningEffort string
	httpClient      *http.Client
}

func NewLLMEvaluator(config LLMEvaluatorConfig, httpClient *http.Client) (*LLMEvaluator, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = "gpt-5.4"
	}
	reasoningEffort := strings.TrimSpace(config.ReasoningEffort)
	if reasoningEffort == "" {
		reasoningEffort = "low"
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("LLM evaluator requires an API key")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}

	return &LLMEvaluator{
		apiKey:          config.APIKey,
		baseURL:         baseURL,
		model:           model,
		reasoningEffort: reasoningEffort,
		httpClient:      httpClient,
	}, nil
}

func (e *LLMEvaluator) Mode() string {
	return "llm"
}

func (e *LLMEvaluator) Evaluate(ctx context.Context, requestPayload EvaluationRequest) (*CompletionEvaluation, error) {
	payload, err := json.Marshal(e.buildRequest(requestPayload))
	if err != nil {
		return nil, fmt.Errorf("marshal evaluator request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create evaluator request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+e.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := e.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("evaluator request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read evaluator response: %w", err)
	}

	if response.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("evaluator returned %s: %s", response.Status, compactJSON(string(body)))
	}

	var decoded openAIResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode evaluator response: %w", err)
	}

	rawDecision := extractPlannerText(decoded)
	if strings.TrimSpace(rawDecision) == "" {
		return nil, fmt.Errorf("evaluator response did not contain text output")
	}

	var decision CompletionEvaluation
	if err := json.Unmarshal([]byte(rawDecision), &decision); err != nil {
		return nil, fmt.Errorf("decode evaluator decision JSON: %w", err)
	}
	return &decision, nil
}

func (e *LLMEvaluator) buildRequest(requestPayload EvaluationRequest) map[string]any {
	return map[string]any{
		"model": e.model,
		"reasoning": map[string]any{
			"effort": e.reasoningEffort,
		},
		"input": []map[string]any{
			{
				"role":    "developer",
				"content": e.instructions(requestPayload),
			},
			{
				"role":    "user",
				"content": requestPayload.Message,
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "scagent_completion_evaluation",
				"strict": true,
				"schema": completionSchema(),
			},
		},
	}
}

func (e *LLMEvaluator) instructions(requestPayload EvaluationRequest) string {
	lines := []string{
		"You are the scAgent completion evaluator.",
		"Decide whether the user's request has already been satisfied by the current state of the session.",
		"Return only valid JSON matching the supplied schema.",
		"Be conservative: completed=true only when the request objective is already satisfied and no further execution is needed.",
		"Treat current_job, the active object metadata, recent jobs, and artifacts as the source of truth.",
		"If the request is a long workflow and intermediate preprocessing has not yet reached the necessary state, return completed=false.",
		"If the request asks for a concrete output such as a plot, marker table, subset object, export file, or assessment summary and that output already exists in the current job results, return completed=true.",
		"Current execution context:",
	}
	lines = append(lines, formatEvaluationContext(requestPayload)...)
	return strings.Join(lines, "\n")
}

func completionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"completed", "reason"},
		"properties": map[string]any{
			"completed": map[string]any{"type": "boolean"},
			"reason":    map[string]any{"type": "string"},
		},
	}
}

func formatEvaluationContext(request EvaluationRequest) []string {
	planningContext := formatPlanningContext(PlanningRequest{
		Message:         request.Message,
		Session:         request.Session,
		ActiveObject:    request.ActiveObject,
		Objects:         request.Objects,
		RecentMessages:  request.RecentMessages,
		RecentJobs:      request.RecentJobs,
		RecentArtifacts: request.RecentArtifacts,
	})
	if request.CurrentJob == nil {
		return append(planningContext, "- current_job=none")
	}

	lines := append([]string(nil), planningContext...)
	lines = append(lines, "- current_job="+formatCurrentJobContext(request.CurrentJob))
	return lines
}

func formatCurrentJobContext(job *models.Job) string {
	if job == nil {
		return "job=nil"
	}
	stepParts := make([]string, 0, len(job.Steps))
	for _, step := range job.Steps {
		stepParts = append(stepParts, fmt.Sprintf(
			"{id=%s skill=%s status=%s target=%s params=%s summary=%s output=%s}",
			step.ID,
			step.Skill,
			step.Status,
			step.TargetObjectID,
			compactJSON(mustMarshalJSON(step.Params)),
			truncateText(step.Summary, 120),
			step.OutputObjectID,
		))
	}
	return fmt.Sprintf(
		"id=%s | status=%s | summary=%s | steps=%s",
		job.ID,
		job.Status,
		truncateText(job.Summary, 200),
		strings.Join(stepParts, "; "),
	)
}
