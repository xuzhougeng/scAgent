package weixin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"scagent/internal/models"
	"scagent/internal/orchestrator"
	"scagent/internal/session"
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

func TestFinalizeReplyPayloadRewritesExistingImageSummary(t *testing.T) {
	reply := finalizeReplyPayload(replyPayload{
		Text: "如果你愿意，我还可以继续帮你:\n1.画一个更标准的Scanpy stacked violin版本\n2.按你指定的marker列表重画\n3.导出成PNG/PDF\n已附上1张图。",
		Images: []*models.Artifact{
			{ID: "art_plot_1"},
		},
	})

	expectedText := "如果你愿意，我还可以继续帮你:\n1.画一个更标准的Scanpy stacked violin版本\n2.按你指定的marker列表重画\n3.导出成PNG/PDF\n\n已附上 1 张图。"
	if reply.Text != expectedText {
		t.Fatalf("expected normalized reply text %q, got %q", expectedText, reply.Text)
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

func TestWaitForJobWithTimeoutReturnsTerminalSnapshotWithoutEvent(t *testing.T) {
	bridge, sessionID, jobID := newTestBridgeWithJob(t, models.JobSucceeded, "分析完成。", "分析完成。")

	reply, done := bridge.waitForJobWithTimeout(context.Background(), sessionID, jobID, 20*time.Millisecond, nil)
	if !done {
		t.Fatalf("expected completed job to return immediately")
	}
	if reply.Text != "分析完成。" {
		t.Fatalf("expected assistant reply, got %q", reply.Text)
	}
}

func TestWatchPendingJobPushesIncompleteJobReply(t *testing.T) {
	var (
		mu        sync.Mutex
		sentTexts []string
	)
	client := NewClient("https://example.test", "test-token")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"ret":1,"errcode":404,"message":"not found"}`)),
				Header:     make(http.Header),
			}, nil
		}
		var payload SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if len(payload.Msg.ItemList) > 0 && payload.Msg.ItemList[0].TextItem != nil {
			mu.Lock()
			sentTexts = append(sentTexts, payload.Msg.ItemList[0].TextItem.Text)
			mu.Unlock()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ret":0,"errcode":0,"message":"ok"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	bridge, sessionID, jobID := newTestBridgeWithJob(t, models.JobIncomplete, "当前结果还不完整。", "这是当前能给出的结果。")
	bridge.client = client
	pj := &pendingJob{
		SessionID:    sessionID,
		JobID:        jobID,
		StartedAt:    time.Now().Add(-15 * time.Second),
		ContextToken: "ctx-token",
	}
	bridge.pendingJobs["user_1"] = pj

	bridge.watchPendingJob("user_1", pj)

	if _, ok := bridge.pendingJobs["user_1"]; ok {
		t.Fatalf("expected pending job to be cleared")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sentTexts) != 1 {
		t.Fatalf("expected one auto-pushed message, got %d", len(sentTexts))
	}
	if sentTexts[0] != "这是当前能给出的结果。" {
		t.Fatalf("expected incomplete job reply to be pushed, got %q", sentTexts[0])
	}
}

func newTestBridgeWithJob(t *testing.T, status models.JobStatus, summary, answer string) (*Bridge, string, string) {
	t.Helper()

	store := session.NewStore()
	sessionRecord := store.CreateSession("wechat-test")
	now := time.Now().UTC()
	finishedAt := now
	job := &models.Job{
		ID:          store.NextID("job"),
		WorkspaceID: sessionRecord.WorkspaceID,
		SessionID:   sessionRecord.ID,
		Status:      status,
		Summary:     summary,
		CreatedAt:   now,
		FinishedAt:  &finishedAt,
	}
	store.SaveJob(job)
	if answer != "" {
		store.AddMessage(&models.Message{
			ID:        store.NextID("msg"),
			SessionID: sessionRecord.ID,
			JobID:     job.ID,
			Role:      models.MessageAssistant,
			Content:   answer,
			CreatedAt: now,
		})
	}

	return &Bridge{
		service:     orchestrator.NewService(store, nil, nil, nil, t.TempDir()),
		config:      BridgeConfig{JobTimeout: 100 * time.Millisecond},
		pendingJobs: make(map[string]*pendingJob),
		sessions:    make(map[string]string),
	}, sessionRecord.ID, job.ID
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
