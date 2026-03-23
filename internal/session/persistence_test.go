package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scagent/internal/models"
)

func TestSQLitePersistenceSaveUpsertsAndDeletesRows(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "store.db")
	persistence := newSQLitePersistence(statePath)

	now := time.Now().UTC().Truncate(time.Second)
	state1 := &persistedState{
		Counter: 5,
		Workspaces: []*models.Workspace{
			{
				ID:             "ws_000001",
				Label:          "pbmc3k",
				DatasetID:      "ds_000001",
				FocusObjectID:  "obj_000001",
				CreatedAt:      now,
				UpdatedAt:      now,
				LastAccessedAt: now,
			},
		},
		Sessions: []*models.Session{
			{
				ID:             "sess_000001",
				WorkspaceID:    "ws_000001",
				Label:          "marker analysis",
				DatasetID:      "ds_000001",
				FocusObjectID:  "obj_000001",
				Status:         models.SessionActive,
				CreatedAt:      now,
				UpdatedAt:      now,
				LastAccessedAt: now,
			},
		},
		Objects: []*models.ObjectMeta{
			{
				ID:             "obj_000001",
				WorkspaceID:    "ws_000001",
				SessionID:      "sess_000001",
				DatasetID:      "ds_000001",
				Kind:           models.ObjectSubset,
				Label:          "putative_B_cells",
				BackendRef:     "py:sess_000001:adata_2",
				State:          models.ObjectResident,
				InMemory:       true,
				CreatedAt:      now,
				LastAccessedAt: now,
			},
		},
		Messages: []*models.Message{
			{
				ID:        "msg_000001",
				SessionID: "sess_000001",
				Role:      models.MessageUser,
				Content:   "分析 B 细胞 marker",
				CreatedAt: now,
			},
		},
	}

	if err := persistence.Save(state1); err != nil {
		t.Fatalf("save initial state: %v", err)
	}

	state2 := &persistedState{
		Counter: 8,
		Workspaces: []*models.Workspace{
			{
				ID:             "ws_000001",
				Label:          "pbmc3k updated",
				DatasetID:      "ds_000001",
				FocusObjectID:  "obj_000002",
				CreatedAt:      now,
				UpdatedAt:      now.Add(time.Minute),
				LastAccessedAt: now.Add(time.Minute),
			},
		},
		Sessions: []*models.Session{
			{
				ID:             "sess_000001",
				WorkspaceID:    "ws_000001",
				Label:          "dotplot follow-up",
				DatasetID:      "ds_000001",
				FocusObjectID:  "obj_000002",
				Status:         models.SessionActive,
				CreatedAt:      now,
				UpdatedAt:      now.Add(time.Minute),
				LastAccessedAt: now.Add(time.Minute),
			},
		},
		Objects: []*models.ObjectMeta{
			{
				ID:             "obj_000002",
				WorkspaceID:    "ws_000001",
				SessionID:      "sess_000001",
				DatasetID:      "ds_000001",
				Kind:           models.ObjectMarkerResult,
				Label:          "B_cell_markers",
				BackendRef:     "py:sess_000001:adata_3",
				State:          models.ObjectResident,
				InMemory:       true,
				CreatedAt:      now.Add(time.Minute),
				LastAccessedAt: now.Add(time.Minute),
			},
		},
		Messages: []*models.Message{
			{
				ID:        "msg_000001",
				SessionID: "sess_000001",
				Role:      models.MessageAssistant,
				Content:   "已更新分析结论。",
				CreatedAt: now,
			},
		},
	}

	if err := persistence.Save(state2); err != nil {
		t.Fatalf("save updated state: %v", err)
	}

	loaded, err := persistence.Load()
	if err != nil {
		t.Fatalf("load updated state: %v", err)
	}

	if loaded.Counter != 8 {
		t.Fatalf("expected counter 8, got %d", loaded.Counter)
	}
	if len(loaded.Workspaces) != 1 || loaded.Workspaces[0].Label != "pbmc3k updated" {
		t.Fatalf("expected updated workspace row, got %+v", loaded.Workspaces)
	}
	if len(loaded.Sessions) != 1 || loaded.Sessions[0].Label != "dotplot follow-up" {
		t.Fatalf("expected updated session row, got %+v", loaded.Sessions)
	}
	if len(loaded.Objects) != 1 || loaded.Objects[0].ID != "obj_000002" {
		t.Fatalf("expected stale object removed and new object inserted, got %+v", loaded.Objects)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Role != models.MessageAssistant {
		t.Fatalf("expected message row to be updated in place, got %+v", loaded.Messages)
	}
}

func TestSQLitePersistenceResetsOlderSchemaState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "store.db")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	for _, statement := range []string{
		`CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE workspaces (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, last_accessed_at TEXT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, workspace_id TEXT, created_at TEXT NOT NULL, last_accessed_at TEXT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE objects (id TEXT PRIMARY KEY, workspace_id TEXT, session_id TEXT, created_at TEXT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE jobs (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, created_at TEXT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE artifacts (id TEXT PRIMARY KEY, workspace_id TEXT, session_id TEXT, created_at TEXT NOT NULL, payload TEXT NOT NULL)`,
		`CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, created_at TEXT NOT NULL, payload TEXT NOT NULL)`,
		`INSERT INTO metadata(key, value) VALUES('counter', '7')`,
		`INSERT INTO metadata(key, value) VALUES('schema_version', '1')`,
		`INSERT INTO workspaces(id, created_at, last_accessed_at, payload) VALUES('ws_legacy', '2026-03-23T00:00:00Z', '2026-03-23T00:00:00Z', '{"id":"ws_legacy","dataset_id":"ds_legacy","active_object_id":"obj_legacy"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed legacy sqlite state: %v", err)
		}
	}

	persistence := newSQLitePersistence(statePath)
	loaded, err := persistence.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if loaded.Counter != 0 {
		t.Fatalf("expected legacy state to reset counter, got %d", loaded.Counter)
	}
	if len(loaded.Workspaces) != 0 || len(loaded.Sessions) != 0 || len(loaded.Objects) != 0 {
		t.Fatalf("expected legacy state to be cleared, got %+v", loaded)
	}

	version, err := querySingleString(db, `SELECT value FROM metadata WHERE key = 'schema_version'`)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != persistenceSchemaVersion {
		t.Fatalf("expected schema version %q after reset, got %q", persistenceSchemaVersion, version)
	}
}
