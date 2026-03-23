package orchestrator

import (
	"testing"
	"time"

	"scagent/internal/models"
)

func TestResolveObjectRolesPrefersGlobalFilteredObjectWithinLineage(t *testing.T) {
	root := &models.ObjectMeta{
		ID:             "obj_root",
		Label:          "pbmc_raw",
		Kind:           models.ObjectRawDataset,
		LastAccessedAt: time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC),
	}
	global := &models.ObjectMeta{
		ID:             "obj_global",
		Label:          "pbmc_filtered",
		ParentID:       "obj_root",
		Kind:           models.ObjectFilteredDataset,
		Metadata:       map[string]any{"assessment": map[string]any{"has_umap": true, "preprocessing_state": "analysis_ready"}},
		LastAccessedAt: time.Date(2026, 3, 23, 11, 0, 0, 0, time.UTC),
	}
	subset := &models.ObjectMeta{
		ID:             "obj_subset",
		Label:          "pbmc_b_cells",
		ParentID:       "obj_global",
		Kind:           models.ObjectSubset,
		LastAccessedAt: time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC),
	}

	roles := resolveObjectRoles(&models.Session{FocusObjectID: subset.ID}, []*models.ObjectMeta{root, global, subset})
	if roles.FocusObject == nil || roles.FocusObject.ID != subset.ID {
		t.Fatalf("expected focus object %q, got %+v", subset.ID, roles.FocusObject)
	}
	if roles.RootObject == nil || roles.RootObject.ID != root.ID {
		t.Fatalf("expected root object %q, got %+v", root.ID, roles.RootObject)
	}
	if roles.GlobalObject == nil || roles.GlobalObject.ID != global.ID {
		t.Fatalf("expected global object %q, got %+v", global.ID, roles.GlobalObject)
	}
}

func TestResolveObjectRolesFallsBackToFocusWhenLineageHasNoGlobalCandidate(t *testing.T) {
	subset := &models.ObjectMeta{
		ID:             "obj_subset",
		Label:          "pbmc_subset",
		Kind:           models.ObjectSubset,
		LastAccessedAt: time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC),
	}

	roles := resolveObjectRoles(&models.Session{FocusObjectID: subset.ID}, []*models.ObjectMeta{subset})
	if roles.FocusObject == nil || roles.FocusObject.ID != subset.ID {
		t.Fatalf("expected focus object %q, got %+v", subset.ID, roles.FocusObject)
	}
	if roles.GlobalObject == nil || roles.GlobalObject.ID != subset.ID {
		t.Fatalf("expected global fallback to focus %q, got %+v", subset.ID, roles.GlobalObject)
	}
	if roles.RootObject == nil || roles.RootObject.ID != subset.ID {
		t.Fatalf("expected root fallback to focus %q, got %+v", subset.ID, roles.RootObject)
	}
}
