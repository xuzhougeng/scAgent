package orchestrator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scagent/internal/models"
	"scagent/internal/skill"
)

type pbmcEvalSuite struct {
	Suite       string              `json:"suite"`
	Version     int                 `json:"version"`
	Dataset     string              `json:"dataset"`
	Assumptions pbmcEvalAssumptions `json:"assumptions"`
	Cases       []pbmcEvalCase      `json:"cases"`
}

type pbmcEvalAssumptions struct {
	FocusObject        pbmcEvalObjectSignals `json:"focus_object"`
	ObjectSignals      pbmcEvalObjectSignals `json:"object_signals"`
	CellTypeExamples   []string              `json:"cell_type_examples"`
	RecentPlotDefaults pbmcEvalRecentPlot    `json:"recent_plot_defaults"`
}

type pbmcEvalObjectSignals struct {
	Label             string   `json:"label"`
	Kind              string   `json:"kind"`
	NObs              int      `json:"n_obs"`
	NVars             int      `json:"n_vars"`
	ObsFields         []string `json:"obs_fields"`
	ObsmKeys          []string `json:"obsm_keys"`
	AvailableAnalyses []string `json:"available_analyses"`
}

type pbmcEvalRecentPlot struct {
	Skill     string `json:"skill"`
	ColorBy   string `json:"color_by"`
	LegendLoc string `json:"legend_loc"`
	PointSize int    `json:"point_size"`
	Title     string `json:"title"`
}

type pbmcEvalCase struct {
	ID                  string                      `json:"id"`
	Component           string                      `json:"component"`
	Title               string                      `json:"title"`
	UserMessage         string                      `json:"user_message"`
	ContextOverrides    *pbmcEvalContextOverrides   `json:"context_overrides"`
	ContextExpectations pbmcEvalContextExpectations `json:"context_expectations"`
	CurrentJob          *pbmcEvalCurrentJob         `json:"current_job"`
	Expected            pbmcEvalExpected            `json:"expected"`
}

type pbmcEvalContextOverrides struct {
	FocusObject                  *pbmcEvalObjectSignals      `json:"focus_object"`
	IncludeDefaultRecentPlot     *bool                       `json:"include_default_recent_plot"`
	IncludeDefaultRecentMessages *bool                       `json:"include_default_recent_messages"`
	RecentPlot                   *pbmcEvalRecentPlotOverride `json:"recent_plot"`
	RecentMessages               []pbmcEvalMessageOverride   `json:"recent_messages"`
}

type pbmcEvalRecentPlotOverride struct {
	Skill           string `json:"skill"`
	ColorBy         string `json:"color_by"`
	LegendLoc       string `json:"legend_loc"`
	PointSize       int    `json:"point_size"`
	Title           string `json:"title"`
	ObjectID        string `json:"object_id"`
	ObjectLabel     string `json:"object_label"`
	ArtifactTitle   string `json:"artifact_title"`
	ArtifactSummary string `json:"artifact_summary"`
}

type pbmcEvalMessageOverride struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type pbmcEvalContextExpectations struct {
	CompactContext                               bool   `json:"compact_context"`
	InlineRecentVisualBytes                      bool   `json:"inline_recent_visual_bytes"`
	InlineExplicitVisualBytes                    bool   `json:"inline_explicit_visual_bytes"`
	ShouldUseRecentPlotMetadata                  bool   `json:"should_use_recent_plot_metadata"`
	ShouldUseObsField                            string `json:"should_use_obs_field"`
	RecentVisualArtifactsShouldRemainSummaryOnly bool   `json:"recent_visual_artifacts_should_remain_summary_only"`
}

type pbmcEvalExpected struct {
	Steps                   []pbmcEvalExpectedStep    `json:"steps"`
	Decision                string                    `json:"decision"`
	Confidence              string                    `json:"confidence"`
	AnswerShouldMention     []string                  `json:"answer_should_mention"`
	EvidenceShouldInclude   []string                  `json:"evidence_should_include"`
	Completed               *bool                     `json:"completed"`
	ReasonShouldMention     []string                  `json:"reason_should_mention"`
	RequestBodyExpectations pbmcEvalRequestBodyExpect `json:"request_body_expectations"`
}

type pbmcEvalExpectedStep struct {
	Skill                string         `json:"skill"`
	TargetObjectID       string         `json:"target_object_id"`
	Params               map[string]any `json:"params"`
	ParamsShouldPreserve map[string]any `json:"params_should_preserve"`
	ParamsShouldOverride map[string]any `json:"params_should_override"`
}

type pbmcEvalRequestBodyExpect struct {
	InputImageCount int `json:"input_image_count"`
}

type pbmcEvalCurrentJob struct {
	Status string                `json:"status"`
	Steps  []pbmcEvalCurrentStep `json:"steps"`
}

type pbmcEvalCurrentStep struct {
	Skill        string         `json:"skill"`
	Status       string         `json:"status"`
	Summary      string         `json:"summary"`
	ArtifactKind string         `json:"artifact_kind"`
	Metadata     map[string]any `json:"metadata"`
}

type pbmcEvalMetrics struct {
	Cases                int
	Passed               int
	PromptBytes          int
	PromptTokensEstimate int
	InputTokensActual    int
	OutputTokensActual   int
	TotalTokensActual    int
}

type pbmcEvalSummary struct {
	Suite       string                              `json:"suite"`
	Mode        string                              `json:"mode"`
	Model       string                              `json:"model,omitempty"`
	GeneratedAt time.Time                           `json:"generated_at"`
	Components  map[string]pbmcEvalComponentSummary `json:"components"`
}

type pbmcEvalComponentSummary struct {
	Cases                   int     `json:"cases"`
	Passed                  int     `json:"passed"`
	PassRate                float64 `json:"pass_rate"`
	AvgPromptBytes          int     `json:"avg_prompt_bytes"`
	AvgPromptTokensEstimate int     `json:"avg_prompt_tokens_estimate"`
	AvgInputTokensActual    int     `json:"avg_input_tokens_actual,omitempty"`
	AvgOutputTokensActual   int     `json:"avg_output_tokens_actual,omitempty"`
	AvgTotalTokensActual    int     `json:"avg_total_tokens_actual,omitempty"`
}

func TestPBMCEvalCorpusContextAndTokenBudget(t *testing.T) {
	suite := loadPBMCEvalSuite(t)
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	planner, err := NewLLMPlanner(LLMPlannerConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
		Skills:          registry,
	}, nil)
	if err != nil {
		t.Fatalf("create planner: %v", err)
	}
	answerer, err := NewLLMAnswerer(LLMAnswererConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
	}, nil)
	if err != nil {
		t.Fatalf("create answerer: %v", err)
	}
	evaluator, err := NewLLMEvaluator(LLMEvaluatorConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
	}, nil)
	if err != nil {
		t.Fatalf("create evaluator: %v", err)
	}

	metrics := map[string]*pbmcEvalMetrics{
		"planner":   {},
		"answerer":  {},
		"evaluator": {},
	}

	for _, tc := range suite.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			var body []byte
			switch tc.Component {
			case "planner":
				request := buildPBMCPlanningRequest(t, suite, tc)
				body = mustMarshalPBMCEvalJSON(t, planner.buildRequest(request))
			case "answerer":
				request := buildPBMCPlanningRequest(t, suite, tc)
				body = mustMarshalPBMCEvalJSON(t, answerer.buildRequest(request))
			case "evaluator":
				request := buildPBMCEvaluationRequest(t, suite, tc)
				body = mustMarshalPBMCEvalJSON(t, evaluator.buildRequest(request))
			default:
				t.Fatalf("unsupported component %q", tc.Component)
			}

			validatePBMCContextExpectations(t, tc, body)
			componentMetrics := metrics[tc.Component]
			componentMetrics.Cases++
			componentMetrics.Passed++
			componentMetrics.PromptBytes += len(body)
			componentMetrics.PromptTokensEstimate += estimateTokenCount(body)
			t.Logf("%s prompt_bytes=%d prompt_tokens_estimate=%d", tc.Component, len(body), estimateTokenCount(body))
		})
	}

	for component, metric := range metrics {
		if metric.Cases == 0 {
			continue
		}
		t.Logf(
			"%s offline summary: pass_rate=%d/%d avg_prompt_bytes=%d avg_prompt_tokens_estimate=%d",
			component,
			metric.Passed,
			metric.Cases,
			metric.PromptBytes/metric.Cases,
			metric.PromptTokensEstimate/metric.Cases,
		)
	}
	assertPBMCPromptTokenBudgets(t, metrics)
	emitPBMCEvalSummary(t, "offline", "", metrics)
}

func TestPBMCLiveLLMEval(t *testing.T) {
	if os.Getenv("SCAGENT_RUN_PBMC_LLM_EVAL") != "1" {
		t.Skip("set SCAGENT_RUN_PBMC_LLM_EVAL=1 to run live PBMC LLM eval")
	}

	loadPBMCEvalDotEnv(t)

	apiKey := strings.TrimSpace(os.Getenv("SCAGENT_OPENAI_API_KEY"))
	if apiKey == "" {
		t.Skip("SCAGENT_OPENAI_API_KEY is required for live PBMC LLM eval")
	}

	suite := loadPBMCEvalSuite(t)
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	baseURL := strings.TrimSpace(os.Getenv("SCAGENT_OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("SCAGENT_PBMC_EVAL_MODEL"))
	if model == "" {
		model = "gpt-5.4"
	}
	reasoningEffort := strings.TrimSpace(os.Getenv("SCAGENT_PBMC_EVAL_REASONING_EFFORT"))
	if reasoningEffort == "" {
		reasoningEffort = "low"
	}
	caseFilter := strings.TrimSpace(os.Getenv("SCAGENT_PBMC_EVAL_CASE_FILTER"))

	planner, err := NewLLMPlanner(LLMPlannerConfig{
		APIKey:          apiKey,
		BaseURL:         baseURL,
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Skills:          registry,
	}, nil)
	if err != nil {
		t.Fatalf("create planner: %v", err)
	}
	answerer, err := NewLLMAnswerer(LLMAnswererConfig{
		APIKey:          apiKey,
		BaseURL:         baseURL,
		Model:           model,
		ReasoningEffort: reasoningEffort,
	}, nil)
	if err != nil {
		t.Fatalf("create answerer: %v", err)
	}
	evaluator, err := NewLLMEvaluator(LLMEvaluatorConfig{
		APIKey:          apiKey,
		BaseURL:         baseURL,
		Model:           model,
		ReasoningEffort: reasoningEffort,
	}, nil)
	if err != nil {
		t.Fatalf("create evaluator: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	metrics := map[string]*pbmcEvalMetrics{
		"planner":   {},
		"answerer":  {},
		"evaluator": {},
	}

	for _, tc := range suite.Cases {
		tc := tc
		if caseFilter != "" && !strings.Contains(tc.ID, caseFilter) && !strings.Contains(tc.Component, caseFilter) {
			continue
		}

		t.Run(tc.ID, func(t *testing.T) {
			componentMetrics := metrics[tc.Component]
			componentMetrics.Cases++

			switch tc.Component {
			case "planner":
				request := buildPBMCPlanningRequest(t, suite, tc)
				requestBody := planner.buildRequest(request)
				payload := mustMarshalPBMCEvalJSON(t, requestBody)
				response, err := executePBMCResponsesRequest(ctx, planner.httpClient, planner.baseURL, planner.apiKey, requestBody)
				if err != nil {
					t.Fatalf("planner live eval failed: %v", err)
				}
				raw := extractPlannerText(response)
				var plan models.Plan
				if err := json.Unmarshal([]byte(raw), &plan); err != nil {
					t.Fatalf("decode planner plan: %v; raw=%s", err, raw)
				}
				validatePBMCPlannerResult(t, tc, plan)
				recordPBMCMetrics(componentMetrics, payload, response)
			case "answerer":
				request := buildPBMCPlanningRequest(t, suite, tc)
				requestBody := answerer.buildRequest(request)
				payload := mustMarshalPBMCEvalJSON(t, requestBody)
				response, err := executePBMCResponsesRequest(ctx, answerer.httpClient, answerer.baseURL, answerer.apiKey, requestBody)
				if err != nil {
					t.Fatalf("answerer live eval failed: %v", err)
				}
				raw := extractPlannerText(response)
				var decision directAnswerDecision
				if err := json.Unmarshal([]byte(raw), &decision); err != nil {
					t.Fatalf("decode answerer decision: %v; raw=%s", err, raw)
				}
				validatePBMCAnswererResult(t, tc, decision)
				recordPBMCMetrics(componentMetrics, payload, response)
			case "evaluator":
				request := buildPBMCEvaluationRequest(t, suite, tc)
				requestBody := evaluator.buildRequest(request)
				payload := mustMarshalPBMCEvalJSON(t, requestBody)
				response, err := executePBMCResponsesRequest(ctx, evaluator.httpClient, evaluator.baseURL, evaluator.apiKey, requestBody)
				if err != nil {
					t.Fatalf("evaluator live eval failed: %v", err)
				}
				raw := extractPlannerText(response)
				var evaluation CompletionEvaluation
				if err := json.Unmarshal([]byte(raw), &evaluation); err != nil {
					t.Fatalf("decode evaluator result: %v; raw=%s", err, raw)
				}
				validatePBMCEvaluatorResult(t, tc, evaluation)
				recordPBMCMetrics(componentMetrics, payload, response)
			default:
				t.Fatalf("unsupported component %q", tc.Component)
			}

			componentMetrics.Passed++
		})
	}

	for component, metric := range metrics {
		if metric.Cases == 0 {
			continue
		}
		t.Logf(
			"%s live summary: pass_rate=%d/%d avg_prompt_tokens_estimate=%d avg_input_tokens_actual=%d avg_output_tokens_actual=%d avg_total_tokens_actual=%d",
			component,
			metric.Passed,
			metric.Cases,
			metric.PromptTokensEstimate/metric.Cases,
			metric.InputTokensActual/metric.Cases,
			metric.OutputTokensActual/metric.Cases,
			metric.TotalTokensActual/metric.Cases,
		)
	}
	emitPBMCEvalSummary(t, "live", model, metrics)
}

func loadPBMCEvalSuite(t *testing.T) pbmcEvalSuite {
	t.Helper()

	_, currentFile, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(currentFile), "testdata", "pbmc_llm_eval_cases.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read PBMC eval suite: %v", err)
	}

	var suite pbmcEvalSuite
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatalf("decode PBMC eval suite: %v", err)
	}
	if suite.Suite == "" || len(suite.Cases) == 0 {
		t.Fatalf("invalid PBMC eval suite: %+v", suite)
	}
	return suite
}

func loadPBMCEvalDotEnv(t *testing.T) {
	t.Helper()

	_, currentFile, _, _ := runtime.Caller(0)
	path := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".env"))
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("set env from .env failed for %s: %v", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan .env failed: %v", err)
	}
}

func buildPBMCPlanningRequest(t *testing.T, suite pbmcEvalSuite, tc pbmcEvalCase) PlanningRequest {
	t.Helper()

	focusObject := buildPBMCFocusObject(suite, tc.ContextOverrides)
	recentPlotJob, recentArtifact, workingMemory := buildPBMCRecentPlotContext(suite, tc.ContextOverrides)
	objects := buildPBMCObjects(suite, focusObject)
	recentMessages := buildPBMCRecentMessages(tc.ContextOverrides)
	session := &models.Session{ID: "sess_pbmc_eval", WorkspaceID: "ws_pbmc_eval", DatasetID: "ds_pbmc", FocusObjectID: focusObject.ID, Label: "pbmc eval", Status: models.SessionActive}
	resolvedObjects := resolveObjectRoles(session, objects)
	request := PlanningRequest{
		Message:        tc.UserMessage,
		Session:        session,
		Workspace:      &models.Workspace{ID: "ws_pbmc_eval", DatasetID: "ds_pbmc", FocusObjectID: focusObject.ID, Label: "pbmc eval workspace"},
		FocusObject:    resolvedObjects.FocusObject,
		GlobalObject:   resolvedObjects.GlobalObject,
		RootObject:     resolvedObjects.RootObject,
		Objects:        objects,
		RecentMessages: recentMessages,
		WorkingMemory:  workingMemory,
	}
	if recentPlotJob != nil {
		request.RecentJobs = []*models.Job{recentPlotJob}
	}
	if recentArtifact != nil {
		request.RecentArtifacts = []*models.Artifact{recentArtifact}
	}

	if tc.ContextExpectations.InlineExplicitVisualBytes {
		expectedCount := tc.Expected.RequestBodyExpectations.InputImageCount
		if expectedCount <= 0 {
			expectedCount = 1
		}
		request.InputArtifacts = buildPBMCInputImageArtifacts(t, expectedCount)
	}

	return request
}

func buildPBMCEvaluationRequest(t *testing.T, suite pbmcEvalSuite, tc pbmcEvalCase) EvaluationRequest {
	t.Helper()

	planning := buildPBMCPlanningRequest(t, suite, tc)
	request := EvaluationRequest{
		Message:         planning.Message,
		Session:         planning.Session,
		Workspace:       planning.Workspace,
		FocusObject:     planning.FocusObject,
		GlobalObject:    planning.GlobalObject,
		RootObject:      planning.RootObject,
		Objects:         planning.Objects,
		InputArtifacts:  planning.InputArtifacts,
		RecentMessages:  planning.RecentMessages,
		RecentJobs:      planning.RecentJobs,
		RecentArtifacts: planning.RecentArtifacts,
		WorkingMemory:   planning.WorkingMemory,
	}
	if tc.CurrentJob != nil {
		request.CurrentJob = buildPBMCCurrentJob(tc.CurrentJob)
	}
	return request
}

func buildPBMCFocusObject(suite pbmcEvalSuite, overrides *pbmcEvalContextOverrides) *models.ObjectMeta {
	signals := suite.Assumptions.FocusObject
	if overrides != nil && overrides.FocusObject != nil {
		signals = *overrides.FocusObject
	}
	objectID := "obj_" + sanitizePBMCEvalID(signals.Label)
	if objectID == "obj_" {
		objectID = "obj_pbmc3k"
	}
	kind := signals.Kind
	if kind == "" {
		kind = string(models.ObjectFilteredDataset)
	}
	return &models.ObjectMeta{
		ID:    objectID,
		Label: signals.Label,
		Kind:  models.ObjectKind(kind),
		NObs:  signals.NObs,
		NVars: signals.NVars,
		Metadata: map[string]any{
			"obs_fields": coalesceStringSlice(signals.ObsFields, suite.Assumptions.ObjectSignals.ObsFields),
			"obsm_keys":  coalesceStringSlice(signals.ObsmKeys, suite.Assumptions.ObjectSignals.ObsmKeys),
			"assessment": map[string]any{
				"available_analyses": coalesceStringSlice(signals.AvailableAnalyses, suite.Assumptions.ObjectSignals.AvailableAnalyses),
				"has_umap":           containsString(coalesceStringSlice(signals.ObsmKeys, suite.Assumptions.ObjectSignals.ObsmKeys), "X_umap"),
			},
		},
	}
}

func buildPBMCObjects(suite pbmcEvalSuite, focusObject *models.ObjectMeta) []*models.ObjectMeta {
	defaultObject := buildPBMCFocusObject(suite, nil)
	if focusObject != nil &&
		focusObject.ID != defaultObject.ID &&
		focusObject.ParentID == "" &&
		(focusObject.Kind == models.ObjectSubset || focusObject.Kind == models.ObjectReclustered) {
		focusObject.ParentID = defaultObject.ID
	}
	objects := []*models.ObjectMeta{focusObject}
	if focusObject != nil && focusObject.ID != defaultObject.ID {
		objects = append(objects, defaultObject)
	}
	return objects
}

func buildPBMCRecentMessages(overrides *pbmcEvalContextOverrides) []*models.Message {
	includeDefaults := true
	if overrides != nil && overrides.IncludeDefaultRecentMessages != nil {
		includeDefaults = *overrides.IncludeDefaultRecentMessages
	}
	messages := make([]*models.Message, 0, 4)
	if includeDefaults {
		messages = append(messages,
			&models.Message{
				ID:        "msg_prev_user",
				SessionID: "sess_pbmc_eval",
				Role:      models.MessageUser,
				Content:   "画一下细胞类型的 UMAP",
			},
			&models.Message{
				ID:        "msg_prev_assistant",
				SessionID: "sess_pbmc_eval",
				Role:      models.MessageAssistant,
				Content:   "已经画好了按 cell_type 着色的 UMAP。",
			},
		)
	}
	if overrides == nil || len(overrides.RecentMessages) == 0 {
		return messages
	}
	for index, message := range overrides.RecentMessages {
		role := models.MessageUser
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "assistant":
			role = models.MessageAssistant
		case "system":
			role = models.MessageSystem
		}
		messages = append(messages, &models.Message{
			ID:        fmt.Sprintf("msg_override_%d", index+1),
			SessionID: "sess_pbmc_eval",
			Role:      role,
			Content:   message.Content,
		})
	}
	return messages
}

func buildPBMCRecentPlotContext(suite pbmcEvalSuite, overrides *pbmcEvalContextOverrides) (*models.Job, *models.Artifact, *models.WorkingMemory) {
	includeDefaults := true
	if overrides != nil && overrides.IncludeDefaultRecentPlot != nil {
		includeDefaults = *overrides.IncludeDefaultRecentPlot
	}
	if !includeDefaults && (overrides == nil || overrides.RecentPlot == nil) {
		return nil, nil, buildPBMCWorkingMemory(nil)
	}

	plot := suite.Assumptions.RecentPlotDefaults
	objectID := "obj_pbmc3k"
	objectLabel := suite.Assumptions.FocusObject.Label
	artifactTitle := "PBMC cell type UMAP"
	artifactSummary := "PBMC cell type UMAP colored by cell_type."
	if overrides != nil && overrides.RecentPlot != nil {
		override := overrides.RecentPlot
		if strings.TrimSpace(override.Skill) != "" {
			plot.Skill = override.Skill
		}
		if strings.TrimSpace(override.ColorBy) != "" {
			plot.ColorBy = override.ColorBy
		}
		if strings.TrimSpace(override.LegendLoc) != "" {
			plot.LegendLoc = override.LegendLoc
		}
		if override.PointSize != 0 {
			plot.PointSize = override.PointSize
		}
		if strings.TrimSpace(override.Title) != "" {
			plot.Title = override.Title
		}
		if strings.TrimSpace(override.ObjectID) != "" {
			objectID = override.ObjectID
		}
		if strings.TrimSpace(override.ObjectLabel) != "" {
			objectLabel = override.ObjectLabel
		}
		if strings.TrimSpace(override.ArtifactTitle) != "" {
			artifactTitle = override.ArtifactTitle
		}
		if strings.TrimSpace(override.ArtifactSummary) != "" {
			artifactSummary = override.ArtifactSummary
		}
	}

	job := &models.Job{
		ID:        "job_prev_plot",
		SessionID: "sess_pbmc_eval",
		Status:    models.JobSucceeded,
		Summary:   "已生成最近图像结果。",
		Steps: []models.JobStep{{
			ID:      "step_1",
			Skill:   plot.Skill,
			Status:  models.JobSucceeded,
			Summary: "已生成最近图像结果。",
			Params: map[string]any{
				"color_by":   plot.ColorBy,
				"legend_loc": plot.LegendLoc,
				"point_size": plot.PointSize,
				"title":      plot.Title,
			},
			Metadata: map[string]any{
				"color_by":   plot.ColorBy,
				"legend_loc": plot.LegendLoc,
				"point_size": plot.PointSize,
				"title":      plot.Title,
			},
		}},
	}
	artifact := &models.Artifact{
		ID:          "artifact_prev_plot",
		Kind:        models.ArtifactPlot,
		ObjectID:    objectID,
		JobID:       "job_prev_plot",
		Title:       artifactTitle,
		ContentType: "image/png",
		Summary:     artifactSummary,
	}
	memory := buildPBMCWorkingMemory(&pbmcEvalRecentPlotContext{
		ObjectID:        objectID,
		ObjectLabel:     objectLabel,
		ArtifactTitle:   artifactTitle,
		ArtifactSummary: artifactSummary,
		Plot:            plot,
	})
	return job, artifact, memory
}

type pbmcEvalRecentPlotContext struct {
	ObjectID        string
	ObjectLabel     string
	ArtifactTitle   string
	ArtifactSummary string
	Plot            pbmcEvalRecentPlot
}

func buildPBMCWorkingMemory(context *pbmcEvalRecentPlotContext) *models.WorkingMemory {
	if context == nil {
		return nil
	}
	return &models.WorkingMemory{
		Focus: &models.WorkingMemoryFocus{
			FocusObjectID:         context.ObjectID,
			FocusObjectLabel:      context.ObjectLabel,
			LastArtifactID:        "artifact_prev_plot",
			LastArtifactTitle:     context.ArtifactTitle,
			LastOutputObjectID:    context.ObjectID,
			LastOutputObjectLabel: context.ObjectLabel,
		},
		RecentArtifacts: []models.WorkingMemoryArtifactRef{{
			ID:       "artifact_prev_plot",
			Kind:     models.ArtifactPlot,
			ObjectID: context.ObjectID,
			JobID:    "job_prev_plot",
			Title:    context.ArtifactTitle,
			Summary:  context.ArtifactSummary,
		}},
		ConfirmedPreferences: []models.WorkingMemoryPreference{
			{Skill: "plot_umap", Param: "color_by", Value: context.Plot.ColorBy},
			{Skill: "plot_umap", Param: "legend_loc", Value: context.Plot.LegendLoc},
			{Skill: "plot_umap", Param: "point_size", Value: context.Plot.PointSize},
			{Skill: "plot_umap", Param: "title", Value: context.Plot.Title},
		},
	}
}

func coalesceStringSlice(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func sanitizePBMCEvalID(label string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	replacer := strings.NewReplacer(" ", "_", "-", "_", "/", "_")
	label = replacer.Replace(label)
	label = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_':
			return r
		default:
			return -1
		}
	}, label)
	return label
}

func buildPBMCRecentMessagesLegacy() []*models.Message {
	return []*models.Message{
		{
			ID:        "msg_prev_user",
			SessionID: "sess_pbmc_eval",
			Role:      models.MessageUser,
			Content:   "画一下细胞类型的 UMAP",
		},
		{
			ID:        "msg_prev_assistant",
			SessionID: "sess_pbmc_eval",
			Role:      models.MessageAssistant,
			Content:   "已经画好了按 cell_type 着色的 UMAP。",
		},
	}
}

func buildPBMCInputImageArtifacts(t *testing.T, count int) []*models.Artifact {
	t.Helper()

	if count <= 0 {
		return nil
	}
	tempDir := t.TempDir()
	artifacts := make([]*models.Artifact, 0, count)
	for i := 0; i < count; i++ {
		imagePath := filepath.Join(tempDir, fmt.Sprintf("pbmc_input_%d.png", i+1))
		if err := writeValidPNG(imagePath); err != nil {
			t.Fatalf("write PBMC input image %d: %v", i+1, err)
		}
		artifacts = append(artifacts, &models.Artifact{
			ID:          fmt.Sprintf("artifact_input_image_%d", i+1),
			Kind:        models.ArtifactPlot,
			Title:       fmt.Sprintf("PBMC uploaded UMAP %d", i+1),
			Path:        imagePath,
			ContentType: "image/png",
			Summary:     fmt.Sprintf("用户上传的第 %d 张 PBMC UMAP 图片。", i+1),
		})
	}
	return artifacts
}

func writeValidPNG(path string) error {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	img.Set(1, 0, color.NRGBA{R: 0, G: 122, B: 255, A: 255})
	img.Set(0, 1, color.NRGBA{R: 255, G: 59, B: 48, A: 255})
	img.Set(1, 1, color.NRGBA{R: 52, G: 199, B: 89, A: 255})

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

func buildPBMCCurrentJob(current *pbmcEvalCurrentJob) *models.Job {
	if current == nil {
		return nil
	}

	jobStatus := models.JobStatus(current.Status)
	if current.Status == "running_or_partial" {
		jobStatus = models.JobRunning
	}

	steps := make([]models.JobStep, 0, len(current.Steps))
	for index, step := range current.Steps {
		metadata := map[string]any{}
		for key, value := range step.Metadata {
			metadata[key] = value
		}
		if strings.TrimSpace(step.ArtifactKind) != "" {
			metadata["artifact_kind"] = step.ArtifactKind
		}
		steps = append(steps, models.JobStep{
			ID:       fmt.Sprintf("step_%d", index+1),
			Skill:    step.Skill,
			Status:   models.JobStatus(step.Status),
			Summary:  step.Summary,
			Metadata: metadata,
		})
	}

	return &models.Job{
		ID:        "job_eval_current",
		SessionID: "sess_pbmc_eval",
		Status:    jobStatus,
		Steps:     steps,
	}
}

func validatePBMCContextExpectations(t *testing.T, tc pbmcEvalCase, body []byte) {
	t.Helper()

	if tc.ContextExpectations.CompactContext && !bytes.Contains(body, []byte(`Current session context:`)) && !bytes.Contains(body, []byte(`Current execution context:`)) {
		t.Fatalf("expected compact context marker in request body: %s", string(body))
	}
	if tc.ContextExpectations.InlineRecentVisualBytes && !bytes.Contains(body, []byte(`"input_image"`)) {
		t.Fatalf("expected recent visual bytes in request body: %s", string(body))
	}
	if !tc.ContextExpectations.InlineRecentVisualBytes && tc.ContextExpectations.InlineExplicitVisualBytes == false && bytes.Contains(body, []byte(`"input_image"`)) {
		t.Fatalf("request body unexpectedly inlined image bytes: %s", string(body))
	}
	if tc.ContextExpectations.InlineExplicitVisualBytes {
		expectedCount := tc.Expected.RequestBodyExpectations.InputImageCount
		if expectedCount == 0 {
			expectedCount = 1
		}
		if count := bytes.Count(body, []byte(`"input_image"`)); count != expectedCount {
			t.Fatalf("expected %d explicit image inputs, got %d: %s", expectedCount, count, string(body))
		}
	}
	if tc.ContextExpectations.ShouldUseRecentPlotMetadata {
		for _, signal := range []string{`plot_umap`, `color_by`, `legend_loc`, `point_size`, `title`} {
			if !bytes.Contains(body, []byte(signal)) {
				t.Fatalf("expected recent plot metadata signal %q in request body: %s", signal, string(body))
			}
		}
	}
	if field := strings.TrimSpace(tc.ContextExpectations.ShouldUseObsField); field != "" && !bytes.Contains(body, []byte(field)) {
		t.Fatalf("expected obs field %q in request body: %s", field, string(body))
	}
	if tc.ContextExpectations.RecentVisualArtifactsShouldRemainSummaryOnly && !bytes.Contains(body, []byte(`PBMC cell type UMAP`)) {
		t.Fatalf("expected recent artifact summary in request body: %s", string(body))
	}
}

func validatePBMCPlannerResult(t *testing.T, tc pbmcEvalCase, plan models.Plan) {
	t.Helper()

	if len(plan.Steps) != len(tc.Expected.Steps) {
		t.Fatalf("unexpected planner step count: got %d want %d; plan=%+v", len(plan.Steps), len(tc.Expected.Steps), plan)
	}
	for index, expected := range tc.Expected.Steps {
		actual := plan.Steps[index]
		if actual.Skill != expected.Skill {
			t.Fatalf("step %d skill mismatch: got %q want %q", index, actual.Skill, expected.Skill)
		}
		if expected.TargetObjectID != "" && actual.TargetObjectID != expected.TargetObjectID {
			t.Fatalf("step %d target mismatch: got %q want %q", index, actual.TargetObjectID, expected.TargetObjectID)
		}
		for key, value := range expected.Params {
			if !jsonValueEquals(actual.Params[key], value) {
				t.Fatalf("step %d param %q mismatch: got %#v want %#v", index, key, actual.Params[key], value)
			}
		}
		for key, value := range expected.ParamsShouldPreserve {
			if !jsonValueEquals(actual.Params[key], value) {
				t.Fatalf("step %d preserve param %q mismatch: got %#v want %#v", index, key, actual.Params[key], value)
			}
		}
		for key, value := range expected.ParamsShouldOverride {
			if !jsonValueEquals(actual.Params[key], value) {
				t.Fatalf("step %d override param %q mismatch: got %#v want %#v", index, key, actual.Params[key], value)
			}
		}
	}
}

func validatePBMCAnswererResult(t *testing.T, tc pbmcEvalCase, decision directAnswerDecision) {
	t.Helper()

	expectedDecision := strings.TrimSpace(tc.Expected.Decision)
	switch expectedDecision {
	case "", "direct_answer_or_needs_execution":
		// Accept either; still validate image case payloads via offline test.
	default:
		if decision.Decision != expectedDecision {
			t.Fatalf("answerer decision mismatch: got %q want %q", decision.Decision, expectedDecision)
		}
	}
	if tc.Expected.Confidence != "" && decision.Confidence != tc.Expected.Confidence {
		t.Fatalf("answerer confidence mismatch: got %q want %q", decision.Confidence, tc.Expected.Confidence)
	}
	for _, fragment := range tc.Expected.AnswerShouldMention {
		if !strings.Contains(decision.Answer, fragment) {
			t.Fatalf("answerer answer %q should mention %q", decision.Answer, fragment)
		}
	}
	for _, evidence := range tc.Expected.EvidenceShouldInclude {
		if !containsStringOrSubstring(decision.Evidence, evidence) {
			t.Fatalf("answerer evidence %+v should include %q", decision.Evidence, evidence)
		}
	}
}

func validatePBMCEvaluatorResult(t *testing.T, tc pbmcEvalCase, evaluation CompletionEvaluation) {
	t.Helper()

	if tc.Expected.Completed != nil && evaluation.Completed != *tc.Expected.Completed {
		t.Fatalf("evaluator completed mismatch: got %v want %v", evaluation.Completed, *tc.Expected.Completed)
	}
	for _, fragment := range tc.Expected.ReasonShouldMention {
		if !strings.Contains(strings.ToLower(evaluation.Reason), strings.ToLower(fragment)) {
			t.Fatalf("evaluator reason %q should mention %q", evaluation.Reason, fragment)
		}
	}
}

func executePBMCResponsesRequest(ctx context.Context, httpClient *http.Client, baseURL, apiKey string, requestBody map[string]any) (openAIResponsesResponse, error) {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return openAIResponsesResponse{}, fmt.Errorf("marshal eval request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/responses", bytes.NewReader(payload))
	if err != nil {
		return openAIResponsesResponse{}, fmt.Errorf("create eval request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := httpClient.Do(request)
	if err != nil {
		return openAIResponsesResponse{}, fmt.Errorf("eval request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return openAIResponsesResponse{}, fmt.Errorf("read eval response: %w", err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return openAIResponsesResponse{}, fmt.Errorf("eval request returned %s: %s", response.Status, compactJSON(string(body)))
	}

	var decoded openAIResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return openAIResponsesResponse{}, fmt.Errorf("decode eval response: %w", err)
	}
	return decoded, nil
}

func recordPBMCMetrics(metrics *pbmcEvalMetrics, payload []byte, response openAIResponsesResponse) {
	metrics.PromptBytes += len(payload)
	metrics.PromptTokensEstimate += estimateTokenCount(payload)
	metrics.InputTokensActual += response.Usage.InputTokens
	metrics.OutputTokensActual += response.Usage.OutputTokens
	metrics.TotalTokensActual += response.Usage.TotalTokens
}

func estimateTokenCount(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	return (len(payload) + 3) / 4
}

func assertPBMCPromptTokenBudgets(t *testing.T, metrics map[string]*pbmcEvalMetrics) {
	t.Helper()

	budgets := map[string]int{
		"planner":   3200,
		"answerer":  1200,
		"evaluator": 1200,
	}
	for component, budget := range budgets {
		metric := metrics[component]
		if metric == nil || metric.Cases == 0 {
			continue
		}
		avg := metric.PromptTokensEstimate / metric.Cases
		if avg > budget {
			t.Fatalf("%s prompt token budget exceeded: avg=%d budget=%d", component, avg, budget)
		}
	}
}

func emitPBMCEvalSummary(t *testing.T, mode, model string, metrics map[string]*pbmcEvalMetrics) {
	t.Helper()

	summary := pbmcEvalSummary{
		Suite:       "pbmc_llm_eval_cases",
		Mode:        mode,
		Model:       model,
		GeneratedAt: time.Now().UTC(),
		Components:  make(map[string]pbmcEvalComponentSummary, len(metrics)),
	}

	for component, metric := range metrics {
		if metric == nil || metric.Cases == 0 {
			continue
		}
		componentSummary := pbmcEvalComponentSummary{
			Cases:                   metric.Cases,
			Passed:                  metric.Passed,
			PassRate:                float64(metric.Passed) / float64(metric.Cases),
			AvgPromptBytes:          metric.PromptBytes / metric.Cases,
			AvgPromptTokensEstimate: metric.PromptTokensEstimate / metric.Cases,
		}
		if metric.InputTokensActual > 0 {
			componentSummary.AvgInputTokensActual = metric.InputTokensActual / metric.Cases
		}
		if metric.OutputTokensActual > 0 {
			componentSummary.AvgOutputTokensActual = metric.OutputTokensActual / metric.Cases
		}
		if metric.TotalTokensActual > 0 {
			componentSummary.AvgTotalTokensActual = metric.TotalTokensActual / metric.Cases
		}
		summary.Components[component] = componentSummary
	}

	payload, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("marshal PBMC eval summary: %v", err)
	}
	t.Logf("PBMC_EVAL_SUMMARY %s", string(payload))

	summaryDir := strings.TrimSpace(os.Getenv("SCAGENT_PBMC_EVAL_SUMMARY_DIR"))
	if summaryDir == "" {
		return
	}
	if err := os.MkdirAll(summaryDir, 0o755); err != nil {
		t.Fatalf("create PBMC eval summary dir: %v", err)
	}
	filename := fmt.Sprintf("pbmc_eval_%s_summary.json", mode)
	path := filepath.Join(summaryDir, filename)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write PBMC eval summary: %v", err)
	}
	t.Logf("wrote PBMC eval summary to %s", path)
}

func mustMarshalPBMCEvalJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal eval JSON: %v", err)
	}
	return payload
}

func jsonValueEquals(left, right any) bool {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return bytes.Equal(leftJSON, rightJSON)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsStringOrSubstring(values []string, target string) bool {
	for _, value := range values {
		if value == target || strings.Contains(value, target) {
			return true
		}
	}
	return false
}
