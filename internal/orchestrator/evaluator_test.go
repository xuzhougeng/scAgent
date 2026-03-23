package orchestrator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"scagent/internal/models"
)

func TestLLMEvaluatorBuildRequestIncludesOnlyExplicitImageInputs(t *testing.T) {
	var capturedBody []byte
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			var err error
			capturedBody, err = io.ReadAll(request.Body)
			if err != nil {
				return nil, err
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
									"text": "{\"completed\":false,\"reason\":\"still pending\"}"
								}
							]
						}
					]
				}`)),
			}, nil
		}),
	}

	evaluator, err := NewLLMEvaluator(LLMEvaluatorConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
	}, httpClient)
	if err != nil {
		t.Fatalf("create evaluator: %v", err)
	}

	inputImagePath := filepath.Join(t.TempDir(), "input.png")
	if err := os.WriteFile(inputImagePath, tinyPNG(), 0o644); err != nil {
		t.Fatalf("write temp input image: %v", err)
	}
	recentImagePath := filepath.Join(t.TempDir(), "recent.png")
	if err := os.WriteFile(recentImagePath, tinyPNG(), 0o644); err != nil {
		t.Fatalf("write temp recent image: %v", err)
	}

	_, err = evaluator.Evaluate(context.Background(), EvaluationRequest{
		Message: "这个请求完成了吗",
		Session: &models.Session{ID: "sess_1", ActiveObjectID: "obj_1"},
		ActiveObject: &models.ObjectMeta{
			ID:    "obj_1",
			Label: "pbmc3k",
			Kind:  models.ObjectFilteredDataset,
			NObs:  2638,
			NVars: 1838,
		},
		InputArtifacts: []*models.Artifact{{
			ID:          "artifact_input",
			Path:        inputImagePath,
			ContentType: "image/png",
			Title:       "用户上传图片",
		}},
		RecentArtifacts: []*models.Artifact{{
			ID:          "artifact_recent",
			Path:        recentImagePath,
			ContentType: "image/png",
			Title:       "最近生成图片",
			Summary:     "最近生成的 UMAP 图。",
		}},
		CurrentJob: &models.Job{
			ID:     "job_1",
			Status: models.JobSucceeded,
			Steps: []models.JobStep{{
				ID:      "step_1",
				Skill:   "plot_umap",
				Status:  models.JobSucceeded,
				Summary: "已生成 UMAP 图。",
			}},
		},
	})
	if err != nil {
		t.Fatalf("evaluate request: %v", err)
	}

	if count := bytes.Count(capturedBody, []byte(`"input_image"`)); count != 1 {
		t.Fatalf("expected exactly one explicit image input, got %d: %s", count, string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`最近生成图片`)) {
		t.Fatalf("expected recent artifact summary in evaluator context: %s", string(capturedBody))
	}
}
