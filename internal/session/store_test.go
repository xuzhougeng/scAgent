package session

import (
	"path/filepath"
	"testing"
	"time"

	"scagent/internal/models"
)

func TestPersistentStoreRestoresWorkspaceConversationState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "store.db")

	store, err := NewPersistentStore(statePath)
	if err != nil {
		t.Fatalf("create persistent store: %v", err)
	}

	workspace := store.CreateWorkspace("pbmc3k")
	conversation, err := store.CreateConversation(workspace.ID, "marker analysis")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	object := &models.ObjectMeta{
		ID:             store.NextID("obj"),
		WorkspaceID:    workspace.ID,
		SessionID:      conversation.ID,
		DatasetID:      workspace.DatasetID,
		Kind:           models.ObjectSubset,
		Label:          "putative_B_cells",
		BackendRef:     "py:sess_000001:adata_2",
		NObs:           412,
		NVars:          32738,
		State:          models.ObjectResident,
		InMemory:       true,
		CreatedAt:      now,
		LastAccessedAt: now,
	}
	store.SaveObject(object)

	workspace.FocusObjectID = object.ID
	workspace.UpdatedAt = now
	workspace.LastAccessedAt = now
	store.SaveWorkspace(workspace)

	conversation.FocusObjectID = object.ID
	conversation.UpdatedAt = now
	conversation.LastAccessedAt = now
	store.SaveSession(conversation)

	messageUser := &models.Message{
		ID:        store.NextID("msg"),
		SessionID: conversation.ID,
		Role:      models.MessageUser,
		Content:   "帮我分析 B 细胞 marker",
		CreatedAt: now,
	}
	store.AddMessage(messageUser)

	job := &models.Job{
		ID:          store.NextID("job"),
		WorkspaceID: workspace.ID,
		SessionID:   conversation.ID,
		MessageID:   messageUser.ID,
		Status:      models.JobSucceeded,
		Summary:     "已完成 B 细胞 marker 分析",
		CreatedAt:   now,
	}
	store.SaveJob(job)

	artifact := &models.Artifact{
		ID:          store.NextID("art"),
		WorkspaceID: workspace.ID,
		SessionID:   conversation.ID,
		ObjectID:    object.ID,
		JobID:       job.ID,
		Kind:        models.ArtifactPlot,
		Title:       "B cell marker dotplot",
		Path:        filepath.Join("workspaces", workspace.ID, "artifacts", "marker-dotplot.png"),
		URL:         "/data/workspaces/" + workspace.ID + "/artifacts/marker-dotplot.png",
		ContentType: "image/png",
		Summary:     "top10 marker gene dotplot",
		CreatedAt:   now,
	}
	store.SaveArtifact(artifact)

	job.Steps = []models.JobStep{
		{
			ID:             "step_1",
			Skill:          "plot_dotplot",
			Status:         models.JobSucceeded,
			Summary:        "已输出 B 细胞 top10 marker dotplot。",
			OutputObjectID: object.ID,
			ArtifactIDs:    []string{artifact.ID},
			Params: map[string]any{
				"groupby": "cell_type",
			},
			FinishedAt: &now,
		},
	}
	store.SaveJob(job)

	messageAssistant := &models.Message{
		ID:        store.NextID("msg"),
		SessionID: conversation.ID,
		JobID:     job.ID,
		Role:      models.MessageAssistant,
		Content:   "已输出 B 细胞 top10 marker dotplot。",
		CreatedAt: now.Add(time.Second),
	}
	store.AddMessage(messageAssistant)

	reloaded, err := NewPersistentStore(statePath)
	if err != nil {
		t.Fatalf("reload persistent store: %v", err)
	}

	snapshot, err := reloaded.Snapshot(conversation.ID)
	if err != nil {
		t.Fatalf("snapshot after reload: %v", err)
	}

	if snapshot.Workspace == nil || snapshot.Workspace.ID != workspace.ID {
		t.Fatalf("expected workspace %q after reload, got %+v", workspace.ID, snapshot.Workspace)
	}
	if snapshot.Session.WorkspaceID != workspace.ID {
		t.Fatalf("expected conversation to keep workspace id, got %+v", snapshot.Session)
	}
	if snapshot.Session.FocusObjectID != object.ID {
		t.Fatalf("expected focus object %q, got %q", object.ID, snapshot.Session.FocusObjectID)
	}
	if len(snapshot.Objects) != 1 || snapshot.Objects[0].ID != object.ID {
		t.Fatalf("expected restored object %q, got %+v", object.ID, snapshot.Objects)
	}
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].ID != job.ID {
		t.Fatalf("expected restored job %q, got %+v", job.ID, snapshot.Jobs)
	}
	if len(snapshot.Artifacts) != 1 || snapshot.Artifacts[0].ID != artifact.ID {
		t.Fatalf("expected restored artifact %q, got %+v", artifact.ID, snapshot.Artifacts)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("expected 2 restored messages, got %d", len(snapshot.Messages))
	}
	if snapshot.Messages[0].ID != messageUser.ID || snapshot.Messages[1].ID != messageAssistant.ID {
		t.Fatalf("expected messages to preserve order, got %+v", snapshot.Messages)
	}
	if snapshot.WorkingMemory == nil {
		t.Fatalf("expected working memory in snapshot")
	}
	if snapshot.WorkingMemory.Focus == nil || snapshot.WorkingMemory.Focus.FocusObjectID != object.ID {
		t.Fatalf("expected working memory focus on focus object %q, got %+v", object.ID, snapshot.WorkingMemory.Focus)
	}
	if len(snapshot.WorkingMemory.RecentArtifacts) != 1 || snapshot.WorkingMemory.RecentArtifacts[0].ID != artifact.ID {
		t.Fatalf("expected working memory to reference recent artifact %q, got %+v", artifact.ID, snapshot.WorkingMemory.RecentArtifacts)
	}
	if len(snapshot.WorkingMemory.ConfirmedPreferences) == 0 {
		t.Fatalf("expected working memory confirmed preferences, got %+v", snapshot.WorkingMemory)
	}
	if len(snapshot.WorkingMemory.SemanticStateChanges) == 0 {
		t.Fatalf("expected working memory state changes, got %+v", snapshot.WorkingMemory)
	}

	workspaceSnapshot, err := reloaded.WorkspaceSnapshot(workspace.ID)
	if err != nil {
		t.Fatalf("workspace snapshot after reload: %v", err)
	}
	if len(workspaceSnapshot.Conversations) != 1 || workspaceSnapshot.Conversations[0].ID != conversation.ID {
		t.Fatalf("expected restored conversation %q, got %+v", conversation.ID, workspaceSnapshot.Conversations)
	}
	if len(workspaceSnapshot.Objects) != 1 || workspaceSnapshot.Objects[0].ID != object.ID {
		t.Fatalf("expected workspace snapshot to restore object %q, got %+v", object.ID, workspaceSnapshot.Objects)
	}
	if len(workspaceSnapshot.Artifacts) != 1 || workspaceSnapshot.Artifacts[0].ID != artifact.ID {
		t.Fatalf("expected workspace snapshot to restore artifact %q, got %+v", artifact.ID, workspaceSnapshot.Artifacts)
	}

	workspaces := reloaded.ListWorkspaces()
	if len(workspaces) != 1 || workspaces[0].ID != workspace.ID {
		t.Fatalf("expected workspace list to restore %q, got %+v", workspace.ID, workspaces)
	}

	nextConversationID := reloaded.NextID("sess")
	if nextConversationID == conversation.ID {
		t.Fatalf("expected reloaded counter to advance beyond existing conversation id")
	}
}
