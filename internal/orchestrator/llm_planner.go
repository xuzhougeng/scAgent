package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
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
	if mode == "" || mode == "fake" {
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

func (p *LLMPlanner) Plan(ctx context.Context, requestPayload PlanningRequest) (models.Plan, error) {
	payload, err := json.Marshal(p.buildRequest(requestPayload))
	if err != nil {
		return models.Plan{}, fmt.Errorf("marshal planner request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return models.Plan{}, fmt.Errorf("create planner request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := p.httpClient.Do(request)
	if err != nil {
		return models.Plan{}, fmt.Errorf("planner request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return models.Plan{}, fmt.Errorf("read planner response: %w", err)
	}

	if response.StatusCode >= http.StatusBadRequest {
		return models.Plan{}, fmt.Errorf("planner returned %s: %s", response.Status, compactJSON(string(body)))
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

func (p *LLMPlanner) DebugPreview(_ context.Context, requestPayload PlanningRequest) (*PlannerDebugPreview, error) {
	return &PlannerDebugPreview{
		PlannerMode:           p.Mode(),
		PlanningRequest:       requestPayload,
		DeveloperInstructions: p.instructions(requestPayload),
		RequestBody:           p.buildRequest(requestPayload),
		Note:                  "Authorization headers are omitted from this preview.",
	}, nil
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
				"content": requestPayload.Message,
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
		"Convert the user request into a JSON execution plan.",
		"Return only valid JSON matching the supplied schema.",
		"Use only listed skills.",
		"Use \"$active\" for the current object and \"$prev\" for chaining.",
		"Do not invent parameters outside the skill schemas.",
		"Prefer the fewest valid steps needed to satisfy the request.",
		"Compose multiple skills when the user asks for a workflow, not just a single action.",
		"If the user asks for routine preprocessing, prefer a chain such as normalize_total -> log1p_transform -> select_hvg -> run_pca -> compute_neighbors -> run_umap when those skills are available and appropriate.",
		"If the request is about plot presentation details such as legends, colors, or labels, prefer the closest visualization skill such as plot_umap instead of returning an empty plan.",
		"Treat recent_messages, recent_jobs, and recent_artifacts as conversation context for follow-up requests such as '把这个图改一下' or '把图例加上'.",
		"Never return an empty steps array.",
		"Available skills:",
	}

	for _, definition := range p.skills.ListExecutable() {
		lines = append(lines, formatSkillInstruction(definition))
	}

	lines = append(lines, "Current session context:")
	lines = append(lines, formatPlanningContext(requestPayload)...)

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

	return fmt.Sprintf(
		"- %s: %s | category=%s | support=%s | inputs=%s | required=%s",
		definition.Name,
		definition.Description,
		definition.Category,
		definition.SupportLevel,
		strings.Join(inputNames, ","),
		strings.Join(required, ","),
	)
}

func planSchema(definitions []skill.Definition) map[string]any {
	stepSchemas := make([]any, 0, len(definitions))
	for _, definition := range definitions {
		stepSchemas = append(stepSchemas, planStepSchema(definition))
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"steps"},
		"properties": map[string]any{
			"steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"anyOf": stepSchemas,
				},
			},
		},
	}
}

func planStepSchema(definition skill.Definition) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"skill", "target_object_id", "params"},
		"properties": map[string]any{
			"skill": map[string]any{
				"type": "string",
				"enum": []string{definition.Name},
			},
			"target_object_id": nullableSchema(map[string]any{"type": "string"}),
			"params":           paramsSchema(definition),
		},
	}
}

func paramsSchema(definition skill.Definition) map[string]any {
	properties := make(map[string]any, len(definition.Input))
	inputNames := make([]string, 0, len(definition.Input))

	for name := range definition.Input {
		inputNames = append(inputNames, name)
	}
	sort.Strings(inputNames)

	for _, name := range inputNames {
		field := definition.Input[name]
		if name == "target_object_id" {
			continue
		}

		properties[name] = fieldSchema(field)
	}

	required := make([]string, 0, len(properties))
	for _, name := range inputNames {
		if name == "target_object_id" {
			continue
		}
		required = append(required, name)
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
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

func formatPlanningContext(request PlanningRequest) []string {
	lines := make([]string, 0, 20)
	if request.Session != nil {
		lines = append(lines, fmt.Sprintf("- session_id=%s", request.Session.ID))
	}

	if request.ActiveObject != nil {
		lines = append(lines, "- active_object="+formatObjectContext(request.ActiveObject))
	} else {
		lines = append(lines, "- active_object=none")
	}

	if len(request.Objects) == 0 {
		lines = append(lines, "- objects=none")
	} else {
		lines = append(lines, "- objects:")
		for _, object := range request.Objects {
			lines = append(lines, "  "+formatObjectContext(object))
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
	return lines
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

func formatJobContext(job *models.Job) string {
	if job == nil {
		return "job=nil"
	}
	stepSkills := make([]string, 0, len(job.Steps))
	for _, step := range job.Steps {
		stepSkills = append(stepSkills, step.Skill)
	}
	return fmt.Sprintf(
		"id=%s | status=%s | summary=%s | steps=%s",
		job.ID,
		job.Status,
		truncateText(job.Summary, 200),
		strings.Join(stepSkills, ","),
	)
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
