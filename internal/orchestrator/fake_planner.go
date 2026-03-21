package orchestrator

import (
	"context"
	"regexp"
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

var plotAssignmentPattern = regexp.MustCompile(`(?i)\b(color_by|legend_loc|palette|title|point_size|figure_width|figure_height)\s*=\s*(?:'([^']*)'|"([^"]*)"|([^\s,;]+))`)

func NewFakePlanner() *FakePlanner {
	return &FakePlanner{}
}

func (p *FakePlanner) Mode() string {
	return "fake"
}

func (p *FakePlanner) Plan(_ context.Context, request PlanningRequest) (models.Plan, error) {
	lower := strings.ToLower(request.Message)
	steps := make([]models.PlanStep, 0, 4)
	explicitPlotParams := parseExplicitPlotParams(request.Message)
	wantsLegend := strings.Contains(lower, "legend") || strings.Contains(request.Message, "图例")
	wantsPlot := strings.Contains(lower, "plot") || strings.Contains(request.Message, "画") || strings.Contains(request.Message, "绘") || wantsLegend
	activeHasUMAP := objectHasEmbedding(request.ActiveObject, "X_umap")
	recentPlotSkill := latestRecentPlotSkill(request)
	wantsPlotFollowUp := strings.Contains(request.Message, "这个图") ||
		strings.Contains(request.Message, "这张图") ||
		strings.Contains(request.Message, "上一张图") ||
		strings.Contains(request.Message, "刚才的图") ||
		strings.Contains(request.Message, "改图") ||
		strings.Contains(request.Message, "修改图") ||
		strings.Contains(request.Message, "重画") ||
		strings.Contains(request.Message, "不符合要求") ||
		(len(explicitPlotParams) > 0 && recentPlotSkill != "")

	if strings.Contains(lower, "preprocess") || strings.Contains(request.Message, "预处理") {
		steps = append(steps,
			models.PlanStep{ID: "step_1", Skill: "normalize_total", TargetObjectID: "$active"},
			models.PlanStep{ID: "step_2", Skill: "log1p_transform", TargetObjectID: "$prev"},
			models.PlanStep{ID: "step_3", Skill: "select_hvg", TargetObjectID: "$prev"},
			models.PlanStep{ID: "step_4", Skill: "run_pca", TargetObjectID: "$prev"},
			models.PlanStep{ID: "step_5", Skill: "compute_neighbors", TargetObjectID: "$prev"},
			models.PlanStep{ID: "step_6", Skill: "run_umap", TargetObjectID: "$prev"},
		)
	}

	if strings.Contains(lower, "subset") || strings.Contains(request.Message, "拿出来") || strings.Contains(request.Message, "筛") || strings.Contains(request.Message, "cortex") {
		value := "cortex"
		if !strings.Contains(lower, "cortex") && !strings.Contains(request.Message, "cortex") {
			value = "selected_group"
		}
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "subset_cells",
			TargetObjectID: targetFromPrevious(steps),
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

	if strings.Contains(lower, "umap") || strings.Contains(request.Message, "UMAP") || strings.Contains(request.Message, "降维") || wantsLegend || (wantsPlotFollowUp && recentPlotSkill == "plot_umap") {
		if !hasSkill(steps, "run_umap") && ((!wantsPlot && !wantsPlotFollowUp) || !activeHasUMAP) {
			steps = append(steps, models.PlanStep{
				ID:             stepID(len(steps) + 1),
				Skill:          "run_umap",
				TargetObjectID: targetFromPrevious(steps),
				Params:         map[string]any{},
			})
		}
		if !hasSkill(steps, "plot_umap") {
			plotParams := map[string]any{
				"color_by": "leiden",
			}
			for key, value := range explicitPlotParams {
				plotParams[key] = value
			}
			steps = append(steps, models.PlanStep{
				ID:             stepID(len(steps) + 1),
				Skill:          "plot_umap",
				TargetObjectID: targetFromPrevious(steps),
				Params:         plotParams,
			})
		}
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

func hasSkill(steps []models.PlanStep, skill string) bool {
	for _, step := range steps {
		if step.Skill == skill {
			return true
		}
	}
	return false
}

func objectHasEmbedding(object *models.ObjectMeta, embedding string) bool {
	if object == nil || object.Metadata == nil {
		return false
	}

	value, ok := object.Metadata["obsm_keys"]
	if !ok {
		return false
	}

	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			if item == embedding {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == embedding {
				return true
			}
		}
	}

	return false
}

func latestRecentPlotSkill(request PlanningRequest) string {
	for index := len(request.RecentJobs) - 1; index >= 0; index-- {
		job := request.RecentJobs[index]
		if job == nil {
			continue
		}
		for stepIndex := len(job.Steps) - 1; stepIndex >= 0; stepIndex-- {
			step := job.Steps[stepIndex]
			if strings.HasPrefix(step.Skill, "plot_") {
				return step.Skill
			}
		}
	}

	for index := len(request.RecentArtifacts) - 1; index >= 0; index-- {
		artifact := request.RecentArtifacts[index]
		if artifact == nil || artifact.Kind != models.ArtifactPlot {
			continue
		}
		title := strings.ToLower(artifact.Title)
		summary := strings.ToLower(artifact.Summary)
		if strings.Contains(title, "umap") || strings.Contains(summary, "umap") {
			return "plot_umap"
		}
	}

	return ""
}

func parseExplicitPlotParams(message string) map[string]any {
	matches := plotAssignmentPattern.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return nil
	}

	params := make(map[string]any, len(matches))
	for _, match := range matches {
		if len(match) < 5 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(match[1]))
		value := strings.TrimSpace(firstNonEmpty(match[2], match[3], match[4]))
		if value == "" {
			continue
		}

		switch key {
		case "point_size", "figure_width", "figure_height":
			number, err := strconv.ParseFloat(value, 64)
			if err != nil {
				continue
			}
			params[key] = number
		default:
			params[key] = value
		}
	}

	if len(params) == 0 {
		return nil
	}
	return params
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
