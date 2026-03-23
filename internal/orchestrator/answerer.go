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

type Answerer interface {
	BuildDirectAnswer(ctx context.Context, request PlanningRequest) (string, bool, error)
	BuildInvestigationResponse(ctx context.Context, request ResponseComposeRequest) (*ResponseComposeResult, error)
	BuildFailureAnswer(err error) string
}

type AnswererConfig struct {
	Mode            string
	OpenAIAPIKey    string
	OpenAIBaseURL   string
	OpenAIModel     string
	ReasoningEffort string
}

type NoopAnswerer struct{}

type LLMAnswerer struct {
	*NoopAnswerer
	apiKey          string
	baseURL         string
	model           string
	reasoningEffort string
	httpClient      *http.Client
}

type directAnswerDecision struct {
	Decision   string   `json:"decision"`
	Answer     string   `json:"answer"`
	Confidence string   `json:"confidence"`
	Evidence   []string `json:"evidence,omitempty"`
}

type ResponseComposeRequest struct {
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
	CurrentJob      *models.Job           `json:"current_job,omitempty"`
	WorkingMemory   *models.WorkingMemory `json:"working_memory,omitempty"`
}

type ResponseComposeResult struct {
	Answer  string `json:"answer"`
	Summary string `json:"summary"`
}

type investigationResponse struct {
	Answer  string `json:"answer"`
	Summary string `json:"summary"`
}

func NewAnswerer(config AnswererConfig) (Answerer, error) {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode == "" {
		return nil, fmt.Errorf("answerer mode is required")
	}
	if mode == "fake" {
		return NewNoopAnswerer(), nil
	}
	if mode != "llm" {
		return nil, fmt.Errorf("unsupported answerer mode %q", config.Mode)
	}
	return NewLLMAnswerer(LLMAnswererConfig{
		APIKey:          config.OpenAIAPIKey,
		BaseURL:         config.OpenAIBaseURL,
		Model:           config.OpenAIModel,
		ReasoningEffort: config.ReasoningEffort,
	}, nil)
}

func NewNoopAnswerer() *NoopAnswerer {
	return &NoopAnswerer{}
}

func (a *NoopAnswerer) BuildDirectAnswer(_ context.Context, _ PlanningRequest) (string, bool, error) {
	return "", false, nil
}

func (a *NoopAnswerer) BuildInvestigationResponse(_ context.Context, request ResponseComposeRequest) (*ResponseComposeResult, error) {
	job := request.CurrentJob
	if job == nil {
		return &ResponseComposeResult{}, nil
	}

	switch job.Status {
	case models.JobFailed:
		message := strings.TrimSpace(job.Error)
		if message == "" {
			message = "本次执行失败，请稍后重试。"
		}
		return &ResponseComposeResult{
			Answer:  message,
			Summary: message,
		}, nil
	case models.JobIncomplete:
		message := bestResponseSummary(job)
		if message == "" {
			message = "本次执行还没完成，需要继续操作。"
		}
		return &ResponseComposeResult{
			Answer:  message,
			Summary: strings.TrimSpace(job.Summary),
		}, nil
	case models.JobCanceled:
		message := bestResponseSummary(job)
		if message == "" {
			message = "当前任务已停止。"
		}
		return &ResponseComposeResult{
			Answer:  message,
			Summary: strings.TrimSpace(job.Summary),
		}, nil
	default:
		answer := composeAnswerFromEvidence(request.Message, job)
		if answer == "" {
			answer = bestResponseSummary(job)
		}
		if answer == "" {
			answer = "本次执行已完成。"
		}
		summary := strings.TrimSpace(job.Summary)
		if summary == "" {
			summary = answer
		}
		return &ResponseComposeResult{
			Answer:  answer,
			Summary: summary,
		}, nil
	}
}

func (a *NoopAnswerer) BuildFailureAnswer(err error) string {
	if err == nil {
		return "本次执行失败，请稍后重试。"
	}
	raw := strings.TrimSpace(err.Error())
	lower := strings.ToLower(raw)

	switch {
	case strings.Contains(raw, "需要一个目标对象"),
		strings.Contains(raw, "未找到对象"),
		strings.Contains(raw, "当前没有活动对象"):
		return "当前没有可用的目标对象。请先上传或选择数据集。"
	case strings.Contains(raw, "未找到 workspace"):
		return "当前 workspace 不可用，请刷新后重试。"
	case strings.Contains(raw, "规划器执行失败"),
		strings.Contains(raw, "规划上下文构建失败"),
		strings.Contains(lower, "planner"),
		strings.Contains(lower, "invalid schema"),
		strings.Contains(lower, "response_format"):
		return "规划器暂时不可用，本次执行未开始。请稍后重试。"
	case userFacingRuntimeDetail(raw) != "":
		return userFacingRuntimeDetail(raw)
	case strings.Contains(lower, "decode runtime response"),
		strings.Contains(lower, "json"),
		strings.Contains(lower, "traceback"),
		strings.Contains(lower, "bad request"):
		return "本次执行失败，请稍后重试。"
	default:
		return raw
	}
}

type LLMAnswererConfig struct {
	APIKey          string
	BaseURL         string
	Model           string
	ReasoningEffort string
}

func NewLLMAnswerer(config LLMAnswererConfig, httpClient *http.Client) (*LLMAnswerer, error) {
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
		return nil, fmt.Errorf("LLM answerer requires an API key")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}

	return &LLMAnswerer{
		NoopAnswerer:    NewNoopAnswerer(),
		apiKey:          config.APIKey,
		baseURL:         baseURL,
		model:           model,
		reasoningEffort: reasoningEffort,
		httpClient:      httpClient,
	}, nil
}

func (a *LLMAnswerer) BuildDirectAnswer(ctx context.Context, requestPayload PlanningRequest) (string, bool, error) {
	payload, err := json.Marshal(a.buildRequest(requestPayload))
	if err != nil {
		return "", false, fmt.Errorf("marshal answerer request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return "", false, fmt.Errorf("create answerer request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return "", false, fmt.Errorf("answerer request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", false, fmt.Errorf("read answerer response: %w", err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return "", false, fmt.Errorf("answerer returned %s: %s", response.Status, compactJSON(string(body)))
	}

	var decoded openAIResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", false, fmt.Errorf("decode answerer response: %w", err)
	}

	rawDecision := extractPlannerText(decoded)
	if strings.TrimSpace(rawDecision) == "" {
		return "", false, fmt.Errorf("answerer response did not contain text output")
	}

	var decision directAnswerDecision
	if err := json.Unmarshal([]byte(rawDecision), &decision); err != nil {
		return "", false, fmt.Errorf("decode answerer JSON: %w", err)
	}

	if decision.Decision != "direct_answer" {
		return "", false, nil
	}
	if strings.TrimSpace(decision.Confidence) != "high" {
		return "", false, nil
	}

	answer := strings.TrimSpace(decision.Answer)
	if answer == "" {
		return "", false, nil
	}
	return answer, true, nil
}

func (a *LLMAnswerer) BuildInvestigationResponse(ctx context.Context, requestPayload ResponseComposeRequest) (*ResponseComposeResult, error) {
	payload, err := json.Marshal(a.buildResponseRequest(requestPayload))
	if err != nil {
		return nil, fmt.Errorf("marshal response composer request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create response composer request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+a.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("response composer request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read response composer response: %w", err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("response composer returned %s: %s", response.Status, compactJSON(string(body)))
	}

	var decoded openAIResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode response composer response: %w", err)
	}

	rawResponse := extractPlannerText(decoded)
	if strings.TrimSpace(rawResponse) == "" {
		return nil, fmt.Errorf("response composer response did not contain text output")
	}

	var composed investigationResponse
	if err := json.Unmarshal([]byte(rawResponse), &composed); err != nil {
		return nil, fmt.Errorf("decode response composer JSON: %w", err)
	}

	return &ResponseComposeResult{
		Answer:  strings.TrimSpace(composed.Answer),
		Summary: strings.TrimSpace(composed.Summary),
	}, nil
}

func (a *LLMAnswerer) buildRequest(requestPayload PlanningRequest) map[string]any {
	return map[string]any{
		"model": a.model,
		"reasoning": map[string]any{
			"effort": a.reasoningEffort,
		},
		"input": []map[string]any{
			{
				"role":    "developer",
				"content": a.instructions(requestPayload),
			},
			{
				"role": "user",
				"content": buildUserInputContentWithPolicy(
					requestPayload.Message,
					requestPayload.InputArtifacts,
					requestPayload.RecentArtifacts,
					UserInputContentPolicy{
						IncludeInputVisualArtifacts:  true,
						IncludeRecentVisualArtifacts: false,
					},
				),
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "scagent_direct_answer",
				"strict": true,
				"schema": directAnswerSchema(),
			},
		},
	}
}

func (a *LLMAnswerer) buildResponseRequest(requestPayload ResponseComposeRequest) map[string]any {
	return map[string]any{
		"model": a.model,
		"reasoning": map[string]any{
			"effort": a.reasoningEffort,
		},
		"input": []map[string]any{
			{
				"role":    "developer",
				"content": a.responseInstructions(requestPayload),
			},
			{
				"role": "user",
				"content": buildUserInputContentWithPolicy(
					requestPayload.Message,
					requestPayload.InputArtifacts,
					requestPayload.RecentArtifacts,
					UserInputContentPolicy{
						IncludeInputVisualArtifacts:  true,
						IncludeRecentVisualArtifacts: false,
					},
				),
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "scagent_final_response",
				"strict": true,
				"schema": responseComposeSchema(),
			},
		},
	}
}

func (a *LLMAnswerer) instructions(requestPayload PlanningRequest) string {
	lines := []string{
		"You are the scAgent direct-answer decider.",
		"Decide whether the user's message can be answered immediately from the current session context without running any new analysis step.",
		"Use full natural-language understanding. Do not rely on literal keyword matching or fixed templates.",
		"Only choose decision=direct_answer when the answer is already grounded in the provided context and you are highly confident.",
		"The resolved object roles are authoritative. When the user says 当前对象/这个对象/current object, treat focus_object as the primary source of truth.",
		"Do not let stale working_memory, recent plots, or older artifacts override focus_object for current-object questions.",
		"If focus_object already contains the exact scalar or field list needed to answer, such as n_obs, n_vars, label, obs_fields, or available analyses, answer directly with confidence=high.",
		"If the request requires new computation, data inspection not already reflected in context, or the intent is ambiguous, choose decision=needs_execution.",
		"The current user turn may include attached images; use them when deciding whether a direct visual answer is possible.",
		"When decision=direct_answer, answer concisely in the user's language and do not mention internal fields, planning, or hidden state.",
		"When decision=needs_execution, leave answer as an empty string.",
		"Return only valid JSON matching the supplied schema.",
		"Current session context:",
	}
	lines = append(lines, formatPlanningContextWithPolicy(requestPayload, answererPlanningContextPolicy())...)
	return strings.Join(lines, "\n")
}

func (a *LLMAnswerer) responseInstructions(requestPayload ResponseComposeRequest) string {
	lines := []string{
		"You are the scAgent responder.",
		"Your task is to produce the final user-facing answer after the investigation phase has collected evidence.",
		"Base the answer on current_job facts, metadata, artifacts, the resolved object roles (focus/global/root), and recent context.",
		"If the current request includes attached images, use them as additional evidence when they are relevant to the answer.",
		"Prefer concrete collected evidence such as structured facts, scalar results, tables, generated objects, and artifacts over generic step summaries.",
		"If the investigation is still incomplete, say clearly what is still missing instead of pretending the answer is known.",
		"Do not mention internal step ids, job ids, or implementation details unless the user explicitly asked for them.",
		"Return only valid JSON matching the supplied schema.",
		"Current execution context:",
	}
	lines = append(lines, formatResponseComposeContext(requestPayload)...)
	return strings.Join(lines, "\n")
}

func directAnswerSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"decision", "answer", "confidence", "evidence"},
		"properties": map[string]any{
			"decision": map[string]any{
				"type": "string",
				"enum": []string{"direct_answer", "needs_execution"},
			},
			"answer": map[string]any{
				"type": "string",
			},
			"confidence": map[string]any{
				"type": "string",
				"enum": []string{"high", "medium", "low"},
			},
			"evidence": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
	}
}

func responseComposeSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"answer", "summary"},
		"properties": map[string]any{
			"answer": map[string]any{
				"type": "string",
			},
			"summary": map[string]any{
				"type": "string",
			},
		},
	}
}

func userFacingRuntimeDetail(raw string) string {
	if !strings.Contains(raw, "runtime /execute returned") {
		return ""
	}
	index := strings.LastIndex(raw, ": ")
	if index < 0 || index+2 >= len(raw) {
		return ""
	}
	detail := strings.TrimSpace(raw[index+2:])
	lower := strings.ToLower(detail)
	if detail == "" ||
		strings.Contains(lower, "bad request") ||
		strings.Contains(lower, "traceback") ||
		strings.Contains(lower, "decode") ||
		strings.Contains(lower, "json") {
		return ""
	}
	return detail
}

func formatResponseComposeContext(request ResponseComposeRequest) []string {
	contextLines := formatEvaluationContext(EvaluationRequest{
		Message:         request.Message,
		Session:         request.Session,
		Workspace:       request.Workspace,
		FocusObject:     request.FocusObject,
		GlobalObject:    request.GlobalObject,
		RootObject:      request.RootObject,
		Objects:         request.Objects,
		RecentMessages:  request.RecentMessages,
		RecentJobs:      request.RecentJobs,
		RecentArtifacts: request.RecentArtifacts,
		CurrentJob:      request.CurrentJob,
		WorkingMemory:   request.WorkingMemory,
	})
	return contextLines
}

func composeAnswerFromEvidence(message string, job *models.Job) string {
	if text := strings.TrimSpace(extractResultText(job)); text != "" {
		return text
	}
	if value, ok := extractResultValue(job); ok {
		return fmt.Sprintf("结果是 %s。", renderEvidenceValue(value))
	}
	if stdout := strings.TrimSpace(extractScalarStdout(job)); stdout != "" {
		return fmt.Sprintf("结果是 %s。", stdout)
	}
	if strings.TrimSpace(job.Summary) != "" && !isGenericStepSummary(job.Summary) {
		return strings.TrimSpace(job.Summary)
	}
	return bestJobSummary(job)
}

func bestResponseSummary(job *models.Job) string {
	if job == nil {
		return ""
	}
	if strings.TrimSpace(job.Summary) != "" && !isGenericStepSummary(job.Summary) {
		return strings.TrimSpace(job.Summary)
	}
	if summary := bestJobSummary(job); summary != "" {
		return summary
	}
	return strings.TrimSpace(job.Summary)
}

func bestJobSummary(job *models.Job) string {
	if job == nil {
		return ""
	}
	if strings.TrimSpace(job.Summary) != "" {
		if len(job.Steps) != 1 || isGenericStepSummary(job.Steps[0].Summary) {
			return strings.TrimSpace(job.Summary)
		}
	}
	if len(job.Steps) == 1 {
		stepSummary := strings.TrimSpace(job.Steps[0].Summary)
		if stepSummary != "" && !isGenericStepSummary(stepSummary) {
			return stepSummary
		}
		return strings.TrimSpace(job.Summary)
	}
	for i := len(job.Steps) - 1; i >= 0; i-- {
		if summary := strings.TrimSpace(job.Steps[i].Summary); summary != "" {
			return summary
		}
	}
	return ""
}

func extractResultText(job *models.Job) string {
	for i := len(job.Steps) - 1; i >= 0; i-- {
		if text, ok := stringFact(job.Steps[i].Facts["result_text"]); ok && text != "" {
			return text
		}
	}
	return ""
}

func extractResultValue(job *models.Job) (any, bool) {
	for i := len(job.Steps) - 1; i >= 0; i-- {
		value, ok := job.Steps[i].Facts["result_value"]
		if ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func extractScalarStdout(job *models.Job) string {
	for i := len(job.Steps) - 1; i >= 0; i-- {
		if stdout, ok := stringFact(job.Steps[i].Facts["stdout_text"]); ok && isSingleTokenValue(stdout) {
			return stdout
		}
		if stdout, ok := stringFact(job.Steps[i].Metadata["stdout"]); ok && isSingleTokenValue(stdout) {
			return stdout
		}
	}
	return ""
}

func renderEvidenceValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%g", typed)
	case float32:
		if typed == float32(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%g", typed)
	default:
		return fmt.Sprint(typed)
	}
}

func stringFact(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), true
	default:
		return strings.TrimSpace(fmt.Sprint(typed)), true
	}
}

func isSingleTokenValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "\n") {
		return false
	}
	if len(strings.Fields(value)) > 1 && !(strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]")) {
		return false
	}
	return true
}

func isGenericStepSummary(summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}
	genericMarkers := []string{
		"已完成针对",
		"自定义 Python 分析",
		"编排器已接受执行计划",
	}
	for _, marker := range genericMarkers {
		if strings.Contains(summary, marker) {
			return true
		}
	}
	return false
}
