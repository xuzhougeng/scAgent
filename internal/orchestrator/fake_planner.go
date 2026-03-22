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
var englishCellTypePattern = regexp.MustCompile(`(?i)\b([a-z0-9][a-z0-9_+\- ]*cells?)\b`)
var chineseCellTypePattern = regexp.MustCompile(`提取\s*([^\s,，。；;]+?)\s*细胞`)
var geneTokenPattern = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9._-]{2,}\b`)

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
	wantsSubcluster := strings.Contains(lower, "subcluster") || strings.Contains(request.Message, "亚群")
	activeHasUMAP := objectHasEmbedding(request.ActiveObject, "X_umap")
	activeIsSubset := request.ActiveObject != nil && (request.ActiveObject.Kind == models.ObjectSubset || request.ActiveObject.Kind == models.ObjectReclustered)
	recentPlotSkill := latestRecentPlotSkill(request)
	wantsPlotFollowUp := isPlotFollowUpRequest(request, explicitPlotParams)
	cellTypeValue := inferCellTypeValue(request, request.Message)
	geneNames := inferGeneNames(request.Message)

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

	if wantsSubcluster && activeIsSubset {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "reanalyze_subset",
			TargetObjectID: targetFromPrevious(steps),
			Params: map[string]any{
				"resolution": 0.6,
			},
		})
	} else if wantsSubcluster && cellTypeValue != "" {
		steps = append(steps, models.PlanStep{
			ID:             stepID(len(steps) + 1),
			Skill:          "subcluster_from_global",
			TargetObjectID: targetFromPrevious(steps),
			Params: map[string]any{
				"obs_field":   "cell_type",
				"op":          "eq",
				"value":       cellTypeValue,
				"resolution":  0.6,
				"n_neighbors": 15,
			},
		})
	}

	if !wantsSubcluster && (strings.Contains(lower, "subset") || strings.Contains(request.Message, "提取") || strings.Contains(request.Message, "拿出来") || strings.Contains(request.Message, "筛") || strings.Contains(request.Message, "cortex") || cellTypeValue != "") {
		value := "cortex"
		if cellTypeValue != "" {
			value = cellTypeValue
		} else if !strings.Contains(lower, "cortex") && !strings.Contains(request.Message, "cortex") {
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
		if !hasSkill(steps, "run_umap") && !hasSkill(steps, "prepare_umap") && !hasSkill(steps, "subcluster_from_global") && !hasSkill(steps, "reanalyze_subset") && ((!wantsPlot && !wantsPlotFollowUp) || !activeHasUMAP) {
			steps = append(steps, models.PlanStep{
				ID:             stepID(len(steps) + 1),
				Skill:          "run_umap",
				TargetObjectID: targetFromPrevious(steps),
				Params:         map[string]any{},
			})
		}
		if len(geneNames) > 0 && !hasSkill(steps, "plot_gene_umap") {
			steps = append(steps, models.PlanStep{
				ID:             stepID(len(steps) + 1),
				Skill:          "plot_gene_umap",
				TargetObjectID: targetFromPrevious(steps),
				Params: map[string]any{
					"genes": geneNames,
				},
			})
		} else if !hasSkill(steps, "plot_umap") {
			plotParams := map[string]any{
				"color_by": "leiden",
			}
			for key, value := range explicitPlotParams {
				plotParams[key] = value
			}
			plotParams = mergeRecentPlotUMAPParams(request, plotParams)
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

func inferCellTypeValue(request PlanningRequest, message string) string {
	for _, candidate := range candidateCellTypeValues(request.ActiveObject) {
		if strings.Contains(strings.ToLower(message), strings.ToLower(candidate)) {
			return candidate
		}
	}

	if match := chineseCellTypePattern.FindStringSubmatch(message); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if match := englishCellTypePattern.FindStringSubmatch(message); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func candidateCellTypeValues(object *models.ObjectMeta) []string {
	if object == nil || object.Metadata == nil {
		return nil
	}

	out := make([]string, 0, 8)
	if annotation, ok := object.Metadata["cell_type_annotation"].(map[string]any); ok {
		if values, ok := annotation["sample_values"].([]any); ok {
			for _, value := range values {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, text)
				}
			}
		}
	}
	if fields, ok := object.Metadata["categorical_obs_fields"].([]any); ok {
		for _, field := range fields {
			fieldMap, ok := field.(map[string]any)
			if !ok {
				continue
			}
			role, _ := fieldMap["role"].(string)
			if role != "cell_type" {
				continue
			}
			values, ok := fieldMap["sample_values"].([]any)
			if !ok {
				continue
			}
			for _, value := range values {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, text)
				}
			}
		}
	}
	return dedupeStrings(out)
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func inferGeneNames(message string) []string {
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "umap") {
		return nil
	}

	matches := geneTokenPattern.FindAllString(message, -1)
	if len(matches) == 0 {
		return nil
	}

	stopwords := map[string]struct{}{
		"and": {}, "best": {}, "by": {}, "cell": {}, "cells": {}, "cluster": {}, "clusters": {}, "color": {}, "cortex": {}, "data": {},
		"draw": {}, "expression": {}, "for": {}, "gene": {}, "genes": {}, "legend": {}, "legend_loc": {}, "leiden": {}, "louvain": {},
		"none": {}, "on": {}, "palette": {}, "plot": {}, "point_size": {}, "right": {}, "title": {}, "umap": {}, "with": {},
	}

	out := make([]string, 0, len(matches))
	for _, match := range matches {
		key := strings.ToLower(strings.TrimSpace(match))
		if _, blocked := stopwords[key]; blocked {
			continue
		}

		hasGeneSignal := false
		for _, char := range match {
			if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' {
				hasGeneSignal = true
				break
			}
		}
		if !hasGeneSignal {
			continue
		}
		out = append(out, match)
	}

	return dedupeStrings(out)
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
