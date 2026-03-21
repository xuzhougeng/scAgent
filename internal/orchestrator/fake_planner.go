package orchestrator

import (
	"context"
	"strconv"
	"strings"

	"scagent/internal/models"
)

type Planner interface {
	Plan(ctx context.Context, request PlanningRequest) (models.Plan, error)
	Mode() string
}

type PlannerDebugger interface {
	DebugPreview(ctx context.Context, request PlanningRequest) (*PlannerDebugPreview, error)
}

type FakePlanner struct{}

func NewFakePlanner() *FakePlanner {
	return &FakePlanner{}
}

func (p *FakePlanner) Mode() string {
	return "fake"
}

func (p *FakePlanner) Plan(_ context.Context, request PlanningRequest) (models.Plan, error) {
	lower := strings.ToLower(request.Message)
	steps := make([]models.PlanStep, 0, 4)

	if strings.Contains(lower, "subset") || strings.Contains(request.Message, "拿出来") || strings.Contains(request.Message, "筛") || strings.Contains(request.Message, "cortex") {
		value := "cortex"
		if !strings.Contains(lower, "cortex") && !strings.Contains(request.Message, "cortex") {
			value = "selected_group"
		}
		steps = append(steps, models.PlanStep{
			ID:             "step_1",
			Skill:          "subset_cells",
			TargetObjectID: "$active",
			Params: map[string]any{
				"obs_field": "cell_type",
				"op":        "eq",
				"value":     value,
			},
		})
	}

	if strings.Contains(lower, "recluster") || strings.Contains(request.Message, "重新聚类") {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "recluster",
			TargetObjectID: targetFromPrevious(steps),
			Params: map[string]any{
				"resolution": 0.6,
			},
		})
	}

	if strings.Contains(lower, "marker") || strings.Contains(request.Message, "marker") || strings.Contains(request.Message, "标记") {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "find_markers",
			TargetObjectID: targetFromPrevious(steps),
			Params: map[string]any{
				"groupby": "leiden",
			},
		})
	}

	if strings.Contains(lower, "umap") || strings.Contains(request.Message, "UMAP") || strings.Contains(request.Message, "降维") {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "plot_umap",
			TargetObjectID: targetFromPrevious(steps),
			Params: map[string]any{
				"color_by": "leiden",
			},
		})
	}

	if strings.Contains(lower, "dotplot") || strings.Contains(request.Message, "点图") {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "plot_dotplot",
			TargetObjectID: targetFromPrevious(steps),
			Params: map[string]any{
				"genes": []string{"WOX5", "SCR", "PLT1"},
			},
		})
	}

	if strings.Contains(lower, "violin") || strings.Contains(request.Message, "小提琴") {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "plot_violin",
			TargetObjectID: targetFromPrevious(steps),
			Params: map[string]any{
				"genes": []string{"WOX5", "SCR"},
			},
		})
	}

	if strings.Contains(lower, "export") || strings.Contains(request.Message, "导出") {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "export_h5ad",
			TargetObjectID: targetFromPrevious(steps),
		})
	}

	if len(steps) == 0 && (strings.Contains(lower, "assess") || strings.Contains(request.Message, "评估")) {
		steps = append(steps, models.PlanStep{
			ID:             "step_1",
			Skill:          "assess_dataset",
			TargetObjectID: "$active",
		})
	}

	if len(steps) == 0 {
		steps = append(steps, models.PlanStep{
			ID:             "step_1",
			Skill:          "inspect_dataset",
			TargetObjectID: "$active",
		})
	}

	return models.Plan{Steps: steps}, nil
}

func (p *FakePlanner) DebugPreview(_ context.Context, request PlanningRequest) (*PlannerDebugPreview, error) {
	return &PlannerDebugPreview{
		PlannerMode:     p.Mode(),
		PlanningRequest: request,
		Note:            "规则规划器基于固定关键词生成步骤，不会构造 LLM 提示词。",
	}, nil
}

func stepID(index int) string {
	return "step_" + strconv.Itoa(index)
}

func targetFromPrevious(steps []models.PlanStep) string {
	if len(steps) == 0 {
		return "$active"
	}
	return "$prev"
}

func NormalizePlan(plan models.Plan) models.Plan {
	if len(plan.Steps) == 0 {
		return plan
	}

	normalized := models.Plan{
		Steps: make([]models.PlanStep, len(plan.Steps)),
	}
	for i, step := range plan.Steps {
		copyStep := step
		if strings.TrimSpace(copyStep.ID) == "" {
			copyStep.ID = stepID(i + 1)
		}
		if copyStep.Params == nil {
			copyStep.Params = map[string]any{}
		}
		normalized.Steps[i] = copyStep
	}
	return normalized
}
