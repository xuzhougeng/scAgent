package orchestrator

import (
	"fmt"
	"strings"

	"scagent/internal/models"
)

type ContextDetailLevel string

const (
	ContextDetailFull    ContextDetailLevel = "full"
	ContextDetailCompact ContextDetailLevel = "compact"
)

type PlanningContextPolicy struct {
	DetailLevel                  ContextDetailLevel
	MaxObjects                   int
	MaxInputArtifacts            int
	MaxRecentMessages            int
	MaxRecentJobs                int
	MaxRecentArtifacts           int
	MaxWorkingMemoryArtifacts    int
	MaxWorkingMemoryPreferences  int
	MaxWorkingMemoryStateChanges int
}

func plannerPlanningContextPolicy() PlanningContextPolicy {
	return PlanningContextPolicy{
		DetailLevel:                  ContextDetailCompact,
		MaxObjects:                   4,
		MaxInputArtifacts:            3,
		MaxRecentMessages:            4,
		MaxRecentJobs:                2,
		MaxRecentArtifacts:           3,
		MaxWorkingMemoryArtifacts:    2,
		MaxWorkingMemoryPreferences:  3,
		MaxWorkingMemoryStateChanges: 3,
	}
}

func answererPlanningContextPolicy() PlanningContextPolicy {
	return PlanningContextPolicy{
		DetailLevel:                  ContextDetailCompact,
		MaxObjects:                   4,
		MaxInputArtifacts:            3,
		MaxRecentMessages:            6,
		MaxRecentJobs:                3,
		MaxRecentArtifacts:           4,
		MaxWorkingMemoryArtifacts:    3,
		MaxWorkingMemoryPreferences:  4,
		MaxWorkingMemoryStateChanges: 4,
	}
}

func evaluatorPlanningContextPolicy() PlanningContextPolicy {
	return PlanningContextPolicy{
		DetailLevel:                  ContextDetailCompact,
		MaxObjects:                   4,
		MaxInputArtifacts:            3,
		MaxRecentMessages:            4,
		MaxRecentJobs:                2,
		MaxRecentArtifacts:           3,
		MaxWorkingMemoryArtifacts:    2,
		MaxWorkingMemoryPreferences:  3,
		MaxWorkingMemoryStateChanges: 3,
	}
}

func formatPlanningContextWithPolicy(request PlanningRequest, policy PlanningContextPolicy) []string {
	if policy.DetailLevel != ContextDetailCompact {
		return formatPlanningContext(request)
	}

	lines := make([]string, 0, 16)
	if request.Session != nil {
		parts := []string{"session_id=" + request.Session.ID}
		if request.Session.ActiveObjectID != "" {
			parts = append(parts, "active_object_id="+request.Session.ActiveObjectID)
		}
		lines = append(lines, "- session="+joinContextParts(parts))
	}

	if request.Workspace != nil {
		parts := []string{"id=" + request.Workspace.ID}
		if request.Workspace.Label != "" {
			parts = append(parts, "label="+truncateText(request.Workspace.Label, 80))
		}
		lines = append(lines, "- workspace="+joinContextParts(parts))
	}

	if request.ActiveObject != nil {
		lines = append(lines, "- active_object="+formatPlannerObjectContext(request.ActiveObject, true))
	} else {
		lines = append(lines, "- active_object=none")
	}

	lines = append(lines, formatCompactObjectsContext(request.Objects, request.ActiveObject, policy.MaxObjects)...)
	lines = append(lines, formatPlannerArtifactGroup("input_artifacts", request.InputArtifacts, policy.MaxInputArtifacts)...)
	lines = append(lines, formatPlannerRecentMessages(request.RecentMessages, policy.MaxRecentMessages)...)
	lines = append(lines, formatPlannerRecentJobs(request.RecentJobs, policy.MaxRecentJobs)...)
	lines = append(lines, formatPlannerArtifactGroup("recent_artifacts", request.RecentArtifacts, policy.MaxRecentArtifacts)...)
	lines = append(lines, formatPlannerWorkingMemoryContext(
		request.WorkingMemory,
		policy.MaxWorkingMemoryArtifacts,
		policy.MaxWorkingMemoryPreferences,
		policy.MaxWorkingMemoryStateChanges,
	)...)
	return lines
}

func formatCompactObjectsContext(objects []*models.ObjectMeta, activeObject *models.ObjectMeta, limit int) []string {
	if len(objects) == 0 || limit <= 0 {
		return []string{"- available_objects=none"}
	}

	lines := []string{"- available_objects:"}
	count := 0
	for _, object := range objects {
		if object == nil {
			continue
		}
		if activeObject != nil && object.ID == activeObject.ID {
			continue
		}
		if count >= limit {
			break
		}
		lines = append(lines, "  "+formatPlannerObjectContext(object, false))
		count++
	}
	if remaining := countRemainingObjects(objects, activeObject); remaining > count {
		lines = append(lines, fmt.Sprintf("  ... %d more object(s)", remaining-count))
	}
	return lines
}

func joinContextParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}
