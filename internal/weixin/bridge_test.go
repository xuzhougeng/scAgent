package weixin

import (
	"testing"
	"time"

	"scagent/internal/models"
)

func TestBuildJobReplyIncludesImageArtifacts(t *testing.T) {
	bridge := &Bridge{}
	imageArtifact := &models.Artifact{
		ID:          "art_plot",
		JobID:       "job_1",
		Kind:        models.ArtifactPlot,
		Title:       "UMAP",
		Path:        "/tmp/umap.png",
		ContentType: "image/png",
	}
	nonImageArtifact := &models.Artifact{
		ID:          "art_table",
		JobID:       "job_1",
		Kind:        models.ArtifactTable,
		Title:       "markers",
		Path:        "/tmp/markers.csv",
		ContentType: "text/csv",
	}

	reply := bridge.buildJobReply(&models.SessionSnapshot{
		Messages: []*models.Message{
			{JobID: "job_1", Role: models.MessageAssistant, Content: "分析完成。"},
		},
		Artifacts: []*models.Artifact{imageArtifact, nonImageArtifact},
	}, "job_1")

	if len(reply.Images) != 1 || reply.Images[0].ID != imageArtifact.ID {
		t.Fatalf("expected only image artifact, got %+v", reply.Images)
	}
	expectedText := "分析完成。\n\n已附上 1 张图。"
	if reply.Text != expectedText {
		t.Fatalf("expected reply text %q, got %q", expectedText, reply.Text)
	}
}

func TestBuildJobReplyFallsBackToJobSummary(t *testing.T) {
	bridge := &Bridge{}
	reply := bridge.buildJobReply(&models.SessionSnapshot{
		Jobs: []*models.Job{
			{ID: "job_2", Summary: "已生成小提琴图。", CreatedAt: time.Now()},
		},
		Artifacts: []*models.Artifact{
			{
				ID:    "art_plot",
				JobID: "job_2",
				Kind:  models.ArtifactPlot,
				Path:  "/tmp/violin.png",
			},
		},
	}, "job_2")

	expectedText := "已生成小提琴图。\n\n已附上 1 张图。"
	if reply.Text != expectedText {
		t.Fatalf("expected summary fallback %q, got %q", expectedText, reply.Text)
	}
	if len(reply.Images) != 1 {
		t.Fatalf("expected 1 image artifact, got %d", len(reply.Images))
	}
}

func TestIsImageArtifactFallsBackToExtension(t *testing.T) {
	artifact := &models.Artifact{
		Kind: models.ArtifactPlot,
		Path: "/tmp/plot.jpeg",
	}
	if !isImageArtifact(artifact) {
		t.Fatalf("expected jpeg path to be treated as image artifact")
	}
}
