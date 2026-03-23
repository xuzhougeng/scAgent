package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"scagent/internal/models"
	"scagent/internal/skill"
)

func TestLLMPlannerBuildsRequestAndParsesPlan(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	var capturedBody []byte
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			var err error
			capturedBody, err = io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			if request.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("missing authorization header")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(bytes.NewBufferString(`{
					"output": [
						{
							"type": "message",
							"content": [
								{
									"type": "output_text",
									"text": "{\"steps\":[{\"skill\":\"inspect_dataset\",\"target_object_id\":\"$active\",\"params\":{},\"memory_refs\":[\"focus.active_object_id\"]}]}"
								}
							]
						}
					]
				}`)),
			}, nil
		}),
	}

	planner, err := NewLLMPlanner(LLMPlannerConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
		Skills:          registry,
	}, httpClient)
	if err != nil {
		t.Fatalf("create LLM planner: %v", err)
	}

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "Inspect the dataset",
		Session: &models.Session{ID: "sess_123"},
		ActiveObject: &models.ObjectMeta{
			ID:    "obj_1",
			Label: "pbmc3k",
			Kind:  models.ObjectRawDataset,
			NObs:  2638,
			NVars: 1838,
			Metadata: map[string]any{
				"obs_fields": []string{"cell_type", "sample", "leiden"},
				"obsm_keys":  []string{"X_umap"},
			},
		},
		Objects: []*models.ObjectMeta{
			{
				ID:    "obj_1",
				Label: "pbmc3k",
				Kind:  models.ObjectRawDataset,
				NObs:  2638,
				NVars: 1838,
				Metadata: map[string]any{
					"obs_fields": []string{"cell_type", "sample", "leiden"},
				},
			},
		},
		RecentJobs: []*models.Job{
			{
				ID:     "job_prev",
				Status: models.JobSucceeded,
				Steps: []models.JobStep{
					{
						Skill: "plot_umap",
						Params: map[string]any{
							"color_by":   "louvain",
							"legend_loc": "on data",
						},
						Metadata: map[string]any{
							"legend_loc": "on data",
						},
					},
				},
			},
		},
		WorkingMemory: &models.WorkingMemory{
			Focus: &models.WorkingMemoryFocus{
				ActiveObjectID:        "obj_1",
				ActiveObjectLabel:     "pbmc3k",
				LastArtifactID:        "art_1",
				LastArtifactTitle:     "pbmc3k UMAP",
				LastOutputObjectID:    "obj_1",
				LastOutputObjectLabel: "pbmc3k",
			},
			RecentArtifacts: []models.WorkingMemoryArtifactRef{
				{
					ID:       "art_1",
					Kind:     models.ArtifactPlot,
					ObjectID: "obj_1",
					JobID:    "job_prev",
					Title:    "pbmc3k UMAP",
					Summary:  "colored by louvain",
				},
			},
			ConfirmedPreferences: []models.WorkingMemoryPreference{
				{
					Skill: "plot_umap",
					Param: "legend_loc",
					Value: "on data",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("run planner: %v", err)
	}

	if len(plan.Steps) != 1 || plan.Steps[0].Skill != "inspect_dataset" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Steps[0].MemoryRefs) != 1 || plan.Steps[0].MemoryRefs[0] != "focus.active_object_id" {
		t.Fatalf("planner response should preserve memory refs, got %+v", plan.Steps[0].MemoryRefs)
	}

	if !bytes.Contains(capturedBody, []byte(`"model":"gpt-5.4"`)) {
		t.Fatalf("planner request missing model: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`"effort":"low"`)) {
		t.Fatalf("planner request missing reasoning effort: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`"name":"scagent_plan"`)) {
		t.Fatalf("planner request missing schema name: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`pbmc3k`)) {
		t.Fatalf("planner request missing object context: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`obs_fields`)) {
		t.Fatalf("planner request missing compact object signals: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`legend_loc`)) || !bytes.Contains(capturedBody, []byte(`on data`)) {
		t.Fatalf("planner request missing recent step params/metadata context: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`working_memory`)) || !bytes.Contains(capturedBody, []byte(`confirmed_preferences`)) {
		t.Fatalf("planner request missing working memory context: %s", string(capturedBody))
	}
	if bytes.Contains(capturedBody, []byte(`input_image`)) {
		t.Fatalf("planner request should not inline image inputs: %s", string(capturedBody))
	}

	var requestPayload map[string]any
	if err := json.Unmarshal(capturedBody, &requestPayload); err != nil {
		t.Fatalf("decode planner request: %v", err)
	}

	textPayload, ok := requestPayload["text"].(map[string]any)
	if !ok {
		t.Fatalf("planner request missing text payload")
	}
	formatPayload, ok := textPayload["format"].(map[string]any)
	if !ok {
		t.Fatalf("planner request missing format payload")
	}
	schemaPayload, ok := formatPayload["schema"].(map[string]any)
	if !ok {
		t.Fatalf("planner request missing schema payload")
	}
	propertiesPayload, ok := schemaPayload["properties"].(map[string]any)
	if !ok {
		t.Fatalf("planner schema missing properties")
	}
	stepsPayload, ok := propertiesPayload["steps"].(map[string]any)
	if !ok {
		t.Fatalf("planner schema missing steps")
	}
	if minItems, ok := stepsPayload["minItems"].(float64); !ok || minItems != 1 {
		t.Fatalf("planner schema should require at least one step: %+v", stepsPayload)
	}
	itemsPayload, ok := stepsPayload["items"].(map[string]any)
	if !ok {
		t.Fatalf("planner schema missing items")
	}
	requiredFields, ok := itemsPayload["required"].([]any)
	if !ok {
		t.Fatalf("planner step schema missing required fields: %+v", itemsPayload)
	}
	if len(requiredFields) != 4 {
		t.Fatalf("plan step schema required must enumerate every property: %+v", itemsPayload)
	}
	requiredFieldNames := make(map[string]struct{}, len(requiredFields))
	for _, requiredField := range requiredFields {
		name, ok := requiredField.(string)
		if !ok {
			t.Fatalf("plan step schema required contains non-string value: %+v", requiredFields)
		}
		requiredFieldNames[name] = struct{}{}
	}
	for _, field := range []string{"skill", "target_object_id", "params", "memory_refs"} {
		if _, ok := requiredFieldNames[field]; !ok {
			t.Fatalf("plan step schema required missing %q: %+v", field, requiredFields)
		}
	}

	stepProperties, ok := itemsPayload["properties"].(map[string]any)
	if !ok {
		t.Fatalf("planner step schema missing properties: %+v", itemsPayload)
	}

	skillPayload, ok := stepProperties["skill"].(map[string]any)
	if !ok {
		t.Fatalf("planner step schema missing skill property")
	}
	enumValues, ok := skillPayload["enum"].([]any)
	if !ok || len(enumValues) == 0 {
		t.Fatalf("planner skill enum missing: %+v", skillPayload)
	}
	for _, requiredSkill := range []any{"inspect_dataset", "plot_umap", "plot_gene_umap", "run_python_analysis"} {
		found := false
		for _, candidate := range enumValues {
			if candidate == requiredSkill {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("planner skill enum missing %v: %+v", requiredSkill, enumValues)
		}
	}

	paramsPayload, ok := stepProperties["params"].(map[string]any)
	if !ok {
		t.Fatalf("planner step schema missing params property")
	}
	if paramsPayload["type"] != "object" {
		t.Fatalf("planner params schema should be generic object: %+v", paramsPayload)
	}
	if paramsPayload["additionalProperties"] != false {
		t.Fatalf("planner params schema should be strict superset object: %+v", paramsPayload)
	}
	paramsProperties, ok := paramsPayload["properties"].(map[string]any)
	if !ok || len(paramsProperties) == 0 {
		t.Fatalf("planner params schema should include superset properties: %+v", paramsPayload)
	}
	for _, field := range []string{"groupby", "genes", "legend_loc", "include_fields", "code"} {
		if _, ok := paramsProperties[field]; !ok {
			t.Fatalf("planner params schema missing field %q: %+v", field, paramsProperties)
		}
	}

	memoryRefsPayload, ok := stepProperties["memory_refs"].(map[string]any)
	if !ok {
		t.Fatalf("plan step schema missing memory_refs: %+v", stepProperties)
	}
	memoryRefsAnyOf, ok := memoryRefsPayload["anyOf"].([]any)
	if !ok || len(memoryRefsAnyOf) != 2 {
		t.Fatalf("memory_refs should allow null: %+v", memoryRefsPayload)
	}

	targetObjectPayload, ok := stepProperties["target_object_id"].(map[string]any)
	if !ok {
		t.Fatalf("plan step schema missing target_object_id")
	}
	targetObjectAnyOf, ok := targetObjectPayload["anyOf"].([]any)
	if !ok || len(targetObjectAnyOf) != 2 {
		t.Fatalf("target_object_id should allow null: %+v", targetObjectPayload)
	}
}

func TestLLMPlannerBuildRequestUsesArtifactSummariesInsteadOfImageBytes(t *testing.T) {
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
	}, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("http client should not be used in buildRequest test")
		return nil, nil
	})})
	if err != nil {
		t.Fatalf("create LLM planner: %v", err)
	}

	imagePath := filepath.Join(t.TempDir(), "recent-plot.png")
	if err := os.WriteFile(imagePath, tinyPNG(), 0o644); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	payload, err := json.Marshal(planner.buildRequest(PlanningRequest{
		Message: "导出当前对象为 h5ad",
		Session: &models.Session{
			ID:             "sess_123",
			ActiveObjectID: "obj_1",
		},
		ActiveObject: &models.ObjectMeta{
			ID:    "obj_1",
			Label: "pbmc3k",
			Kind:  models.ObjectFilteredDataset,
			NObs:  2638,
			NVars: 1838,
			Metadata: map[string]any{
				"assessment": map[string]any{
					"available_analyses": []any{"inspect_dataset", "plot_umap", "export_h5ad"},
					"has_umap":           true,
				},
			},
		},
		RecentArtifacts: []*models.Artifact{
			{
				ID:          "artifact_plot_1",
				Kind:        models.ArtifactPlot,
				ObjectID:    "obj_1",
				JobID:       "job_1",
				Title:       "pbmc3k 的 UMAP 图",
				Summary:     "按 cell_type 着色的真实 UMAP 图。",
				Path:        imagePath,
				ContentType: "image/png",
			},
		},
	}))
	if err != nil {
		t.Fatalf("marshal planner request: %v", err)
	}

	if bytes.Contains(payload, []byte(`data:image/png;base64,`)) {
		t.Fatalf("planner request should not contain inlined image bytes: %s", string(payload))
	}
	if bytes.Contains(payload, []byte(`"input_image"`)) {
		t.Fatalf("planner request should not contain image input items: %s", string(payload))
	}
	if !bytes.Contains(payload, []byte(`pbmc3k 的 UMAP 图`)) {
		t.Fatalf("planner request should retain artifact summaries: %s", string(payload))
	}
}

func TestLLMPlannerHealthChecksResponsesEndpoint(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	var requestCount int
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			requestCount++
			if request.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", request.Method)
			}
			if request.URL.String() != "https://example.test/v1/responses" {
				t.Fatalf("unexpected health check url: %s", request.URL.String())
			}
			if request.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("missing authorization header")
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			if string(body) != "{}" {
				t.Fatalf("unexpected health check body: %s", string(body))
			}
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"missing model"}}`)),
			}, nil
		}),
	}

	planner, err := NewLLMPlanner(LLMPlannerConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
		Skills:          registry,
	}, httpClient)
	if err != nil {
		t.Fatalf("create LLM planner: %v", err)
	}

	if err := planner.Health(context.Background()); err != nil {
		t.Fatalf("health check should accept 400 validation response: %v", err)
	}
	if err := planner.Health(context.Background()); err != nil {
		t.Fatalf("cached health check should still succeed: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("expected cached health result, got %d requests", requestCount)
	}
}

func TestLLMPlannerRetriesTimeoutOnce(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	var requestCount int
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			requestCount++
			if requestCount == 1 {
				return nil, context.DeadlineExceeded
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(bytes.NewBufferString(`{
					"output": [
						{
							"type": "message",
							"content": [
								{
									"type": "output_text",
									"text": "{\"steps\":[{\"id\":\"step_1\",\"skill\":\"normalize_total\",\"target_object_id\":\"$active\",\"params\":{},\"memory_refs\":null}]}"
								}
							]
						}
					]
				}`)),
			}, nil
		}),
	}

	planner, err := NewLLMPlanner(LLMPlannerConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
		Skills:          registry,
	}, httpClient)
	if err != nil {
		t.Fatalf("create LLM planner: %v", err)
	}

	plan, err := planner.Plan(context.Background(), PlanningRequest{Message: "预处理"})
	if err != nil {
		t.Fatalf("expected timeout retry to recover, got %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("expected one retry after timeout, got %d requests", requestCount)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Skill != "normalize_total" {
		t.Fatalf("unexpected plan after retry: %+v", plan)
	}
}

func skillsRegistryPath() string {
	_, currentFile, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "skills", "registry.json"))
}

type roundTripperFunc func(request *http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
