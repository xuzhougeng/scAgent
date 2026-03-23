package orchestrator

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scagent/internal/models"
)

const maxVisualInputArtifacts = 2

func buildUserInputContent(message string, inputArtifacts []*models.Artifact, recentArtifacts []*models.Artifact) any {
	visualArtifacts := collectVisualInputArtifacts(inputArtifacts, recentArtifacts, maxVisualInputArtifacts)
	if len(visualArtifacts) == 0 {
		return message
	}

	items := make([]map[string]any, 0, len(visualArtifacts)+1)
	if strings.TrimSpace(message) != "" {
		items = append(items, map[string]any{
			"type": "input_text",
			"text": message,
		})
	}
	for _, artifact := range visualArtifacts {
		dataURL, err := artifactDataURL(artifact)
		if err != nil {
			continue
		}
		items = append(items, map[string]any{
			"type":      "input_image",
			"image_url": dataURL,
			"detail":    "auto",
		})
	}
	if len(items) == 0 {
		return message
	}
	return items
}

func collectVisualInputArtifacts(inputArtifacts []*models.Artifact, recentArtifacts []*models.Artifact, limit int) []*models.Artifact {
	if limit <= 0 {
		return nil
	}

	selected := make([]*models.Artifact, 0, limit)
	seen := make(map[string]struct{}, limit)
	appendArtifact := func(artifact *models.Artifact) {
		if artifact == nil || !isVisualArtifact(artifact) || len(selected) >= limit {
			return
		}
		key := artifact.ID
		if key == "" {
			key = artifact.Path
		}
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		selected = append(selected, artifact)
	}

	for _, artifact := range inputArtifacts {
		appendArtifact(artifact)
	}
	for index := len(recentArtifacts) - 1; index >= 0 && len(selected) < limit; index-- {
		appendArtifact(recentArtifacts[index])
	}
	return selected
}

func isVisualArtifact(artifact *models.Artifact) bool {
	if artifact == nil || strings.TrimSpace(artifact.Path) == "" {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(artifact.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(artifact.Path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func artifactDataURL(artifact *models.Artifact) (string, error) {
	if artifact == nil || strings.TrimSpace(artifact.Path) == "" {
		return "", fmt.Errorf("artifact path is required")
	}
	data, err := os.ReadFile(artifact.Path)
	if err != nil {
		return "", err
	}
	contentType := strings.TrimSpace(artifact.ContentType)
	if contentType == "" {
		contentType = detectArtifactContentType(artifact.Path)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func detectArtifactContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}
