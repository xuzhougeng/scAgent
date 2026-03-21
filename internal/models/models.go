package models

import "time"

type SessionStatus string

const (
	SessionActive    SessionStatus = "active"
	SessionSuspended SessionStatus = "suspended"
)

type ObjectKind string

const (
	ObjectRawDataset      ObjectKind = "raw_dataset"
	ObjectFilteredDataset ObjectKind = "filtered_dataset"
	ObjectSubset          ObjectKind = "subset"
	ObjectReclustered     ObjectKind = "reclustered_subset"
	ObjectDEResult        ObjectKind = "de_result"
	ObjectMarkerResult    ObjectKind = "marker_result"
	ObjectPlotArtifact    ObjectKind = "plot_artifact"
	ObjectObjectSummary   ObjectKind = "object_summary"
	ObjectUnknown         ObjectKind = "unknown"
)

type ObjectState string

const (
	ObjectResident     ObjectState = "resident"
	ObjectMaterialized ObjectState = "materialized"
	ObjectEvicted      ObjectState = "evicted"
	ObjectDeleted      ObjectState = "deleted"
)

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
)

type ArtifactKind string

const (
	ArtifactPlot          ArtifactKind = "plot"
	ArtifactTable         ArtifactKind = "table"
	ArtifactObjectSummary ArtifactKind = "object_summary"
	ArtifactFile          ArtifactKind = "file"
)

type MessageRole string

const (
	MessageUser      MessageRole = "user"
	MessageAssistant MessageRole = "assistant"
	MessageSystem    MessageRole = "system"
)

type Session struct {
	ID             string        `json:"id"`
	Label          string        `json:"label"`
	DatasetID      string        `json:"dataset_id"`
	ActiveObjectID string        `json:"active_object_id"`
	Status         SessionStatus `json:"status"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	LastAccessedAt time.Time     `json:"last_accessed_at"`
}

type ObjectMeta struct {
	ID               string         `json:"id"`
	SessionID        string         `json:"session_id"`
	DatasetID        string         `json:"dataset_id"`
	ParentID         string         `json:"parent_id,omitempty"`
	Kind             ObjectKind     `json:"kind"`
	Label            string         `json:"label"`
	BackendRef       string         `json:"backend_ref"`
	NObs             int            `json:"n_obs"`
	NVars            int            `json:"n_vars"`
	State            ObjectState    `json:"state"`
	InMemory         bool           `json:"in_memory"`
	Pinned           bool           `json:"pinned"`
	MaterializedPath string         `json:"materialized_path,omitempty"`
	MaterializedURL  string         `json:"materialized_url,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	LastAccessedAt   time.Time      `json:"last_accessed_at"`
}

type Artifact struct {
	ID          string       `json:"id"`
	SessionID   string       `json:"session_id"`
	ObjectID    string       `json:"object_id,omitempty"`
	JobID       string       `json:"job_id,omitempty"`
	Kind        ArtifactKind `json:"kind"`
	Title       string       `json:"title"`
	Path        string       `json:"path"`
	URL         string       `json:"url"`
	ContentType string       `json:"content_type"`
	Summary     string       `json:"summary"`
	CreatedAt   time.Time    `json:"created_at"`
}

type Message struct {
	ID        string      `json:"id"`
	SessionID string      `json:"session_id"`
	JobID     string      `json:"job_id,omitempty"`
	Role      MessageRole `json:"role"`
	Content   string      `json:"content"`
	CreatedAt time.Time   `json:"created_at"`
}

type Plan struct {
	Steps []PlanStep `json:"steps"`
}

type PlanStep struct {
	ID             string         `json:"id,omitempty"`
	Skill          string         `json:"skill"`
	TargetObjectID string         `json:"target_object_id,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
}

type JobStep struct {
	ID                     string         `json:"id"`
	Skill                  string         `json:"skill"`
	TargetObjectID         string         `json:"target_object_id,omitempty"`
	ResolvedTargetObjectID string         `json:"resolved_target_object_id,omitempty"`
	Status                 JobStatus      `json:"status"`
	Summary                string         `json:"summary,omitempty"`
	OutputObjectID         string         `json:"output_object_id,omitempty"`
	ArtifactIDs            []string       `json:"artifact_ids,omitempty"`
	Metadata               map[string]any `json:"metadata,omitempty"`
	StartedAt              time.Time      `json:"started_at"`
	FinishedAt             *time.Time     `json:"finished_at,omitempty"`
}

type Job struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	MessageID  string     `json:"message_id"`
	Status     JobStatus  `json:"status"`
	Summary    string     `json:"summary,omitempty"`
	Plan       *Plan      `json:"plan,omitempty"`
	Steps      []JobStep  `json:"steps,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type SessionSnapshot struct {
	Session   *Session      `json:"session"`
	Objects   []*ObjectMeta `json:"objects"`
	Jobs      []*Job        `json:"jobs"`
	Artifacts []*Artifact   `json:"artifacts"`
	Messages  []*Message    `json:"messages"`
}

type Event struct {
	Type      string    `json:"type"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}
