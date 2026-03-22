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
									"text": "{\"steps\":[{\"skill\":\"inspect_dataset\",\"target_object_id\":\"$active\",\"params\":{}}]}"
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
	})
	if err != nil {
		t.Fatalf("run planner: %v", err)
	}

	if len(plan.Steps) != 1 || plan.Steps[0].Skill != "inspect_dataset" {
		t.Fatalf("unexpected plan: %+v", plan)
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
			continue
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
		requiredFields, ok := paramsPayload["required"].([]any)
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

func skillsRegistryPath() string {
	_, currentFile, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "skills", "registry.json"))
}

type roundTripperFunc func(request *http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
