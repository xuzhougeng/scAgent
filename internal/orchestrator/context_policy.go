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
		if request.Session.FocusObjectID != "" {
			parts = append(parts, "focus_object_id="+request.Session.FocusObjectID)
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

	focusObject := request.FocusObject
	lines = append(lines, formatResolvedObjectContext("focus_object", focusObject, true)...)
	lines = append(lines, formatResolvedObjectContext("global_object", request.GlobalObject, true, namedObjectRef{name: "focus_object", object: focusObject})...)
	lines = append(lines, formatResolvedObjectContext("root_object", request.RootObject, true, namedObjectRef{name: "focus_object", object: focusObject}, namedObjectRef{name: "global_object", object: request.GlobalObject})...)
	lines = append(lines, formatCompactObjectsContext(request.Objects, policy.MaxObjects, focusObject, request.GlobalObject, request.RootObject)...)
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

type namedObjectRef struct {
	name   string
	object *models.ObjectMeta
}

func formatResolvedObjectContext(label string, object *models.ObjectMeta, includeSignals bool, aliases ...namedObjectRef) []string {
	if object == nil {
		return []string{"- " + label + "=none"}
	}
	for _, alias := range aliases {
		if alias.object != nil && alias.object.ID != "" && alias.object.ID == object.ID {
			return []string{"- " + label + "=same_as_" + alias.name}
		}
	}
	return []string{"- " + label + "=" + formatPlannerObjectContext(object, includeSignals)}
}

func formatCompactObjectsContext(objects []*models.ObjectMeta, limit int, excluded ...*models.ObjectMeta) []string {
	if len(objects) == 0 || limit <= 0 {
		return []string{"- available_objects=none"}
	}

	excludedIDs := make(map[string]struct{}, len(excluded))
	for _, object := range excluded {
		if object != nil && object.ID != "" {
			excludedIDs[object.ID] = struct{}{}
		}
	}

	lines := []string{"- available_objects:"}
	count := 0
	for _, object := range objects {
		if object == nil {
			continue
		}
		if _, ok := excludedIDs[object.ID]; ok {
			continue
		}
		if count >= limit {
			break
		}
		lines = append(lines, "  "+formatPlannerObjectContext(object, false))
		count++
	}
	if remaining := countRemainingObjects(objects, excludedIDs); remaining > count {
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
