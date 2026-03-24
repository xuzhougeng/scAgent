package orchestrator

import (
	"slices"

	"scagent/internal/models"
)

type ResolvedObjectRoles struct {
	FocusObject  *models.ObjectMeta
	GlobalObject *models.ObjectMeta
	RootObject   *models.ObjectMeta
}

func resolveObjectRoles(sessionRecord *models.Session, objects []*models.ObjectMeta) ResolvedObjectRoles {
	objectByID := make(map[string]*models.ObjectMeta, len(objects))
	for _, object := range objects {
		if object == nil || object.ID == "" {
			continue
		}
		objectByID[object.ID] = object
	}

	focus := objectByID[sessionRecord.FocusObjectID]
	if focus == nil {
		for _, object := range objects {
			if object != nil {
				focus = object
				break
			}
		}
	}
	if focus == nil {
		return ResolvedObjectRoles{}
	}

	root := lineageRootObject(focus, objectByID)
	global := bestGlobalObjectInLineage(root, focus, objects, objectByID)
	if global == nil {
		global = focus
	}

	return ResolvedObjectRoles{
		FocusObject:  focus,
		GlobalObject: global,
		RootObject:   root,
	}
}

func lineageRootObject(object *models.ObjectMeta, objectByID map[string]*models.ObjectMeta) *models.ObjectMeta {
	if object == nil {
		return nil
	}
	current := object
	seen := map[string]struct{}{}
	for current != nil && current.ParentID != "" {
		if _, ok := seen[current.ID]; ok {
			break
		}
		seen[current.ID] = struct{}{}
		parent := objectByID[current.ParentID]
		if parent == nil {
			break
		}
		current = parent
	}
	return current
}

func bestGlobalObjectInLineage(root, focus *models.ObjectMeta, objects []*models.ObjectMeta, objectByID map[string]*models.ObjectMeta) *models.ObjectMeta {
	if root == nil {
		return focus
	}
	candidates := make([]*models.ObjectMeta, 0, len(objects))
	for _, object := range objects {
		if object == nil {
			continue
		}
		if lineageRootObject(object, objectByID) == nil || lineageRootObject(object, objectByID).ID != root.ID {
			continue
		}
		if !isGlobalScopeObject(object) {
			continue
		}
		candidates = append(candidates, object)
	}
	if len(candidates) == 0 {
		return focus
	}
	slices.SortStableFunc(candidates, func(left, right *models.ObjectMeta) int {
		return compareResolvedObjectPriority(left, right)
	})
	return candidates[0]
}

func isGlobalScopeObject(object *models.ObjectMeta) bool {
	if object == nil {
		return false
	}
	switch object.Kind {
	case models.ObjectRawDataset, models.ObjectFilteredDataset, models.ObjectUnknown:
		return true
	default:
		return false
	}
}

func compareResolvedObjectPriority(left, right *models.ObjectMeta) int {
	leftScore := resolvedObjectPriority(left)
	rightScore := resolvedObjectPriority(right)
	if leftScore != rightScore {
		if leftScore > rightScore {
			return -1
		}
		return 1
	}
	if left == nil || right == nil {
		return 0
	}
	if left.LastAccessedAt.After(right.LastAccessedAt) {
		return -1
	}
	if right.LastAccessedAt.After(left.LastAccessedAt) {
		return 1
	}
	return 0
}

func resolvedObjectPriority(object *models.ObjectMeta) int {
	if object == nil {
		return -1
	}
	score := 0
	switch object.Kind {
	case models.ObjectFilteredDataset:
		score += 30
	case models.ObjectRawDataset:
		score += 20
	default:
		score += 5
	}
	if objectHasEmbedding(object, "X_umap") {
		score += 10
	}
	if objectHasEmbedding(object, "X_pca") {
		score += 5
	}
	if objectIsAnalysisReady(object) {
		score += 10
	}
	return score
}

func objectIsAnalysisReady(object *models.ObjectMeta) bool {
	if object == nil || len(object.Metadata) == 0 {
		return false
	}
	assessment, ok := object.Metadata["assessment"].(map[string]any)
	if !ok {
		return objectHasEmbedding(object, "X_umap")
	}
	if state, ok := assessment["preprocessing_state"].(string); ok && state == "analysis_ready" {
		return true
	}
	if hasUMAP, ok := assessment["has_umap"].(bool); ok && hasUMAP {
		return true
	}
	return objectHasEmbedding(object, "X_umap")
}
