package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	"scagent/internal/models"
)

const persistenceSchemaVersion = "2"

type statePersistence interface {
	Load() (*persistedState, error)
	Save(*persistedState) error
}

type persistedState struct {
	Counter    uint64               `json:"counter"`
	Workspaces []*models.Workspace  `json:"workspaces"`
	Sessions   []*models.Session    `json:"sessions"`
	Objects    []*models.ObjectMeta `json:"objects"`
	Jobs       []*models.Job        `json:"jobs"`
	Artifacts  []*models.Artifact   `json:"artifacts"`
	Messages   []*models.Message    `json:"messages"`
}

type sqlitePersistence struct {
	path string
	mu   sync.Mutex
	db   *sql.DB
}

func newSQLitePersistence(path string) statePersistence {
	return &sqlitePersistence{path: path}
}

func (p *sqlitePersistence) Load() (*persistedState, error) {
	if p == nil || p.path == "" {
		return &persistedState{}, nil
	}

	db, err := p.open()
	if err != nil {
		return nil, err
	}

	state := &persistedState{}

	counter, err := querySingleString(db, `SELECT value FROM metadata WHERE key = 'counter'`)
	if err != nil {
		return nil, err
	}
	version, err := querySingleString(db, `SELECT value FROM metadata WHERE key = 'schema_version'`)
	if err != nil {
		return nil, err
	}
	if version != persistenceSchemaVersion {
		hasData, err := persistedStateHasData(db)
		if err != nil {
			return nil, err
		}
		if hasData {
			if err := resetPersistedState(db); err != nil {
				return nil, err
			}
		}
		return state, nil
	}
	if counter != "" {
		if _, err := fmt.Sscanf(counter, "%d", &state.Counter); err != nil {
			return nil, err
		}
	}

	if state.Workspaces, err = queryJSONList[models.Workspace](db, `SELECT payload FROM workspaces ORDER BY last_accessed_at DESC, created_at DESC, id ASC`); err != nil {
		return nil, err
	}
	if state.Sessions, err = queryJSONList[models.Session](db, `SELECT payload FROM sessions ORDER BY last_accessed_at DESC, created_at DESC, id ASC`); err != nil {
		return nil, err
	}
	if state.Objects, err = queryJSONList[models.ObjectMeta](db, `SELECT payload FROM objects ORDER BY created_at ASC, id ASC`); err != nil {
		return nil, err
	}
	if state.Jobs, err = queryJSONList[models.Job](db, `SELECT payload FROM jobs ORDER BY created_at ASC, id ASC`); err != nil {
		return nil, err
	}
	if state.Artifacts, err = queryJSONList[models.Artifact](db, `SELECT payload FROM artifacts ORDER BY created_at ASC, id ASC`); err != nil {
		return nil, err
	}
	if state.Messages, err = queryJSONList[models.Message](db, `SELECT payload FROM messages ORDER BY created_at ASC, id ASC`); err != nil {
		return nil, err
	}

	return state, nil
}

func (p *sqlitePersistence) Save(state *persistedState) error {
	if p == nil || p.path == "" || state == nil {
		return nil
	}

	db, err := p.open()
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM metadata WHERE key NOT IN ('counter', 'schema_version')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO metadata(key, value)
		VALUES('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, persistenceSchemaVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO metadata(key, value)
		VALUES('counter', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, fmt.Sprintf("%d", state.Counter)); err != nil {
		return err
	}

	workspaces, err := workspaceRows(state.Workspaces)
	if err != nil {
		return err
	}
	if err := syncPayloadTable(tx, "workspaces", []string{"id", "created_at", "last_accessed_at", "payload"}, workspaces); err != nil {
		return err
	}

	sessions, err := sessionRows(state.Sessions)
	if err != nil {
		return err
	}
	if err := syncPayloadTable(tx, "sessions", []string{"id", "workspace_id", "created_at", "last_accessed_at", "payload"}, sessions); err != nil {
		return err
	}

	objects, err := objectRows(state.Objects)
	if err != nil {
		return err
	}
	if err := syncPayloadTable(tx, "objects", []string{"id", "workspace_id", "session_id", "created_at", "payload"}, objects); err != nil {
		return err
	}

	jobs, err := jobRows(state.Jobs)
	if err != nil {
		return err
	}
	if err := syncPayloadTable(tx, "jobs", []string{"id", "session_id", "created_at", "payload"}, jobs); err != nil {
		return err
	}

	artifacts, err := artifactRows(state.Artifacts)
	if err != nil {
		return err
	}
	if err := syncPayloadTable(tx, "artifacts", []string{"id", "workspace_id", "session_id", "created_at", "payload"}, artifacts); err != nil {
		return err
	}

	messages, err := messageRows(state.Messages)
	if err != nil {
		return err
	}
	if err := syncPayloadTable(tx, "messages", []string{"id", "session_id", "created_at", "payload"}, messages); err != nil {
		return err
	}

	return tx.Commit()
}

func (p *sqlitePersistence) open() (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db != nil {
		return p.db, nil
	}

	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", p.path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			last_accessed_at TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			created_at TEXT NOT NULL,
			last_accessed_at TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS objects (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			session_id TEXT,
			created_at TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			session_id TEXT,
			created_at TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}

	p.db = db
	return p.db, nil
}

type payloadRow struct {
	id     string
	values []any
}

func workspaceRows(items []*models.Workspace) ([]payloadRow, error) {
	rows := make([]payloadRow, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		row, err := marshalPayloadRow(item.ID, item, item.CreatedAt, item.LastAccessedAt)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func sessionRows(items []*models.Session) ([]payloadRow, error) {
	rows := make([]payloadRow, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		row, err := marshalPayloadRow(item.ID, item, item.WorkspaceID, item.CreatedAt, item.LastAccessedAt)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func objectRows(items []*models.ObjectMeta) ([]payloadRow, error) {
	rows := make([]payloadRow, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		row, err := marshalPayloadRow(item.ID, item, item.WorkspaceID, item.SessionID, item.CreatedAt)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func jobRows(items []*models.Job) ([]payloadRow, error) {
	rows := make([]payloadRow, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		row, err := marshalPayloadRow(item.ID, item, item.SessionID, item.CreatedAt)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func artifactRows(items []*models.Artifact) ([]payloadRow, error) {
	rows := make([]payloadRow, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		row, err := marshalPayloadRow(item.ID, item, item.WorkspaceID, item.SessionID, item.CreatedAt)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func messageRows(items []*models.Message) ([]payloadRow, error) {
	rows := make([]payloadRow, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		row, err := marshalPayloadRow(item.ID, item, item.SessionID, item.CreatedAt)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func marshalPayloadRow(id string, payload any, values ...any) (payloadRow, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return payloadRow{}, err
	}

	rowValues := make([]any, 0, len(values)+2)
	rowValues = append(rowValues, id)
	for _, value := range values {
		switch typed := value.(type) {
		case time.Time:
			rowValues = append(rowValues, typed.UTC().Format(time.RFC3339Nano))
		default:
			rowValues = append(rowValues, typed)
		}
	}
	rowValues = append(rowValues, string(encoded))
	return payloadRow{id: id, values: rowValues}, nil
}

func persistedStateHasData(db *sql.DB) (bool, error) {
	for _, table := range []string{"workspaces", "sessions", "objects", "jobs", "artifacts", "messages"} {
		value, err := querySingleString(db, fmt.Sprintf(`SELECT COUNT(1) FROM %s`, table))
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "0" {
			return true, nil
		}
	}
	return false, nil
}

func resetPersistedState(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, statement := range []string{
		`DELETE FROM workspaces`,
		`DELETE FROM sessions`,
		`DELETE FROM objects`,
		`DELETE FROM jobs`,
		`DELETE FROM artifacts`,
		`DELETE FROM messages`,
		`DELETE FROM metadata`,
		`INSERT INTO metadata(key, value) VALUES('schema_version', '` + persistenceSchemaVersion + `')`,
		`INSERT INTO metadata(key, value) VALUES('counter', '0')`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func syncPayloadTable(tx *sql.Tx, table string, columns []string, rows []payloadRow) error {
	if len(columns) == 0 {
		return nil
	}

	assignments := make([]string, 0, max(len(columns)-1, 0))
	for _, column := range columns[1:] {
		assignments = append(assignments, fmt.Sprintf("%s = excluded.%s", column, column))
	}

	query := fmt.Sprintf(
		"INSERT INTO %s(%s) VALUES(%s) ON CONFLICT(id) DO UPDATE SET %s",
		table,
		strings.Join(columns, ", "),
		placeholders(len(columns)),
		strings.Join(assignments, ", "),
	)

	statement, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer statement.Close()

	keepIDs := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		keepIDs[row.id] = struct{}{}
		if _, err := statement.Exec(row.values...); err != nil {
			return err
		}
	}

	return deleteMissingRows(tx, table, keepIDs)
}

func deleteMissingRows(tx *sql.Tx, table string, keepIDs map[string]struct{}) error {
	rows, err := tx.Query(fmt.Sprintf("SELECT id FROM %s", table))
	if err != nil {
		return err
	}
	defer rows.Close()

	deleteStatement, err := tx.Prepare(fmt.Sprintf("DELETE FROM %s WHERE id = ?", table))
	if err != nil {
		return err
	}
	defer deleteStatement.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if _, ok := keepIDs[id]; ok {
			continue
		}
		if _, err := deleteStatement.Exec(id); err != nil {
			return err
		}
	}

	return rows.Err()
}

func placeholders(count int) string {
	values := make([]string, 0, count)
	for range count {
		values = append(values, "?")
	}
	return strings.Join(values, ", ")
}

func querySingleString(db *sql.DB, query string) (string, error) {
	var value string
	err := db.QueryRow(query).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func queryJSONList[T any](db *sql.DB, query string) ([]*T, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]*T, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}

		var item T
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		items = append(items, &item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
