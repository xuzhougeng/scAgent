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
	JobQueued     JobStatus = "queued"
	JobRunning    JobStatus = "running"
	JobSucceeded  JobStatus = "succeeded"
	JobIncomplete JobStatus = "incomplete"
	JobFailed     JobStatus = "failed"
	JobCanceled   JobStatus = "canceled"
)

type JobPhaseKind string

const (
	JobPhaseDecide      JobPhaseKind = "decide"
	JobPhaseInvestigate JobPhaseKind = "investigate"
	JobPhaseRespond     JobPhaseKind = "respond"
)

type JobPhaseStatus string

const (
	JobPhasePending   JobPhaseStatus = "pending"
	JobPhaseRunning   JobPhaseStatus = "running"
	JobPhaseCompleted JobPhaseStatus = "completed"
	JobPhaseSkipped   JobPhaseStatus = "skipped"
	JobPhaseFailed    JobPhaseStatus = "failed"
	JobPhaseCanceled  JobPhaseStatus = "canceled"
)

type ArtifactKind string

const (
	ArtifactPlot          ArtifactKind = "plot"
	ArtifactTable         ArtifactKind = "table"
	ArtifactObjectSummary ArtifactKind = "object_summary"
	ArtifactFile          ArtifactKind = "file"
)

type TurnStatus string

const (
	TurnPending   TurnStatus = "pending"
	TurnFulfilled TurnStatus = "fulfilled"
	TurnFailed    TurnStatus = "failed"
	TurnCanceled  TurnStatus = "canceled"
)

type TurnStrategy string

const (
	TurnStrategyAnswerText            TurnStrategy = "answer_text"
	TurnStrategyReuseExistingArtifact TurnStrategy = "reuse_existing_artifact"
	TurnStrategyExecute               TurnStrategy = "execute"
	TurnStrategyAskClarification      TurnStrategy = "ask_clarification"
)

type TurnDeliverableKind string

const (
	TurnDeliverableText    TurnDeliverableKind = "text"
	TurnDeliverablePlot    TurnDeliverableKind = "plot"
	TurnDeliverableFile    TurnDeliverableKind = "file"
	TurnDeliverableTable   TurnDeliverableKind = "table"
	TurnDeliverableObject  TurnDeliverableKind = "object"
	TurnDeliverableUnknown TurnDeliverableKind = "unknown"
)

type TurnReusePolicy string

const (
	TurnReusePreferExisting TurnReusePolicy = "prefer_existing"
	TurnReuseRequireNew     TurnReusePolicy = "require_new"
	TurnReuseNoReuse        TurnReusePolicy = "no_reuse"
)

type TurnResultRefKind string

const (
	TurnResultArtifact TurnResultRefKind = "artifact"
	TurnResultObject   TurnResultRefKind = "object"
	TurnResultText     TurnResultRefKind = "text"
)

type TurnCompletionCriterionKind string

const (
	TurnCompletionAnyResult    TurnCompletionCriterionKind = "any_result"
	TurnCompletionTextAnswer   TurnCompletionCriterionKind = "text_answer"
	TurnCompletionArtifactKind TurnCompletionCriterionKind = "artifact_kind"
	TurnCompletionArtifactID   TurnCompletionCriterionKind = "artifact_id"
	TurnCompletionObjectID     TurnCompletionCriterionKind = "object_id"
)

type MessageRole string

const (
	MessageUser      MessageRole = "user"
	MessageAssistant MessageRole = "assistant"
	MessageSystem    MessageRole = "system"
)

type Workspace struct {
	ID             string    `json:"id"`
	Label          string    `json:"label"`
	DatasetID      string    `json:"dataset_id"`
	FocusObjectID  string    `json:"focus_object_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastAccessedAt time.Time `json:"last_accessed_at"`
}

type Session struct {
	ID             string        `json:"id"`
	WorkspaceID    string        `json:"workspace_id,omitempty"`
	Label          string        `json:"label"`
	DatasetID      string        `json:"dataset_id"`
	FocusObjectID  string        `json:"focus_object_id"`
	Status         SessionStatus `json:"status"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	LastAccessedAt time.Time     `json:"last_accessed_at"`
}

type ObjectMeta struct {
	ID               string         `json:"id"`
	WorkspaceID      string         `json:"workspace_id,omitempty"`
	SessionID        string         `json:"session_id,omitempty"`
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
	WorkspaceID string       `json:"workspace_id,omitempty"`
	SessionID   string       `json:"session_id,omitempty"`
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

type TurnContract struct {
	Intent             string                    `json:"intent,omitempty"`
	DeliverableKind    TurnDeliverableKind       `json:"deliverable_kind,omitempty"`
	TargetObjectID     string                    `json:"target_object_id,omitempty"`
	FollowUpTurnID     string                    `json:"follow_up_turn_id,omitempty"`
	FollowUpArtifactID string                    `json:"follow_up_artifact_id,omitempty"`
	ReusePolicy        TurnReusePolicy           `json:"reuse_policy,omitempty"`
	CompletionCriteria []TurnCompletionCriterion `json:"completion_criteria,omitempty"`
}

type TurnCompletionCriterion struct {
	Kind         TurnCompletionCriterionKind `json:"kind"`
	ArtifactKind ArtifactKind                `json:"artifact_kind,omitempty"`
	ArtifactID   string                      `json:"artifact_id,omitempty"`
	ObjectID     string                      `json:"object_id,omitempty"`
}

type TurnResultRef struct {
	Kind         TurnResultRefKind `json:"kind"`
	ArtifactID   string            `json:"artifact_id,omitempty"`
	ArtifactKind ArtifactKind      `json:"artifact_kind,omitempty"`
	ObjectID     string            `json:"object_id,omitempty"`
	Text         string            `json:"text,omitempty"`
}

type Turn struct {
	ID                 string          `json:"id"`
	SessionID          string          `json:"session_id"`
	UserMessageID      string          `json:"user_message_id"`
	AssistantMessageID string          `json:"assistant_message_id,omitempty"`
	JobID              string          `json:"job_id,omitempty"`
	Status             TurnStatus      `json:"status"`
	Strategy           TurnStrategy    `json:"strategy,omitempty"`
	Summary            string          `json:"summary,omitempty"`
	Contract           TurnContract    `json:"contract,omitempty"`
	ResultRefs         []TurnResultRef `json:"result_refs,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
}

type Message struct {
	ID        string      `json:"id"`
	SessionID string      `json:"session_id"`
	TurnID    string      `json:"turn_id,omitempty"`
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
	MemoryRefs     []string       `json:"memory_refs,omitempty"`
}

type JobStep struct {
	ID                     string         `json:"id"`
	Skill                  string         `json:"skill"`
	TargetObjectID         string         `json:"target_object_id,omitempty"`
	Params                 map[string]any `json:"params,omitempty"`
	ResolvedTargetObjectID string         `json:"resolved_target_object_id,omitempty"`
	Status                 JobStatus      `json:"status"`
	Summary                string         `json:"summary,omitempty"`
	OutputObjectID         string         `json:"output_object_id,omitempty"`
	ArtifactIDs            []string       `json:"artifact_ids,omitempty"`
	Facts                  map[string]any `json:"facts,omitempty"`
	Metadata               map[string]any `json:"metadata,omitempty"`
	StartedAt              time.Time      `json:"started_at"`
	FinishedAt             *time.Time     `json:"finished_at,omitempty"`
}

type JobCheckpoint struct {
	Kind      string         `json:"kind,omitempty"`
	Tone      string         `json:"tone,omitempty"`
	Title     string         `json:"title"`
	Label     string         `json:"label,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type JobPhase struct {
	Kind       JobPhaseKind   `json:"kind"`
	Title      string         `json:"title"`
	Status     JobPhaseStatus `json:"status"`
	Summary    string         `json:"summary,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

type Job struct {
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspace_id,omitempty"`
	SessionID    string          `json:"session_id"`
	TurnID       string          `json:"turn_id,omitempty"`
	MessageID    string          `json:"message_id"`
	Status       JobStatus       `json:"status"`
	CurrentPhase JobPhaseKind    `json:"current_phase,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Plan         *Plan           `json:"plan,omitempty"`
	Steps        []JobStep       `json:"steps,omitempty"`
	Phases       []JobPhase      `json:"phases,omitempty"`
	Checkpoints  []JobCheckpoint `json:"checkpoints,omitempty"`
	Error        string          `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
}

type WorkingMemory struct {
	Focus                *WorkingMemoryFocus        `json:"focus,omitempty"`
	RecentArtifacts      []WorkingMemoryArtifactRef `json:"recent_artifacts,omitempty"`
	ConfirmedPreferences []WorkingMemoryPreference  `json:"confirmed_preferences,omitempty"`
	SemanticStateChanges []WorkingMemoryStateChange `json:"semantic_state_changes,omitempty"`
	UpdatedAt            time.Time                  `json:"updated_at"`
}

type WorkingMemoryFocus struct {
	FocusObjectID         string `json:"focus_object_id,omitempty"`
	FocusObjectLabel      string `json:"focus_object_label,omitempty"`
	LastOutputObjectID    string `json:"last_output_object_id,omitempty"`
	LastOutputObjectLabel string `json:"last_output_object_label,omitempty"`
	LastArtifactID        string `json:"last_artifact_id,omitempty"`
	LastArtifactTitle     string `json:"last_artifact_title,omitempty"`
}

type WorkingMemoryArtifactRef struct {
	ID        string       `json:"id"`
	Kind      ArtifactKind `json:"kind"`
	ObjectID  string       `json:"object_id,omitempty"`
	JobID     string       `json:"job_id,omitempty"`
	Title     string       `json:"title"`
	Summary   string       `json:"summary,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

type WorkingMemoryPreference struct {
	Skill        string    `json:"skill"`
	Param        string    `json:"param"`
	Value        any       `json:"value,omitempty"`
	SourceJobID  string    `json:"source_job_id,omitempty"`
	SourceStepID string    `json:"source_step_id,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type WorkingMemoryStateChange struct {
	Kind          string    `json:"kind"`
	Skill         string    `json:"skill,omitempty"`
	JobID         string    `json:"job_id,omitempty"`
	StepID        string    `json:"step_id,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	ObjectID      string    `json:"object_id,omitempty"`
	ObjectLabel   string    `json:"object_label,omitempty"`
	ArtifactID    string    `json:"artifact_id,omitempty"`
	ArtifactTitle string    `json:"artifact_title,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type SessionSnapshot struct {
	Session       *Session       `json:"session"`
	Workspace     *Workspace     `json:"workspace,omitempty"`
	Objects       []*ObjectMeta  `json:"objects"`
	Jobs          []*Job         `json:"jobs"`
	Artifacts     []*Artifact    `json:"artifacts"`
	Turns         []*Turn        `json:"turns"`
	Messages      []*Message     `json:"messages"`
	WorkingMemory *WorkingMemory `json:"working_memory,omitempty"`
}

type WorkspaceSnapshot struct {
	Workspace     *Workspace    `json:"workspace"`
	Conversations []*Session    `json:"conversations"`
	Objects       []*ObjectMeta `json:"objects"`
	Artifacts     []*Artifact   `json:"artifacts"`
}

type WorkspaceList struct {
	Workspaces []*Workspace `json:"workspaces"`
}

type Event struct {
	Type        string    `json:"type"`
	SessionID   string    `json:"session_id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Data        any       `json:"data"`
}
