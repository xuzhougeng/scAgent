package session

import (
	"fmt"
	"slices"
	"time"

	"scagent/internal/models"
)

const (
	workingMemoryArtifactLimit    = 4
	workingMemoryPreferenceLimit  = 8
	workingMemoryStateChangeLimit = 8
)

func buildWorkingMemory(session *models.Session, objects []*models.ObjectMeta, jobs []*models.Job, artifacts []*models.Artifact) *models.WorkingMemory {
	if session == nil {
		return nil
	}

	objectByID := make(map[string]*models.ObjectMeta, len(objects))
	for _, object := range objects {
		if object == nil {
			continue
		}
		objectByID[object.ID] = object
	}

	artifactByID := make(map[string]*models.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		artifactByID[artifact.ID] = artifact
	}

	memory := &models.WorkingMemory{
		Focus:                buildWorkingMemoryFocus(session, objectByID, jobs, artifacts),
		RecentArtifacts:      buildWorkingMemoryArtifacts(artifacts),
		ConfirmedPreferences: buildWorkingMemoryPreferences(jobs),
		SemanticStateChanges: buildWorkingMemoryStateChanges(jobs, objectByID, artifactByID),
		UpdatedAt:            latestWorkingMemoryTimestamp(session, jobs, artifacts),
	}

	if memory.Focus == nil && len(memory.RecentArtifacts) == 0 && len(memory.ConfirmedPreferences) == 0 && len(memory.SemanticStateChanges) == 0 {
		return nil
	}
	return memory
}

func buildWorkingMemoryFocus(session *models.Session, objectByID map[string]*models.ObjectMeta, jobs []*models.Job, artifacts []*models.Artifact) *models.WorkingMemoryFocus {
	if session == nil {
		return nil
	}

	focus := &models.WorkingMemoryFocus{}
	if session.ActiveObjectID != "" {
		focus.ActiveObjectID = session.ActiveObjectID
		if object, ok := objectByID[session.ActiveObjectID]; ok {
			focus.ActiveObjectLabel = object.Label
		}
	}

	for jobIndex := len(jobs) - 1; jobIndex >= 0; jobIndex-- {
		job := jobs[jobIndex]
		if job == nil {
			continue
		}
		for stepIndex := len(job.Steps) - 1; stepIndex >= 0; stepIndex-- {
			step := job.Steps[stepIndex]
			if step.OutputObjectID == "" {
				continue
			}
			focus.LastOutputObjectID = step.OutputObjectID
			if object, ok := objectByID[step.OutputObjectID]; ok {
				focus.LastOutputObjectLabel = object.Label
			}
			jobIndex = -1
			break
		}
	}

	for index := len(artifacts) - 1; index >= 0; index-- {
		artifact := artifacts[index]
		if artifact == nil {
			continue
		}
		focus.LastArtifactID = artifact.ID
		focus.LastArtifactTitle = artifact.Title
		break
	}

	if *focus == (models.WorkingMemoryFocus{}) {
		return nil
	}
	return focus
}

func buildWorkingMemoryArtifacts(artifacts []*models.Artifact) []models.WorkingMemoryArtifactRef {
	if len(artifacts) == 0 {
		return nil
	}

	refs := make([]models.WorkingMemoryArtifactRef, 0, min(len(artifacts), workingMemoryArtifactLimit))
	for index := len(artifacts) - 1; index >= 0 && len(refs) < workingMemoryArtifactLimit; index-- {
		artifact := artifacts[index]
		if artifact == nil {
			continue
		}
		refs = append(refs, models.WorkingMemoryArtifactRef{
			ID:        artifact.ID,
			Kind:      artifact.Kind,
			ObjectID:  artifact.ObjectID,
			JobID:     artifact.JobID,
			Title:     artifact.Title,
			Summary:   artifact.Summary,
			CreatedAt: artifact.CreatedAt,
		})
	}
	return refs
}

func buildWorkingMemoryPreferences(jobs []*models.Job) []models.WorkingMemoryPreference {
	if len(jobs) == 0 {
		return nil
	}

	preferences := make([]models.WorkingMemoryPreference, 0, workingMemoryPreferenceLimit)
	seen := make(map[string]struct{}, workingMemoryPreferenceLimit)
	for jobIndex := len(jobs) - 1; jobIndex >= 0 && len(preferences) < workingMemoryPreferenceLimit; jobIndex-- {
		job := jobs[jobIndex]
		if job == nil || job.Status != models.JobSucceeded {
			continue
		}
		for stepIndex := len(job.Steps) - 1; stepIndex >= 0 && len(preferences) < workingMemoryPreferenceLimit; stepIndex-- {
			step := job.Steps[stepIndex]
			if step.Status != models.JobSucceeded || len(step.Params) == 0 {
				continue
			}

			keys := make([]string, 0, len(step.Params))
			for key, value := range step.Params {
				if isEmptyWorkingMemoryValue(value) {
					continue
				}
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				identity := fmt.Sprintf("%s.%s", step.Skill, key)
				if _, ok := seen[identity]; ok {
					continue
				}
				seen[identity] = struct{}{}
				preferences = append(preferences, models.WorkingMemoryPreference{
					Skill:        step.Skill,
					Param:        key,
					Value:        cloneWorkingMemoryValue(step.Params[key]),
					SourceJobID:  job.ID,
					SourceStepID: step.ID,
					UpdatedAt:    finishedTimeOrZero(step.FinishedAt, job.FinishedAt, job.CreatedAt),
				})
				if len(preferences) >= workingMemoryPreferenceLimit {
					break
				}
			}
		}
	}
	return preferences
}

func buildWorkingMemoryStateChanges(jobs []*models.Job, objectByID map[string]*models.ObjectMeta, artifactByID map[string]*models.Artifact) []models.WorkingMemoryStateChange {
	if len(jobs) == 0 {
		return nil
	}

	changes := make([]models.WorkingMemoryStateChange, 0, workingMemoryStateChangeLimit)
	for jobIndex := len(jobs) - 1; jobIndex >= 0 && len(changes) < workingMemoryStateChangeLimit; jobIndex-- {
		job := jobs[jobIndex]
		if job == nil {
			continue
		}
		for stepIndex := len(job.Steps) - 1; stepIndex >= 0 && len(changes) < workingMemoryStateChangeLimit; stepIndex-- {
			step := job.Steps[stepIndex]
			createdAt := finishedTimeOrZero(step.FinishedAt, job.FinishedAt, job.CreatedAt)

			if step.OutputObjectID != "" {
				change := models.WorkingMemoryStateChange{
					Kind:      "output_object",
					Skill:     step.Skill,
					JobID:     job.ID,
					StepID:    step.ID,
					Summary:   step.Summary,
					ObjectID:  step.OutputObjectID,
					CreatedAt: createdAt,
				}
				if object, ok := objectByID[step.OutputObjectID]; ok {
					change.ObjectLabel = object.Label
				}
				changes = append(changes, change)
				if len(changes) >= workingMemoryStateChangeLimit {
					break
				}
			}

			for artifactIndex := len(step.ArtifactIDs) - 1; artifactIndex >= 0 && len(changes) < workingMemoryStateChangeLimit; artifactIndex-- {
				artifactID := step.ArtifactIDs[artifactIndex]
				artifact, ok := artifactByID[artifactID]
				if !ok {
					continue
				}
				changes = append(changes, models.WorkingMemoryStateChange{
					Kind:          "artifact",
					Skill:         step.Skill,
					JobID:         job.ID,
					StepID:        step.ID,
					Summary:       artifact.Summary,
					ObjectID:      artifact.ObjectID,
					ArtifactID:    artifact.ID,
					ArtifactTitle: artifact.Title,
					CreatedAt:     artifact.CreatedAt,
				})
			}

			if step.OutputObjectID == "" && len(step.ArtifactIDs) == 0 && step.Summary != "" && len(changes) < workingMemoryStateChangeLimit {
				changes = append(changes, models.WorkingMemoryStateChange{
					Kind:      "step_result",
					Skill:     step.Skill,
					JobID:     job.ID,
					StepID:    step.ID,
					Summary:   step.Summary,
					CreatedAt: createdAt,
				})
			}
		}
	}
	return changes
}

func latestWorkingMemoryTimestamp(session *models.Session, jobs []*models.Job, artifacts []*models.Artifact) time.Time {
	if session == nil {
		return time.Time{}
	}
	latest := session.UpdatedAt
	for _, job := range jobs {
		if job == nil {
			continue
		}
		if job.FinishedAt != nil && job.FinishedAt.After(latest) {
			latest = *job.FinishedAt
		}
		if job.CreatedAt.After(latest) {
			latest = job.CreatedAt
		}
	}
	for _, artifact := range artifacts {
		if artifact != nil && artifact.CreatedAt.After(latest) {
			latest = artifact.CreatedAt
		}
	}
	return latest
}

func finishedTimeOrZero(stepFinishedAt, jobFinishedAt *time.Time, fallback time.Time) time.Time {
	switch {
	case stepFinishedAt != nil:
		return *stepFinishedAt
	case jobFinishedAt != nil:
		return *jobFinishedAt
	default:
		return fallback
	}
}

func isEmptyWorkingMemoryValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case []string:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}

func cloneWorkingMemoryValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			out[key] = cloneWorkingMemoryValue(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, nested := range typed {
			out[index] = cloneWorkingMemoryValue(nested)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
