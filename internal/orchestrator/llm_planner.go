package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"scagent/internal/models"
	"scagent/internal/skill"
)

type PlannerConfig struct {
	Mode            string
	OpenAIAPIKey    string
	OpenAIBaseURL   string
	OpenAIModel     string
	ReasoningEffort string
	Skills          *skill.Registry
}

func NewPlanner(config PlannerConfig) (Planner, error) {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode == "" {
		return nil, fmt.Errorf("planner mode is required")
	}
	if mode == "fake" {
		return NewFakePlanner(), nil
	}
	if mode != "llm" {
		return nil, fmt.Errorf("unsupported planner mode %q", config.Mode)
	}

	if strings.TrimSpace(config.OpenAIAPIKey) == "" {
		return nil, fmt.Errorf("planner mode llm requires SCAGENT_OPENAI_API_KEY or -openai-api-key")
	}
	if config.Skills == nil {
		return nil, fmt.Errorf("planner mode llm requires a skill registry")
	}

	return NewLLMPlanner(LLMPlannerConfig{
		APIKey:          config.OpenAIAPIKey,
		BaseURL:         config.OpenAIBaseURL,
		Model:           config.OpenAIModel,
		ReasoningEffort: config.ReasoningEffort,
		Skills:          config.Skills,
	}, nil)
}

type LLMPlannerConfig struct {
	APIKey          string
	BaseURL         string
	Model           string
	ReasoningEffort string
	Skills          *skill.Registry
}

type LLMPlanner struct {
	apiKey          string
	baseURL         string
	model           string
	reasoningEffort string
	skills          *skill.Registry
	httpClient      *http.Client
	healthMu        sync.Mutex
	healthCheckedAt time.Time
	healthErr       error
}

func NewLLMPlanner(config LLMPlannerConfig, httpClient *http.Client) (*LLMPlanner, error) {
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

	if config.Skills == nil {
		return nil, fmt.Errorf("LLM planner requires a skill registry")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("LLM planner requires an API key")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}

	return &LLMPlanner{
		apiKey:          config.APIKey,
		baseURL:         baseURL,
		model:           model,
		reasoningEffort: reasoningEffort,
		skills:          config.Skills,
		httpClient:      httpClient,
	}, nil
}

func (p *LLMPlanner) Mode() string {
	return "llm"
}

func (p *LLMPlanner) Health(ctx context.Context) error {
	const healthCacheTTL = 15 * time.Second

	p.healthMu.Lock()
	if !p.healthCheckedAt.IsZero() && time.Since(p.healthCheckedAt) < healthCacheTTL {
		err := p.healthErr
		p.healthMu.Unlock()
		return err
	}
	p.healthMu.Unlock()

	err := p.probeHealth(ctx)

	p.healthMu.Lock()
	p.healthCheckedAt = time.Now()
	p.healthErr = err
	p.healthMu.Unlock()

	return err
}

func (p *LLMPlanner) Plan(ctx context.Context, requestPayload PlanningRequest) (models.Plan, error) {
	payload, err := json.Marshal(p.buildRequest(requestPayload))
	if err != nil {
		return models.Plan{}, fmt.Errorf("marshal planner request: %w", err)
	}

	body, err := p.executeResponsesRequest(ctx, payload)
	if err != nil {
		return models.Plan{}, err
	}

	var decoded openAIResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return models.Plan{}, fmt.Errorf("decode planner response: %w", err)
	}

	rawPlan := extractPlannerText(decoded)
	if strings.TrimSpace(rawPlan) == "" {
		return models.Plan{}, fmt.Errorf("planner response did not contain text output")
	}

	var plan models.Plan
	if err := json.Unmarshal([]byte(rawPlan), &plan); err != nil {
		return models.Plan{}, fmt.Errorf("decode planner plan JSON: %w", err)
	}
	return plan, nil
}

func (p *LLMPlanner) executeResponsesRequest(ctx context.Context, payload []byte) ([]byte, error) {
	const maxAttempts = 2

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/responses", bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create planner request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+p.apiKey)
		request.Header.Set("Content-Type", "application/json")

		response, err := p.httpClient.Do(request)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts && shouldRetryPlannerRequest(ctx, err) {
				continue
			}
			return nil, fmt.Errorf("planner request failed: %w", err)
		}

		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read planner response: %w", readErr)
		}
		if response.StatusCode >= http.StatusBadRequest {
			return nil, fmt.Errorf("planner returned %s: %s", response.Status, compactJSON(string(body)))
		}
		return body, nil
	}

	return nil, fmt.Errorf("planner request failed: %w", lastErr)
}

func shouldRetryPlannerRequest(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func (p *LLMPlanner) DebugPreview(_ context.Context, requestPayload PlanningRequest) (*PlannerDebugPreview, error) {
	return &PlannerDebugPreview{
		PlannerMode:           p.Mode(),
		PlanningRequest:       requestPayload,
		PlannerContext:        formatPlannerContext(requestPayload),
		DeveloperInstructions: p.instructions(requestPayload),
		RequestBody:           p.buildRequest(requestPayload),
		Note:                  "Authorization headers are omitted from this preview. planning_request is the internal snapshot; planner_context and request_body reflect the compact context actually sent to the planner.",
	}, nil
}

func (p *LLMPlanner) probeHealth(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/responses", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("create planner health check request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := p.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("planner request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read planner health check response: %w", err)
	}

	switch response.StatusCode {
	case http.StatusOK, http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil
	default:
		return fmt.Errorf("planner health check returned %s: %s", response.Status, compactJSON(string(body)))
	}
}

func (p *LLMPlanner) buildRequest(requestPayload PlanningRequest) map[string]any {
	return map[string]any{
		"model": p.model,
		"reasoning": map[string]any{
			"effort": p.reasoningEffort,
		},
		"input": []map[string]any{
			{
				"role":    "developer",
				"content": p.instructions(requestPayload),
			},
			{
				"role":    "user",
				"content": buildPlannerUserInputContent(requestPayload.Message),
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "scagent_plan",
				"strict": true,
				"schema": planSchema(p.skills.ListExecutable()),
			},
		},
	}
}

func (p *LLMPlanner) instructions(requestPayload PlanningRequest) string {
	lines := []string{
		"You are the scAgent planner.",
		"Return a minimal JSON execution plan using only listed skills.",
		"Use \"$focus\" for the focused object, \"$global\" for the best whole-dataset object in the same lineage, \"$root\" for the lineage root object, and \"$prev\" for chaining derived outputs.",
		"Prefer the fewest valid steps; compose multiple steps only when the request is clearly a workflow.",
		"Do not assume the focused object is correct for full-dataset requests. When the user asks for the whole dataset, all cells, or the current dataset rather than the current subset/object, prefer \"$global\".",
		"When the user explicitly refers to this object/current object/当前这个对象, prefer \"$focus\".",
		"Use recent_messages, recent_jobs, recent_artifacts, and working_memory for follow-up intent such as edit-this-plot requests.",
		"Artifact context is summary-only; do not assume raw file contents or image bytes unless explicitly present in the user turn.",
		"Preserve prior plot params/metadata for follow-up edits unless the user explicitly changes them.",
		"Record memory_refs when a step depends on working_memory or other prior context.",
		"Prefer wired skills over run_python_analysis. Use run_python_analysis only as a last resort for unsupported operations.",
		"Prefer plot_gene_umap for gene-colored UMAP requests, subset_cells+plot_umap for isolate-then-plot requests, reanalyze_subset for already extracted subsets, and write_method for Methods/方法描述 requests.",
		"After generation, ensure the plan is executable: required params must be present and steps must not be empty.",
		"Available skills:",
	}

	for _, definition := range p.skills.ListExecutable() {
		lines = append(lines, formatSkillInstruction(definition))
	}

	lines = append(lines, "Current session context:")
	lines = append(lines, formatPlannerContext(requestPayload)...)

	return strings.Join(lines, "\n")
}

func formatSkillInstruction(definition skill.Definition) string {
	inputNames := make([]string, 0, len(definition.Input))
	required := make([]string, 0, len(definition.Input))
	for name, field := range definition.Input {
		inputNames = append(inputNames, name)
		if field.Required {
			required = append(required, name)
		}
	}
	sort.Strings(inputNames)
	sort.Strings(required)

	inputs := strings.Join(inputNames, ",")
	requiredInputs := strings.Join(required, ",")
	if inputs == "" {
		inputs = "-"
	}
	if requiredInputs == "" {
		requiredInputs = "-"
	}

	return fmt.Sprintf(
		"- %s(%s) req=[%s]: %s",
		definition.Name,
		inputs,
		requiredInputs,
		definition.Description,
	)
}

func planSchema(definitions []skill.Definition) map[string]any {
	skillNames := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		skillNames = append(skillNames, definition.Name)
	}
	sort.Strings(skillNames)

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"steps"},
		"properties": map[string]any{
			"steps": map[string]any{
				"type":     "array",
				"minItems": 1,
				"items":    genericPlanStepSchema(skillNames, definitions),
			},
		},
	}
}

func genericPlanStepSchema(skillNames []string, definitions []skill.Definition) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"skill", "target_object_id", "params", "memory_refs"},
		"properties": map[string]any{
			"skill": map[string]any{
				"type": "string",
				"enum": skillNames,
			},
			"target_object_id": nullableSchema(map[string]any{"type": "string"}),
			"params":           genericParamsSchema(definitions),
			"memory_refs": nullableSchema(map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			}),
		},
	}
}

func genericParamsSchema(definitions []skill.Definition) map[string]any {
	propertyVariants := make(map[string][]map[string]any)
	for _, definition := range definitions {
		for name, field := range definition.Input {
			if name == "target_object_id" {
				continue
			}
			schema := fieldSchema(field)
			if !schemaAllowsNull(schema) {
				schema = nullableSchema(schema)
			}
			propertyVariants[name] = appendUniqueSchema(propertyVariants[name], schema)
		}
	}

	inputNames := make([]string, 0, len(propertyVariants))
	for name := range propertyVariants {
		inputNames = append(inputNames, name)
	}
	sort.Strings(inputNames)

	properties := make(map[string]any, len(inputNames))
	for _, name := range inputNames {
		variants := propertyVariants[name]
		if len(variants) == 1 {
			properties[name] = variants[0]
			continue
		}
		anyOf := make([]any, 0, len(variants))
		for _, variant := range variants {
			anyOf = append(anyOf, variant)
		}
		properties[name] = map[string]any{"anyOf": anyOf}
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             inputNames,
	}
}

func appendUniqueSchema(existing []map[string]any, candidate map[string]any) []map[string]any {
	signature := mustMarshalJSON(candidate)
	for _, schema := range existing {
		if mustMarshalJSON(schema) == signature {
			return existing
		}
	}
	return append(existing, candidate)
}

func schemaAllowsNull(schema map[string]any) bool {
	anyOf, ok := schema["anyOf"].([]any)
	if !ok {
		return false
	}
	for _, candidate := range anyOf {
		entry, ok := candidate.(map[string]any)
		if !ok {
			continue
		}
		if entry["type"] == "null" {
			return true
		}
	}
	return false
}

func fieldSchema(field skill.FieldSchema) map[string]any {
	var schema map[string]any

	switch field.Type {
	case "string":
		schema = map[string]any{
			"type": "string",
		}
		if len(field.Enum) > 0 {
			schema["enum"] = field.Enum
		}
	case "number":
		schema = map[string]any{
			"type": "number",
		}
	case "boolean":
		schema = map[string]any{
			"type": "boolean",
		}
	case "array":
		schema = map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "string",
			},
		}
	case "string|number|array":
		schema = map[string]any{
			"anyOf": []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "number"},
				map[string]any{
					"type": "array",
					"items": map[string]any{
						"anyOf": []any{
							map[string]any{"type": "string"},
							map[string]any{"type": "number"},
						},
					},
				},
			},
		}
	default:
		schema = map[string]any{
			"type": "string",
		}
	}

	if field.Required {
		return schema
	}

	return nullableSchema(schema)
}

func nullableSchema(schema map[string]any) map[string]any {
	return map[string]any{
		"anyOf": []any{
			schema,
			map[string]any{"type": "null"},
		},
	}
}

type openAIResponsesResponse struct {
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func extractPlannerText(response openAIResponsesResponse) string {
	for _, item := range response.Output {
		for _, content := range item.Content {
			if strings.TrimSpace(content.Text) != "" {
				return content.Text
			}
		}
	}
	return ""
}

func compactJSON(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}

func buildPlannerUserInputContent(message string) any {
	return strings.TrimSpace(message)
}

func formatPlannerContext(request PlanningRequest) []string {
	return formatPlanningContextWithPolicy(request, plannerPlanningContextPolicy())
}

func formatPlanningContext(request PlanningRequest) []string {
	lines := make([]string, 0, 24)
	if request.Session != nil {
		lines = append(lines, fmt.Sprintf("- session_id=%s", request.Session.ID))
	}

	lines = append(lines, formatFullResolvedObjectContext("focus_object", request.FocusObject)...)
	lines = append(lines, formatFullResolvedObjectContext("global_object", request.GlobalObject)...)
	lines = append(lines, formatFullResolvedObjectContext("root_object", request.RootObject)...)

	if len(request.Objects) == 0 {
		lines = append(lines, "- objects=none")
	} else {
		lines = append(lines, "- objects:")
		for _, object := range request.Objects {
			lines = append(lines, "  "+formatObjectContext(object))
		}
	}

	if len(request.InputArtifacts) == 0 {
		lines = append(lines, "- input_artifacts=none")
	} else {
		lines = append(lines, "- input_artifacts:")
		for _, artifact := range request.InputArtifacts {
			lines = append(lines, "  "+formatArtifactContext(artifact))
		}
	}

	if len(request.RecentMessages) == 0 {
		lines = append(lines, "- recent_messages=none")
	} else {
		lines = append(lines, "- recent_messages:")
		for _, message := range request.RecentMessages {
			lines = append(lines, "  "+formatMessageContext(message))
		}
	}

	if len(request.RecentJobs) == 0 {
		lines = append(lines, "- recent_jobs=none")
	} else {
		lines = append(lines, "- recent_jobs:")
		for _, job := range request.RecentJobs {
			lines = append(lines, "  "+formatJobContext(job))
		}
	}

	if len(request.RecentArtifacts) == 0 {
		lines = append(lines, "- recent_artifacts=none")
	} else {
		lines = append(lines, "- recent_artifacts:")
		for _, artifact := range request.RecentArtifacts {
			lines = append(lines, "  "+formatArtifactContext(artifact))
		}
	}
	lines = append(lines, formatWorkingMemoryContext(request.WorkingMemory)...)
	return lines
}

func formatFullResolvedObjectContext(label string, object *models.ObjectMeta) []string {
	if object == nil {
		return []string{"- " + label + "=none"}
	}
	return []string{"- " + label + "=" + formatObjectContext(object)}
}

func formatPlannerArtifactGroup(label string, artifacts []*models.Artifact, limit int) []string {
	if len(artifacts) == 0 || limit <= 0 {
		return []string{"- " + label + "=none"}
	}

	lines := []string{"- " + label + ":"}
	count := 0
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		if count >= limit {
			break
		}
		lines = append(lines, "  "+formatPlannerArtifactContext(artifact))
		count++
	}
	if remaining := countRemainingArtifacts(artifacts); remaining > count {
		lines = append(lines, fmt.Sprintf("  ... %d more artifact(s)", remaining-count))
	}
	return lines
}

func formatPlannerRecentMessages(messages []*models.Message, limit int) []string {
	filtered := make([]*models.Message, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			filtered = append(filtered, message)
		}
	}
	if len(filtered) == 0 || limit <= 0 {
		return []string{"- recent_messages=none"}
	}

	start := len(filtered) - limit
	if start < 0 {
		start = 0
	}
	lines := []string{"- recent_messages:"}
	for _, message := range filtered[start:] {
		lines = append(lines, "  "+formatMessageContext(message))
	}
	return lines
}

func formatPlannerRecentJobs(jobs []*models.Job, limit int) []string {
	filtered := make([]*models.Job, 0, len(jobs))
	for _, job := range jobs {
		if job != nil {
			filtered = append(filtered, job)
		}
	}
	if len(filtered) == 0 || limit <= 0 {
		return []string{"- recent_jobs=none"}
	}

	start := len(filtered) - limit
	if start < 0 {
		start = 0
	}
	lines := []string{"- recent_jobs:"}
	for _, job := range filtered[start:] {
		lines = append(lines, "  "+formatPlannerJobContext(job))
	}
	return lines
}

func formatPlannerWorkingMemoryContext(memory *models.WorkingMemory, artifactLimit, preferenceLimit, stateChangeLimit int) []string {
	if memory == nil {
		return []string{"- working_memory=none"}
	}

	lines := []string{"- working_memory:"}
	if memory.Focus != nil {
		lines = append(lines, "  focus="+formatWorkingMemoryFocus(memory.Focus))
	} else {
		lines = append(lines, "  focus=none")
	}

	if len(memory.RecentArtifacts) == 0 {
		lines = append(lines, "  recent_artifacts=none")
	} else {
		lines = append(lines, "  recent_artifacts:")
		for _, artifact := range takeLastWorkingMemoryArtifacts(memory.RecentArtifacts, artifactLimit) {
			lines = append(lines, "    "+formatWorkingMemoryArtifact(artifact))
		}
		if artifactLimit > 0 && len(memory.RecentArtifacts) > artifactLimit {
			lines = append(lines, fmt.Sprintf("    ... %d more artifact ref(s)", len(memory.RecentArtifacts)-artifactLimit))
		}
	}

	if len(memory.ConfirmedPreferences) == 0 {
		lines = append(lines, "  confirmed_preferences=none")
	} else {
		lines = append(lines, "  confirmed_preferences:")
		for _, preference := range takeLastWorkingMemoryPreferences(memory.ConfirmedPreferences, preferenceLimit) {
			lines = append(lines, "    "+formatWorkingMemoryPreference(preference))
		}
		if preferenceLimit > 0 && len(memory.ConfirmedPreferences) > preferenceLimit {
			lines = append(lines, fmt.Sprintf("    ... %d more preference(s)", len(memory.ConfirmedPreferences)-preferenceLimit))
		}
	}

	if len(memory.SemanticStateChanges) == 0 {
		lines = append(lines, "  semantic_state_changes=none")
	} else {
		lines = append(lines, "  semantic_state_changes:")
		for _, change := range takeLastWorkingMemoryStateChanges(memory.SemanticStateChanges, stateChangeLimit) {
			lines = append(lines, "    "+formatWorkingMemoryStateChange(change))
		}
		if stateChangeLimit > 0 && len(memory.SemanticStateChanges) > stateChangeLimit {
			lines = append(lines, fmt.Sprintf("    ... %d more state change(s)", len(memory.SemanticStateChanges)-stateChangeLimit))
		}
	}

	return lines
}

func formatWorkingMemoryContext(memory *models.WorkingMemory) []string {
	if memory == nil {
		return []string{"- working_memory=none"}
	}

	lines := []string{"- working_memory:"}
	if memory.Focus != nil {
		lines = append(lines, "  focus="+formatWorkingMemoryFocus(memory.Focus))
	} else {
		lines = append(lines, "  focus=none")
	}

	if len(memory.RecentArtifacts) == 0 {
		lines = append(lines, "  recent_artifacts=none")
	} else {
		lines = append(lines, "  recent_artifacts:")
		for _, artifact := range memory.RecentArtifacts {
			lines = append(lines, "    "+formatWorkingMemoryArtifact(artifact))
		}
	}

	if len(memory.ConfirmedPreferences) == 0 {
		lines = append(lines, "  confirmed_preferences=none")
	} else {
		lines = append(lines, "  confirmed_preferences:")
		for _, preference := range memory.ConfirmedPreferences {
			lines = append(lines, "    "+formatWorkingMemoryPreference(preference))
		}
	}

	if len(memory.SemanticStateChanges) == 0 {
		lines = append(lines, "  semantic_state_changes=none")
	} else {
		lines = append(lines, "  semantic_state_changes:")
		for _, change := range memory.SemanticStateChanges {
			lines = append(lines, "    "+formatWorkingMemoryStateChange(change))
		}
	}

	return lines
}

func countRemainingObjects(objects []*models.ObjectMeta, excludedIDs map[string]struct{}) int {
	count := 0
	for _, object := range objects {
		if object == nil {
			continue
		}
		if excludedIDs != nil {
			if _, ok := excludedIDs[object.ID]; ok {
				continue
			}
		}
		count++
	}
	return count
}

func countRemainingArtifacts(artifacts []*models.Artifact) int {
	count := 0
	for _, artifact := range artifacts {
		if artifact != nil {
			count++
		}
	}
	return count
}

func formatPlannerObjectContext(object *models.ObjectMeta, includeSignals bool) string {
	if object == nil {
		return "object=nil"
	}

	parts := []string{
		fmt.Sprintf("id=%s", object.ID),
		fmt.Sprintf("label=%s", object.Label),
		fmt.Sprintf("kind=%s", object.Kind),
		fmt.Sprintf("n_obs=%d", object.NObs),
		fmt.Sprintf("n_vars=%d", object.NVars),
	}
	if object.ParentID != "" {
		parts = append(parts, "parent_id="+object.ParentID)
	}
	if includeSignals {
		if summary := summarizePlannerObjectMetadata(object.Metadata); summary != "" {
			parts = append(parts, "signals="+summary)
		}
	}
	return strings.Join(parts, " | ")
}

func summarizePlannerObjectMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}

	parts := make([]string, 0, 8)
	if assessment := mapValue(metadata["assessment"]); len(assessment) > 0 {
		if analyses := stringSliceValue(assessment["available_analyses"]); len(analyses) > 0 {
			parts = append(parts, "available_analyses="+joinListSummary(analyses, 8))
		}
		assessmentParts := make([]string, 0, 5)
		if state := stringValue(assessment["preprocessing_state"]); state != "" {
			assessmentParts = append(assessmentParts, "preprocessing_state="+state)
		}
		for _, key := range []string{"has_clusters", "has_neighbors", "has_pca", "has_umap"} {
			if value, ok := boolValue(assessment[key]); ok && value {
				assessmentParts = append(assessmentParts, key)
			}
		}
		if len(assessmentParts) > 0 {
			parts = append(parts, strings.Join(assessmentParts, ", "))
		}
	}
	if field := stringValue(mapValue(metadata["cell_type_annotation"])["field"]); field != "" {
		parts = append(parts, "cell_type_field="+field)
	}
	if field := stringValue(mapValue(metadata["cluster_annotation"])["field"]); field != "" {
		parts = append(parts, "cluster_field="+field)
	}
	if obsFields := stringSliceValue(metadata["obs_fields"]); len(obsFields) > 0 {
		parts = append(parts, "obs_fields="+joinListSummary(obsFields, 6))
	}
	if obsmKeys := stringSliceValue(metadata["obsm_keys"]); len(obsmKeys) > 0 {
		parts = append(parts, "obsm_keys="+joinListSummary(obsmKeys, 4))
	}
	if unsKeys := stringSliceValue(metadata["uns_keys"]); len(unsKeys) > 0 {
		parts = append(parts, "uns_keys="+joinListSummary(unsKeys, 4))
	}
	if layerKeys := stringSliceValue(metadata["layer_keys"]); len(layerKeys) > 0 {
		parts = append(parts, "layer_keys="+joinListSummary(layerKeys, 4))
	}
	return strings.Join(parts, " | ")
}

func mapValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func boolValue(value any) (bool, bool) {
	if value == nil {
		return false, false
	}
	flag, ok := value.(bool)
	return flag, ok
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				text = strings.TrimSpace(text)
				if text != "" {
					out = append(out, text)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func joinListSummary(items []string, limit int) string {
	if len(items) == 0 {
		return ""
	}
	if limit <= 0 || len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(items[:limit], ", "), len(items)-limit)
}

func formatObjectContext(object *models.ObjectMeta) string {
	parts := []string{
		fmt.Sprintf("id=%s", object.ID),
		fmt.Sprintf("label=%s", object.Label),
		fmt.Sprintf("kind=%s", object.Kind),
		fmt.Sprintf("n_obs=%d", object.NObs),
		fmt.Sprintf("n_vars=%d", object.NVars),
	}
	if object.ParentID != "" {
		parts = append(parts, "parent_id="+object.ParentID)
	}
	if len(object.Metadata) > 0 {
		parts = append(parts, "metadata="+compactJSON(mustMarshalJSON(object.Metadata)))
	}
	return strings.Join(parts, " | ")
}

func mustMarshalJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func formatMessageContext(message *models.Message) string {
	if message == nil {
		return "message=nil"
	}
	return fmt.Sprintf("role=%s | content=%s", message.Role, truncateText(message.Content, 200))
}

func formatPlannerJobContext(job *models.Job) string {
	if job == nil {
		return "job=nil"
	}

	parts := []string{
		"id=" + job.ID,
		"status=" + string(job.Status),
	}
	if job.CurrentPhase != "" {
		parts = append(parts, "current_phase="+string(job.CurrentPhase))
	}
	if job.Summary != "" {
		parts = append(parts, "summary="+truncateText(job.Summary, 160))
	}

	if len(job.Steps) > 0 {
		stepParts := make([]string, 0, 2)
		start := len(job.Steps) - 2
		if start < 0 {
			start = 0
		}
		for _, step := range job.Steps[start:] {
			stepDetails := []string{"skill=" + step.Skill}
			if step.Status != "" {
				stepDetails = append(stepDetails, "status="+string(step.Status))
			}
			if len(step.Params) > 0 {
				stepDetails = append(stepDetails, "params="+compactJSON(mustMarshalJSON(step.Params)))
			}
			if len(step.Metadata) > 0 {
				stepDetails = append(stepDetails, "metadata="+compactJSON(mustMarshalJSON(step.Metadata)))
			}
			if step.OutputObjectID != "" {
				stepDetails = append(stepDetails, "output="+step.OutputObjectID)
			}
			stepParts = append(stepParts, "{"+strings.Join(stepDetails, " | ")+"}")
		}
		if len(job.Steps) > 2 {
			stepParts = append(stepParts, fmt.Sprintf("+%d more step(s)", len(job.Steps)-2))
		}
		parts = append(parts, "steps="+strings.Join(stepParts, "; "))
	}

	return strings.Join(parts, " | ")
}

func formatJobContext(job *models.Job) string {
	if job == nil {
		return "job=nil"
	}
	stepParts := make([]string, 0, len(job.Steps))
	for _, step := range job.Steps {
		stepDetails := []string{
			"skill=" + step.Skill,
		}
		if step.Status != "" {
			stepDetails = append(stepDetails, "status="+string(step.Status))
		}
		if step.TargetObjectID != "" {
			stepDetails = append(stepDetails, "target="+step.TargetObjectID)
		}
		if len(step.Params) > 0 {
			stepDetails = append(stepDetails, "params="+compactJSON(mustMarshalJSON(step.Params)))
		}
		if len(step.Facts) > 0 {
			stepDetails = append(stepDetails, "facts="+compactJSON(mustMarshalJSON(step.Facts)))
		}
		if len(step.Metadata) > 0 {
			stepDetails = append(stepDetails, "metadata="+compactJSON(mustMarshalJSON(step.Metadata)))
		}
		if step.OutputObjectID != "" {
			stepDetails = append(stepDetails, "output="+step.OutputObjectID)
		}
		stepParts = append(stepParts, "{"+strings.Join(stepDetails, " | ")+"}")
	}
	phaseParts := make([]string, 0, len(job.Phases))
	for _, phase := range job.Phases {
		phaseParts = append(phaseParts, fmt.Sprintf(
			"{kind=%s status=%s summary=%s}",
			phase.Kind,
			phase.Status,
			truncateText(phase.Summary, 120),
		))
	}
	return fmt.Sprintf(
		"id=%s | status=%s | current_phase=%s | summary=%s | phases=%s | steps=%s",
		job.ID,
		job.Status,
		job.CurrentPhase,
		truncateText(job.Summary, 200),
		strings.Join(phaseParts, "; "),
		strings.Join(stepParts, "; "),
	)
}

func formatPlannerArtifactContext(artifact *models.Artifact) string {
	if artifact == nil {
		return "artifact=nil"
	}

	parts := []string{
		"id=" + artifact.ID,
		"kind=" + string(artifact.Kind),
	}
	if artifact.ObjectID != "" {
		parts = append(parts, "object_id="+artifact.ObjectID)
	}
	if artifact.JobID != "" {
		parts = append(parts, "job_id="+artifact.JobID)
	}
	if artifact.Title != "" {
		parts = append(parts, "title="+truncateText(artifact.Title, 120))
	}
	if artifact.Summary != "" {
		parts = append(parts, "summary="+truncateText(artifact.Summary, 160))
	}
	if artifact.ContentType != "" {
		parts = append(parts, "content_type="+artifact.ContentType)
	}
	return strings.Join(parts, " | ")
}

func formatArtifactContext(artifact *models.Artifact) string {
	if artifact == nil {
		return "artifact=nil"
	}
	return fmt.Sprintf(
		"kind=%s | title=%s | summary=%s",
		artifact.Kind,
		truncateText(artifact.Title, 120),
		truncateText(artifact.Summary, 160),
	)
}

func formatWorkingMemoryFocus(focus *models.WorkingMemoryFocus) string {
	if focus == nil {
		return "focus=nil"
	}

	parts := make([]string, 0, 6)
	if focus.FocusObjectID != "" {
		parts = append(parts, "focus_object_id="+focus.FocusObjectID)
	}
	if focus.FocusObjectLabel != "" {
		parts = append(parts, "focus_object_label="+focus.FocusObjectLabel)
	}
	if focus.LastOutputObjectID != "" {
		parts = append(parts, "last_output_object_id="+focus.LastOutputObjectID)
	}
	if focus.LastOutputObjectLabel != "" {
		parts = append(parts, "last_output_object_label="+focus.LastOutputObjectLabel)
	}
	if focus.LastArtifactID != "" {
		parts = append(parts, "last_artifact_id="+focus.LastArtifactID)
	}
	if focus.LastArtifactTitle != "" {
		parts = append(parts, "last_artifact_title="+truncateText(focus.LastArtifactTitle, 120))
	}
	if len(parts) == 0 {
		return "focus=empty"
	}
	return strings.Join(parts, " | ")
}

func formatWorkingMemoryArtifact(artifact models.WorkingMemoryArtifactRef) string {
	return fmt.Sprintf(
		"id=%s | kind=%s | title=%s | summary=%s | object_id=%s | job_id=%s",
		artifact.ID,
		artifact.Kind,
		truncateText(artifact.Title, 120),
		truncateText(artifact.Summary, 160),
		artifact.ObjectID,
		artifact.JobID,
	)
}

func formatWorkingMemoryPreference(preference models.WorkingMemoryPreference) string {
	return fmt.Sprintf(
		"%s.%s=%s | source_job=%s | source_step=%s",
		preference.Skill,
		preference.Param,
		compactJSON(mustMarshalJSON(preference.Value)),
		preference.SourceJobID,
		preference.SourceStepID,
	)
}

func formatWorkingMemoryStateChange(change models.WorkingMemoryStateChange) string {
	parts := []string{"kind=" + change.Kind}
	if change.Skill != "" {
		parts = append(parts, "skill="+change.Skill)
	}
	if change.ObjectID != "" {
		parts = append(parts, "object_id="+change.ObjectID)
	}
	if change.ObjectLabel != "" {
		parts = append(parts, "object_label="+change.ObjectLabel)
	}
	if change.ArtifactID != "" {
		parts = append(parts, "artifact_id="+change.ArtifactID)
	}
	if change.ArtifactTitle != "" {
		parts = append(parts, "artifact_title="+truncateText(change.ArtifactTitle, 120))
	}
	if change.JobID != "" {
		parts = append(parts, "job_id="+change.JobID)
	}
	if change.StepID != "" {
		parts = append(parts, "step_id="+change.StepID)
	}
	if change.Summary != "" {
		parts = append(parts, "summary="+truncateText(change.Summary, 160))
	}
	return strings.Join(parts, " | ")
}

func takeLastWorkingMemoryArtifacts(values []models.WorkingMemoryArtifactRef, limit int) []models.WorkingMemoryArtifactRef {
	if len(values) == 0 || limit <= 0 {
		return nil
	}
	start := len(values) - limit
	if start < 0 {
		start = 0
	}
	out := make([]models.WorkingMemoryArtifactRef, len(values[start:]))
	copy(out, values[start:])
	return out
}

func takeLastWorkingMemoryPreferences(values []models.WorkingMemoryPreference, limit int) []models.WorkingMemoryPreference {
	if len(values) == 0 || limit <= 0 {
		return nil
	}
	start := len(values) - limit
	if start < 0 {
		start = 0
	}
	out := make([]models.WorkingMemoryPreference, len(values[start:]))
	copy(out, values[start:])
	return out
}

func takeLastWorkingMemoryStateChanges(values []models.WorkingMemoryStateChange, limit int) []models.WorkingMemoryStateChange {
	if len(values) == 0 || limit <= 0 {
		return nil
	}
	start := len(values) - limit
	if start < 0 {
		start = 0
	}
	out := make([]models.WorkingMemoryStateChange, len(values[start:]))
	copy(out, values[start:])
	return out
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}
