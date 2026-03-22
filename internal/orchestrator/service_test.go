package orchestrator

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"scagent/internal/models"
	"scagent/internal/session"
	"scagent/internal/skill"
)

type emptyLLMPlanner struct{}

func (p emptyLLMPlanner) Plan(context.Context, PlanningRequest) (models.Plan, error) {
	return models.Plan{}, nil
}

func (p emptyLLMPlanner) Mode() string {
	return "llm"
}

type failingLLMPlanner struct{}

func (p failingLLMPlanner) Plan(context.Context, PlanningRequest) (models.Plan, error) {
	return models.Plan{}, errors.New("planner request failed: context deadline exceeded")
}

func (p failingLLMPlanner) Mode() string {
	return "llm"
}

func TestBuildExecutablePlanFallsBackWhenLLMReturnsEmptyPlan(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, emptyLLMPlanner{}, t.TempDir())
	plan, note, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err != nil {
		t.Fatalf("build executable plan: %v", err)
	}
	if note == "" {
		t.Fatalf("expected fallback note when LLM returns empty plan")
	}

	wantSkills := []string{
		"normalize_total",
		"log1p_transform",
		"select_hvg",
		"run_pca",
		"compute_neighbors",
		"run_umap",
	}
	if len(plan.Steps) != len(wantSkills) {
		t.Fatalf("unexpected step count: got %d want %d", len(plan.Steps), len(wantSkills))
	}
	for index, want := range wantSkills {
		if plan.Steps[index].Skill != want {
			t.Fatalf("unexpected skill at %d: got %q want %q", index, plan.Steps[index].Skill, want)
		}
	}
}

func TestBuildExecutablePlanFallsBackWhenLLMRequestFails(t *testing.T) {
	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, failingLLMPlanner{}, t.TempDir())
	plan, note, err := service.buildExecutablePlan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err != nil {
		t.Fatalf("build executable plan: %v", err)
	}
	if note == "" {
		t.Fatalf("expected fallback note when LLM request fails")
	}
	if len(plan.Steps) == 0 || plan.Steps[0].Skill != "normalize_total" {
		t.Fatalf("unexpected fallback plan: %+v", plan.Steps)
	}
}

func TestBuildPlanningRequestIncludesRecentContext(t *testing.T) {
	store := session.NewStore()
	service := NewService(store, nil, nil, NewFakePlanner(), t.TempDir())

	sessionRecord := store.CreateSession("test")
	now := time.Now().UTC()
	sessionRecord.ActiveObjectID = "obj_active"
	store.SaveSession(sessionRecord)

	store.SaveObject(&models.ObjectMeta{
		ID:        "obj_active",
		SessionID: sessionRecord.ID,
		Label:     "prepared_pbmc3k",
		Kind:      models.ObjectFilteredDataset,
		Metadata: map[string]any{
			"obsm_keys": []string{"X_umap"},
		},
		CreatedAt:      now,
		LastAccessedAt: now,
	})
	store.AddMessage(&models.Message{
		ID:        "msg_prev_user",
		SessionID: sessionRecord.ID,
		Role:      models.MessageUser,
		Content:   "画一下 UMAP 图",
		CreatedAt: now,
	})
	store.AddMessage(&models.Message{
		ID:        "msg_prev_assistant",
		SessionID: sessionRecord.ID,
		Role:      models.MessageAssistant,
		Content:   "执行完成：plot_umap：已生成真实 UMAP 图。",
		CreatedAt: now.Add(time.Second),
	})
	store.AddMessage(&models.Message{
		ID:        "msg_current",
		SessionID: sessionRecord.ID,
		Role:      models.MessageUser,
		Content:   "把这个图改一下",
		CreatedAt: now.Add(2 * time.Second),
	})
	store.SaveJob(&models.Job{
		ID:        "job_prev",
		SessionID: sessionRecord.ID,
		Status:    models.JobSucceeded,
		Summary:   "已生成 UMAP 图。",
		Steps: []models.JobStep{
			{Skill: "plot_umap"},
		},
		CreatedAt:  now,
		FinishedAt: ptrTime(now.Add(time.Second)),
	})
	store.SaveArtifact(&models.Artifact{
		ID:        "art_prev",
		SessionID: sessionRecord.ID,
		Kind:      models.ArtifactPlot,
		Title:     "prepared_pbmc3k 的 UMAP 图",
		Summary:   "prepared_pbmc3k 的真实 UMAP 散点图。",
		CreatedAt: now.Add(time.Second),
	})

	request, err := service.buildPlanningRequest(sessionRecord, "把这个图改一下")
	if err != nil {
		t.Fatalf("build planning request: %v", err)
	}

	if request.ActiveObject == nil || request.ActiveObject.ID != "obj_active" {
		t.Fatalf("expected active object in planning request, got %+v", request.ActiveObject)
	}
	if len(request.RecentMessages) != 2 {
		t.Fatalf("expected previous messages without current one, got %d", len(request.RecentMessages))
	}
	if request.RecentMessages[len(request.RecentMessages)-1].Content != "执行完成：plot_umap：已生成真实 UMAP 图。" {
		t.Fatalf("unexpected recent assistant message: %+v", request.RecentMessages)
	}
	if len(request.RecentJobs) != 1 || request.RecentJobs[0].ID != "job_prev" {
		t.Fatalf("unexpected recent jobs: %+v", request.RecentJobs)
	}
	if len(request.RecentArtifacts) != 1 || request.RecentArtifacts[0].ID != "art_prev" {
		t.Fatalf("unexpected recent artifacts: %+v", request.RecentArtifacts)
	}
}

func TestUploadPluginBundleRegistersSkills(t *testing.T) {
	dataRoot := t.TempDir()
	registry, err := skill.LoadRegistryWithPluginsAndState(
		skillsRegistryPath(),
		filepath.Join(dataRoot, "skill-hub", "plugins"),
		filepath.Join(dataRoot, "skill-hub", "state.json"),
	)
	if err != nil {
		t.Fatalf("load skills registry with plugin dir: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, NewFakePlanner(), dataRoot)

	var buffer bytes.Buffer
	archiveWriter := zip.NewWriter(&buffer)
	manifest, err := archiveWriter.Create("plugin.json")
	if err != nil {
		t.Fatalf("create manifest entry: %v", err)
	}
	_, _ = manifest.Write([]byte(`{
		"id": "demo-hub",
		"name": "Demo Hub",
		"skills": [
			{
				"name": "demo_runtime_skill",
				"label": "Demo Runtime Skill",
				"category": "custom",
				"support_level": "wired",
				"description": "Uploaded from test bundle.",
				"target_kinds": ["raw_dataset"],
				"input": {},
				"output": {"summary": "string"},
				"runtime": {"entrypoint": "plugin.py"}
			}
		]
	}`))
	script, err := archiveWriter.Create("plugin.py")
	if err != nil {
		t.Fatalf("create script entry: %v", err)
	}
	_, _ = script.Write([]byte("def run(context):\n    return {'summary': 'ok'}\n"))
	if err := archiveWriter.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	bundle, err := service.UploadPluginBundle("demo-hub.zip", bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("upload plugin bundle: %v", err)
	}
	if bundle.ID != "demo-hub" {
		t.Fatalf("unexpected bundle id: %q", bundle.ID)
	}

	if _, ok := service.skills.Get("demo_runtime_skill"); !ok {
		t.Fatalf("expected uploaded plugin skill to be registered")
	}
}

func TestSetPluginBundleEnabledDisablesBuiltinBundle(t *testing.T) {
	dataRoot := t.TempDir()
	registry, err := skill.LoadRegistryWithPluginsAndState(
		skillsRegistryPath(),
		filepath.Join(dataRoot, "skill-hub", "plugins"),
		filepath.Join(dataRoot, "skill-hub", "state.json"),
	)
	if err != nil {
		t.Fatalf("load skills registry with state: %v", err)
	}

	service := NewService(session.NewStore(), registry, nil, NewFakePlanner(), dataRoot)

	bundle, err := service.SetPluginBundleEnabled(skill.BuiltinBundleID, false)
	if err != nil {
		t.Fatalf("disable builtin bundle: %v", err)
	}
	if bundle.Enabled {
		t.Fatalf("expected builtin bundle to be disabled")
	}
	if _, ok := service.skills.Get("inspect_dataset"); ok {
		t.Fatalf("expected builtin inspect_dataset skill to be removed after disabling")
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
