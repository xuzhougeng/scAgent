package orchestrator

import "scagent/internal/models"

var inheritedPlotUMAPParamKeys = []string{
	"legend_loc",
	"palette",
	"title",
	"point_size",
	"figure_width",
	"figure_height",
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

func shouldInheritRecentPlotParams(request PlanningRequest) bool {
	if request.CurrentTurn == nil {
		return false
	}
	if request.CurrentTurn.Contract.DeliverableKind != models.TurnDeliverablePlot {
		return false
	}
	if request.CurrentTurn.Contract.FollowUpTurnID == "" && request.CurrentTurn.Contract.FollowUpArtifactID == "" {
		return false
	}
	return latestRecentJobStepBySkill(request, "plot_umap") != nil
}

func mergeRecentPlotUMAPParams(request PlanningRequest, currentParams map[string]any) map[string]any {
	merged := cloneParams(currentParams)
	if merged == nil {
		merged = make(map[string]any)
	}

	if !shouldInheritRecentPlotParams(request) {
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
