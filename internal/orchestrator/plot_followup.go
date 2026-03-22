package orchestrator

import (
	"strings"

	"scagent/internal/models"
)

var plotFollowUpPhrases = []string{
	"这个图",
	"这张图",
	"上一张图",
	"刚才的图",
	"改图",
	"修改图",
	"重画",
	"不符合要求",
}

var inheritedPlotUMAPParamKeys = []string{
	"legend_loc",
	"palette",
	"title",
	"point_size",
	"figure_width",
	"figure_height",
}

func isPlotFollowUpRequest(request PlanningRequest, explicitPlotParams map[string]any) bool {
	if latestRecentPlotSkill(request) == "" {
		return false
	}
	if len(explicitPlotParams) > 0 {
		return true
	}
	return hasPlotFollowUpCue(request.Message)
}

func hasPlotFollowUpCue(message string) bool {
	for _, phrase := range plotFollowUpPhrases {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}

func latestRecentJobStepBySkill(request PlanningRequest, skill string) *models.JobStep {
	for jobIndex := len(request.RecentJobs) - 1; jobIndex >= 0; jobIndex-- {
		job := request.RecentJobs[jobIndex]
		if job == nil {
			continue
		}
		for stepIndex := len(job.Steps) - 1; stepIndex >= 0; stepIndex-- {
			step := job.Steps[stepIndex]
			if step.Skill != skill {
				continue
			}
			copyStep := step
			return &copyStep
		}
	}
	return nil
}

func recentPlotUMAPParams(step models.JobStep) map[string]any {
	params := make(map[string]any, len(inheritedPlotUMAPParamKeys))
	for _, key := range inheritedPlotUMAPParamKeys {
		if step.Metadata != nil {
			if value, ok := step.Metadata[key]; ok && value != nil {
				params[key] = value
				continue
			}
		}
		if step.Params != nil {
			if value, ok := step.Params[key]; ok && value != nil {
				params[key] = value
			}
		}
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

func mergeRecentPlotUMAPParams(request PlanningRequest, currentParams map[string]any) map[string]any {
	explicitPlotParams := parseExplicitPlotParams(request.Message)
	followUp := isPlotFollowUpRequest(request, explicitPlotParams) && latestRecentPlotSkill(request) == "plot_umap"
	merged := cloneParams(currentParams)
	if merged == nil {
		merged = make(map[string]any)
	}

	if !followUp {
		if len(merged) == 0 {
			return nil
		}
		return merged
	}

	recentStep := latestRecentJobStepBySkill(request, "plot_umap")
	if recentStep == nil {
		if len(merged) == 0 {
			return nil
		}
		return merged
	}

	inherited := recentPlotUMAPParams(*recentStep)
	if len(inherited) == 0 {
		if len(merged) == 0 {
			return nil
		}
		return merged
	}

	for key, value := range inherited {
		if _, ok := explicitPlotParams[key]; ok {
			continue
		}
		if _, ok := merged[key]; ok {
			continue
		}
		merged[key] = value
	}

	if len(merged) == 0 {
		return nil
	}
	return merged
}

func applyRecentPlotContext(request PlanningRequest, plan models.Plan) models.Plan {
	if len(plan.Steps) == 0 {
		return plan
	}

	updated := clonePlan(plan)
	for index := range updated.Steps {
		step := &updated.Steps[index]
		if step.Skill != "plot_umap" {
			continue
		}
		step.Params = mergeRecentPlotUMAPParams(request, step.Params)
	}
	return updated
}
