package orchestrator

import (
	"context"
	"testing"

	"scagent/internal/models"
)

func TestFakePlannerBuildsRoutinePreprocessingWorkflow(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理",
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
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
		got := plan.Steps[index]
		if got.ID != stepID(index+1) {
			t.Fatalf("unexpected step id at %d: got %q want %q", index, got.ID, stepID(index+1))
		}
		if got.Skill != want {
			t.Fatalf("unexpected skill at %d: got %q want %q", index, got.Skill, want)
		}
	}
}

func TestFakePlannerAvoidsDuplicateUMAPSteps(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理并绘制 UMAP 图",
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}

	runUMAPCount := 0
	plotUMAPCount := 0
	for _, step := range plan.Steps {
		if step.Skill == "run_umap" {
			runUMAPCount++
		}
		if step.Skill == "plot_umap" {
			plotUMAPCount++
		}
	}

	if runUMAPCount != 1 {
		t.Fatalf("expected one run_umap step, got %d", runUMAPCount)
	}
	if plotUMAPCount != 1 {
		t.Fatalf("expected one plot_umap step, got %d", plotUMAPCount)
	}
}

func TestFakePlannerSubsetChainsFromPreviousStep(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "完成常规的数据预处理后把 cortex 细胞筛出来",
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}
	if len(plan.Steps) < 7 {
		t.Fatalf("expected preprocessing + subset workflow, got %+v", plan.Steps)
	}

	last := plan.Steps[len(plan.Steps)-1]
	if last.Skill != "subset_cells" {
		t.Fatalf("expected final step to be subset_cells, got %q", last.Skill)
	}
	if last.TargetObjectID != "$prev" {
		t.Fatalf("expected subset to chain from previous output, got %q", last.TargetObjectID)
	}
	if last.ID != stepID(len(plan.Steps)) {
		t.Fatalf("unexpected subset step id: got %q want %q", last.ID, stepID(len(plan.Steps)))
	}
}

func TestFakePlannerTreatsLegendRequestsAsVisualization(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "需要把legend加到data上",
		ActiveObject: &models.ObjectMeta{
			Metadata: map[string]any{
				"obsm_keys": []string{"X_umap"},
			},
		},
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("unexpected step count: got %d", len(plan.Steps))
	}
	if plan.Steps[0].Skill != "plot_umap" {
		t.Fatalf("expected legend request to map to plot_umap, got %q", plan.Steps[0].Skill)
	}
}

func TestFakePlannerLegendRequestRunsUMAPWhenEmbeddingMissing(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "需要把legend加到data上",
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("unexpected step count: got %d", len(plan.Steps))
	}
	if plan.Steps[0].Skill != "run_umap" || plan.Steps[1].Skill != "plot_umap" {
		t.Fatalf("unexpected steps: %+v", plan.Steps)
	}
}

func TestFakePlannerUsesRecentPlotContextForFollowUp(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "把这个图改一下",
		ActiveObject: &models.ObjectMeta{
			Metadata: map[string]any{
				"obsm_keys": []string{"X_umap"},
			},
		},
		RecentJobs: []*models.Job{
			{
				ID:     "job_prev",
				Status: models.JobSucceeded,
				Steps: []models.JobStep{
					{Skill: "plot_umap"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("unexpected step count: got %d", len(plan.Steps))
	}
	if plan.Steps[0].Skill != "plot_umap" {
		t.Fatalf("expected follow-up request to reuse plot_umap, got %q", plan.Steps[0].Skill)
	}
}

func TestFakePlannerSubsetsNamedCellTypeThenPlotsUMAP(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "提取B cells, 单独画UMAP",
		ActiveObject: &models.ObjectMeta{
			Metadata: map[string]any{
				"obsm_keys": []string{"X_umap"},
				"cell_type_annotation": map[string]any{
					"sample_values": []any{"B cells", "T cells"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("unexpected steps: %+v", plan.Steps)
	}
	if plan.Steps[0].Skill != "subset_cells" || plan.Steps[1].Skill != "plot_umap" {
		t.Fatalf("unexpected steps: %+v", plan.Steps)
	}
	if plan.Steps[0].Params["value"] != "B cells" {
		t.Fatalf("expected subset to target B cells, got %+v", plan.Steps[0].Params)
	}
	if plan.Steps[1].TargetObjectID != "$prev" {
		t.Fatalf("expected plot to target subset output, got %+v", plan.Steps[1])
	}
}

func TestFakePlannerCarriesExplicitPlotParams(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "把这个图改一下，legend_loc='on data' point_size=12 title='UMAP with labels'",
		ActiveObject: &models.ObjectMeta{
			Metadata: map[string]any{
				"obsm_keys": []string{"X_umap"},
			},
		},
		RecentJobs: []*models.Job{
			{
				ID:     "job_prev",
				Status: models.JobSucceeded,
				Steps: []models.JobStep{
					{Skill: "plot_umap"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("unexpected step count: got %d", len(plan.Steps))
	}
	params := plan.Steps[0].Params
	if params["legend_loc"] != "on data" {
		t.Fatalf("expected legend_loc to be carried through, got %+v", params)
	}
	if params["title"] != "UMAP with labels" {
		t.Fatalf("expected title to be carried through, got %+v", params)
	}
	pointSize, ok := params["point_size"].(float64)
	if !ok || pointSize != 12 {
		t.Fatalf("expected numeric point_size to be carried through, got %+v", params)
	}
}

func TestFakePlannerUsesPlotGeneUMAPForGeneRequests(t *testing.T) {
	planner := NewFakePlanner()

	plan, err := planner.Plan(context.Background(), PlanningRequest{
		Message: "绘制LDHB的UMAP",
		ActiveObject: &models.ObjectMeta{
			Metadata: map[string]any{
				"obsm_keys": []string{"X_umap"},
			},
		},
	})
	if err != nil {
		t.Fatalf("run fake planner: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("unexpected step count: got %d", len(plan.Steps))
	}
	if plan.Steps[0].Skill != "plot_gene_umap" {
		t.Fatalf("expected gene request to map to plot_gene_umap, got %q", plan.Steps[0].Skill)
	}
	genes, ok := plan.Steps[0].Params["genes"].([]string)
	if !ok {
		t.Fatalf("expected genes param to be []string, got %+v", plan.Steps[0].Params)
	}
	if len(genes) != 1 || genes[0] != "LDHB" {
		t.Fatalf("unexpected genes param: %+v", genes)
	}
}
