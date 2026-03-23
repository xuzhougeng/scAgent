package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scagent/internal/models"
)

func TestBuildUserInputContentIncludesImageDataURL(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "inbound.png")
	if err := os.WriteFile(imagePath, tinyPNG(), 0o644); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	content := buildUserInputContent("请解释这张图", []*models.Artifact{
		{
			ID:          "artifact_1",
			Path:        imagePath,
			ContentType: "image/png",
			Title:       "微信图片",
		},
	}, nil)

	items, ok := content.([]map[string]any)
	if !ok {
		t.Fatalf("expected multimodal content array, got %T", content)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 content items, got %d", len(items))
	}
	if items[0]["type"] != "input_text" {
		t.Fatalf("expected first item to be input_text, got %+v", items[0])
	}
	if items[1]["type"] != "input_image" {
		t.Fatalf("expected second item to be input_image, got %+v", items[1])
	}
	imageURL, _ := items[1]["image_url"].(string)
	if !strings.HasPrefix(imageURL, "data:image/png;base64,") {
		t.Fatalf("expected png data URL, got %q", imageURL)
	}
}

func TestBuildUserInputContentIgnoresCSVArtifacts(t *testing.T) {
	csvPath := filepath.Join(t.TempDir(), "markers.csv")
	if err := os.WriteFile(csvPath, []byte("gene,score\nCD3D,1.2\nMS4A1,0.9\n"), 0o644); err != nil {
		t.Fatalf("write temp csv: %v", err)
	}

	content := buildUserInputContent("根据这个 marker 列表画图", []*models.Artifact{
		{
			ID:          "artifact_csv",
			Path:        csvPath,
			ContentType: "text/csv",
			Title:       "marker csv",
		},
	}, nil)

	got, ok := content.(string)
	if !ok {
		t.Fatalf("expected plain text content for csv artifact, got %T", content)
	}
	if got != "根据这个 marker 列表画图" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func tinyPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
}
