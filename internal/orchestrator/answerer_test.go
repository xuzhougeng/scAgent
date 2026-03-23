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

func TestLLMAnswererReturnsDirectAnswerFromSemanticDecision(t *testing.T) {
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
									"text": "{\"decision\":\"direct_answer\",\"answer\":\"当前对象 pbmc3k 有 2638 个细胞。\",\"confidence\":\"high\",\"evidence\":[\"focus_object.n_obs\"]}"
								}
							]
						}
					]
				}`)),
			}, nil
		}),
	}

	answerer, err := NewLLMAnswerer(LLMAnswererConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
	}, httpClient)
	if err != nil {
		t.Fatalf("create LLM answerer: %v", err)
	}

	answer, ok, err := answerer.BuildDirectAnswer(context.Background(), PlanningRequest{
		Message: "Cell ENENN 有多少个呢",
		FocusObject: &models.ObjectMeta{
			ID:    "obj_1",
			Label: "pbmc3k",
			Kind:  models.ObjectRawDataset,
			NObs:  2638,
			NVars: 1838,
			Metadata: map[string]any{
				"assessment": map[string]any{"has_umap": true},
			},
		},
	})
	if err != nil {
		t.Fatalf("build direct answer: %v", err)
	}
	if !ok {
		t.Fatalf("expected semantic direct answer to be accepted")
	}
	if answer != "当前对象 pbmc3k 有 2638 个细胞。" {
		t.Fatalf("unexpected semantic answer: %q", answer)
	}
	if !bytes.Contains(capturedBody, []byte(`"name":"scagent_direct_answer"`)) {
		t.Fatalf("answerer request missing direct-answer schema: %s", string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`Do not rely on literal keyword matching`)) {
		t.Fatalf("answerer instructions missing semantic guidance: %s", string(capturedBody))
	}
}

func TestLLMAnswererDeclinesWhenExecutionIsNeeded(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
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
									"text": "{\"decision\":\"needs_execution\",\"answer\":\"\",\"confidence\":\"high\",\"evidence\":[]}"
								}
							]
						}
					]
				}`)),
			}, nil
		}),
	}

	answerer, err := NewLLMAnswerer(LLMAnswererConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
	}, httpClient)
	if err != nil {
		t.Fatalf("create LLM answerer: %v", err)
	}

	answer, ok, err := answerer.BuildDirectAnswer(context.Background(), PlanningRequest{
		Message: "B 细胞有多少个",
	})
	if err != nil {
		t.Fatalf("build direct answer: %v", err)
	}
	if ok || answer != "" {
		t.Fatalf("expected answerer to decline direct answer, got ok=%v answer=%q", ok, answer)
	}
}

func TestLLMAnswererBuildRequestIncludesOnlyExplicitImageInputs(t *testing.T) {
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
									"text": "{\"decision\":\"needs_execution\",\"answer\":\"\",\"confidence\":\"high\",\"evidence\":[]}"
								}
							]
						}
					]
				}`)),
			}, nil
		}),
	}

	answerer, err := NewLLMAnswerer(LLMAnswererConfig{
		APIKey:          "test-key",
		BaseURL:         "https://example.test/v1",
		Model:           "gpt-5.4",
		ReasoningEffort: "low",
	}, httpClient)
	if err != nil {
		t.Fatalf("create LLM answerer: %v", err)
	}

	inputImagePath := filepath.Join(t.TempDir(), "input.png")
	if err := os.WriteFile(inputImagePath, tinyPNG(), 0o644); err != nil {
		t.Fatalf("write temp input image: %v", err)
	}
	recentImagePath := filepath.Join(t.TempDir(), "recent.png")
	if err := os.WriteFile(recentImagePath, tinyPNG(), 0o644); err != nil {
		t.Fatalf("write temp recent image: %v", err)
	}

	_, _, err = answerer.BuildDirectAnswer(context.Background(), PlanningRequest{
		Message: "解释这张图",
		FocusObject: &models.ObjectMeta{
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
	})
	if err != nil {
		t.Fatalf("build direct answer: %v", err)
	}

	if count := bytes.Count(capturedBody, []byte(`"input_image"`)); count != 1 {
		t.Fatalf("expected exactly one explicit image input, got %d: %s", count, string(capturedBody))
	}
	if !bytes.Contains(capturedBody, []byte(`最近生成图片`)) {
		t.Fatalf("expected recent artifact summary in compact context: %s", string(capturedBody))
	}
}
