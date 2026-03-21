package orchestrator

import (
	"bytes"
	"context"
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
}

func skillsRegistryPath() string {
	_, currentFile, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "skills", "registry.json"))
}

type roundTripperFunc func(request *http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
