package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
		t.Fatalf("planner request missing metadata context: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`legend_loc`)) || !bytes.Contains(capturedBody, []byte(`on data`)) {
		t.Fatalf("planner request missing recent step params/metadata context: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`working_memory`)) || !bytes.Contains(capturedBody, []byte(`confirmed_preferences`)) {
		t.Fatalf("planner request missing working memory context: %s", string(capturedBody))
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
	itemsPayload, ok := stepsPayload["items"].(map[string]any)
	if !ok {
		t.Fatalf("planner schema missing items")
	}
	stepVariants, ok := itemsPayload["anyOf"].([]any)
	if !ok || len(stepVariants) == 0 {
		t.Fatalf("planner schema missing skill variants")
	}

	foundStrictParams := false
	foundPlotUMAPParams := false
	foundPlotGeneUMAPSkill := false
	foundCustomCodeSkill := false
	for _, variant := range stepVariants {
		stepPayload, ok := variant.(map[string]any)
		if !ok {
			continue
		}
		stepProperties, ok := stepPayload["properties"].(map[string]any)
		if !ok {
			continue
		}
		requiredFields, ok := stepPayload["required"].([]any)
		if !ok {
			t.Fatalf("plan step schema missing required fields: %+v", stepPayload)
		}
		if len(requiredFields) != 4 {
			t.Fatalf("plan step schema required must enumerate every property: %+v", stepPayload)
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
		skillPayload, ok := stepProperties["skill"].(map[string]any)
		if !ok {
			continue
		}
		enumValues, ok := skillPayload["enum"].([]any)
		if !ok || len(enumValues) != 1 {
			continue
		}
		if enumValues[0] == "run_python_analysis" {
			foundCustomCodeSkill = true
		}
		if enumValues[0] == "plot_gene_umap" {
			foundPlotGeneUMAPSkill = true
		}
		if enumValues[0] == "plot_umap" {
			paramsPayload, ok := stepProperties["params"].(map[string]any)
			if !ok {
				t.Fatalf("plot_umap params schema missing")
			}
			paramsProperties, ok := paramsPayload["properties"].(map[string]any)
			if !ok {
				t.Fatalf("plot_umap params schema missing properties: %+v", paramsPayload)
			}
			legendLocPayload, ok := paramsProperties["legend_loc"].(map[string]any)
			if !ok {
				t.Fatalf("plot_umap legend_loc schema missing: %+v", paramsPayload)
			}
			legendLocAnyOf, ok := legendLocPayload["anyOf"].([]any)
			if !ok || len(legendLocAnyOf) != 2 {
				t.Fatalf("plot_umap legend_loc should allow null: %+v", legendLocPayload)
			}
			stringSchema, ok := legendLocAnyOf[0].(map[string]any)
			if !ok {
				t.Fatalf("plot_umap legend_loc first anyOf entry invalid: %+v", legendLocPayload)
			}
			enumPayload, ok := stringSchema["enum"].([]any)
			if !ok || len(enumPayload) == 0 {
				t.Fatalf("plot_umap legend_loc enum missing: %+v", legendLocPayload)
			}
			foundOnData := false
			for _, candidate := range enumPayload {
				if candidate == "on data" {
					foundOnData = true
					break
				}
			}
			if !foundOnData {
				t.Fatalf("plot_umap legend_loc enum missing 'on data': %+v", legendLocPayload)
			}
			foundPlotUMAPParams = true
		}
		memoryRefsPayload, ok := stepProperties["memory_refs"].(map[string]any)
		if !ok {
			t.Fatalf("plan step schema missing memory_refs: %+v", stepProperties)
		}
		memoryRefsAnyOf, ok := memoryRefsPayload["anyOf"].([]any)
		if !ok || len(memoryRefsAnyOf) != 2 {
			t.Fatalf("memory_refs should allow null: %+v", memoryRefsPayload)
		}
		if enumValues[0] != "inspect_dataset" {
			continue
		}
		paramsPayload, ok := stepProperties["params"].(map[string]any)
		if !ok {
			t.Fatalf("inspect_dataset params schema missing")
		}
		if paramsPayload["additionalProperties"] != false {
			t.Fatalf("inspect_dataset params schema must be strict: %+v", paramsPayload)
		}
		requiredFields, ok = paramsPayload["required"].([]any)
		if !ok {
			t.Fatalf("inspect_dataset params schema missing required array: %+v", paramsPayload)
		}
		if len(requiredFields) != 1 || requiredFields[0] != "include_fields" {
			t.Fatalf("inspect_dataset params required must enumerate all properties: %+v", paramsPayload)
		}
		paramsProperties, ok := paramsPayload["properties"].(map[string]any)
		if !ok {
			t.Fatalf("inspect_dataset params schema missing properties: %+v", paramsPayload)
		}
		includeFieldsPayload, ok := paramsProperties["include_fields"].(map[string]any)
		if !ok {
			t.Fatalf("inspect_dataset include_fields schema missing: %+v", paramsPayload)
		}
		includeFieldsAnyOf, ok := includeFieldsPayload["anyOf"].([]any)
		if !ok || len(includeFieldsAnyOf) != 2 {
			t.Fatalf("inspect_dataset include_fields should allow null: %+v", includeFieldsPayload)
		}
		targetObjectPayload, ok := stepProperties["target_object_id"].(map[string]any)
		if !ok {
			t.Fatalf("inspect_dataset target_object_id schema missing")
		}
		targetObjectAnyOf, ok := targetObjectPayload["anyOf"].([]any)
		if !ok || len(targetObjectAnyOf) != 2 {
			t.Fatalf("inspect_dataset target_object_id should allow null: %+v", targetObjectPayload)
		}
		foundStrictParams = true
	}

	if !foundStrictParams {
		t.Fatalf("did not find strict inspect_dataset params schema in planner request")
	}
	if !foundPlotUMAPParams {
		t.Fatalf("did not find plot_umap params schema in planner request")
	}
	if !foundPlotGeneUMAPSkill {
		t.Fatalf("did not find plot_gene_umap skill in planner request schema")
	}
	if !foundCustomCodeSkill {
		t.Fatalf("did not find run_python_analysis skill in planner request schema")
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

func skillsRegistryPath() string {
	_, currentFile, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "skills", "registry.json"))
}

type roundTripperFunc func(request *http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
