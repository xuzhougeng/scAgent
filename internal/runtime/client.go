package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"scagent/internal/models"
)

// ---------- locale helpers ----------

type localeKeyType struct{}

var localeKey = localeKeyType{}

// ContextWithLocale returns a child context carrying the given locale string.
func ContextWithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey, locale)
}

// LocaleFromContext extracts the locale stored by ContextWithLocale, defaulting
// to "zh" when none is set.
func LocaleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(localeKey).(string); ok && v != "" {
		return v
	}
	return "zh"
}

type Service interface {
	Health(ctx context.Context) error
	Status(ctx context.Context) (*HealthStatus, error)
	InitSession(ctx context.Context, payload InitSessionRequest) (*InitSessionResponse, error)
	LoadFile(ctx context.Context, payload LoadFileRequest) (*LoadFileResponse, error)
	EnsureObject(ctx context.Context, payload EnsureObjectRequest) (*EnsureObjectResponse, error)
	Execute(ctx context.Context, payload ExecuteRequest) (*ExecuteResponse, error)
	CancelExecution(ctx context.Context, payload CancelExecutionRequest) (*CancelExecutionResponse, error)
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{},
	}
}

type InitSessionRequest struct {
	SessionID     string `json:"session_id"`
	DatasetID     string `json:"dataset_id"`
	Label         string `json:"label"`
	WorkspaceRoot string `json:"workspace_root"`
}

type ObjectDescriptor struct {
	BackendRef       string             `json:"backend_ref"`
	Kind             models.ObjectKind  `json:"kind"`
	Label            string             `json:"label"`
	NObs             int                `json:"n_obs"`
	NVars            int                `json:"n_vars"`
	State            models.ObjectState `json:"state"`
	InMemory         bool               `json:"in_memory"`
	MaterializedPath string             `json:"materialized_path,omitempty"`
	Metadata         map[string]any     `json:"metadata,omitempty"`
}

type ArtifactDescriptor struct {
	Kind        models.ArtifactKind `json:"kind"`
	Title       string              `json:"title"`
	Path        string              `json:"path"`
	ContentType string              `json:"content_type"`
	Summary     string              `json:"summary"`
}

type InitSessionResponse struct {
	Object  ObjectDescriptor `json:"object"`
	Summary string           `json:"summary"`
}

type LoadFileRequest struct {
	SessionID string `json:"session_id"`
	FilePath  string `json:"file_path"`
	Label     string `json:"label"`
}

type LoadFileResponse struct {
	Object  ObjectDescriptor `json:"object"`
	Summary string           `json:"summary"`
}

type EnsureObjectRequest struct {
	SessionID string           `json:"session_id"`
	Object    ObjectDescriptor `json:"object"`
}

type EnsureObjectResponse struct {
	Object  ObjectDescriptor `json:"object"`
	Summary string           `json:"summary"`
}

type ExecuteRequest struct {
	SessionID        string         `json:"session_id"`
	RequestID        string         `json:"request_id"`
	Skill            string         `json:"skill"`
	TargetBackendRef string         `json:"target_backend_ref,omitempty"`
	Params           map[string]any `json:"params,omitempty"`
	WorkspaceRoot    string         `json:"workspace_root"`
}

type ExecuteResponse struct {
	Summary    string               `json:"summary"`
	Object     *ObjectDescriptor    `json:"object,omitempty"`
	Artifacts  []ArtifactDescriptor `json:"artifacts,omitempty"`
	Facts      map[string]any       `json:"facts,omitempty"`
	Metadata   map[string]any       `json:"metadata,omitempty"`
	ActiveHint string               `json:"active_hint,omitempty"`
}

type CancelExecutionRequest struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id,omitempty"`
}

type CancelExecutionResponse struct {
	Summary         string `json:"summary"`
	Stopped         bool   `json:"stopped"`
	Isolated        bool   `json:"isolated,omitempty"`
	ActiveRequestID string `json:"active_request_id,omitempty"`
	ActiveOperation string `json:"active_operation,omitempty"`
}

type EnvironmentCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type SampleH5ADSummary struct {
	Path      string   `json:"path"`
	NObs      int      `json:"n_obs"`
	NVars     int      `json:"n_vars"`
	ObsFields []string `json:"obs_fields,omitempty"`
	ObsmKeys  []string `json:"obsm_keys,omitempty"`
}

type HealthStatus struct {
	Status                string             `json:"status"`
	RuntimeMode           string             `json:"runtime_mode,omitempty"`
	RealH5ADInspection    bool               `json:"real_h5ad_inspection"`
	RealAnalysisExecution bool               `json:"real_analysis_execution"`
	ExecutableSkills      []string           `json:"executable_skills,omitempty"`
	Notes                 []string           `json:"notes,omitempty"`
	PythonVersion         string             `json:"python_version,omitempty"`
	EnvironmentChecks     []EnvironmentCheck `json:"environment_checks,omitempty"`
	SampleH5AD            *SampleH5ADSummary `json:"sample_h5ad,omitempty"`
}

func (c *Client) Health(ctx context.Context) error {
	_, err := c.Status(ctx)
	return err
}

func (c *Client) Status(ctx context.Context) (*HealthStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return nil, err
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("runtime health request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime health returned %s", response.Status)
	}

	var status HealthStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode runtime health response: %w", err)
	}
	return &status, nil
}

func (c *Client) InitSession(ctx context.Context, payload InitSessionRequest) (*InitSessionResponse, error) {
	var response InitSessionResponse
	if err := c.postJSON(ctx, "/sessions/init", payload, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Execute(ctx context.Context, payload ExecuteRequest) (*ExecuteResponse, error) {
	var response ExecuteResponse
	if err := c.postJSON(ctx, "/execute", payload, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) LoadFile(ctx context.Context, payload LoadFileRequest) (*LoadFileResponse, error) {
	var response LoadFileResponse
	if err := c.postJSON(ctx, "/sessions/load_file", payload, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) EnsureObject(ctx context.Context, payload EnsureObjectRequest) (*EnsureObjectResponse, error) {
	var response EnsureObjectResponse
	if err := c.postJSON(ctx, "/objects/ensure", payload, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) CancelExecution(ctx context.Context, payload CancelExecutionRequest) (*CancelExecutionResponse, error) {
	var response CancelExecutionResponse
	if err := c.postJSON(ctx, "/sessions/cancel_execution", payload, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal runtime request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create runtime request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if locale := LocaleFromContext(ctx); locale != "" {
		request.Header.Set("Accept-Language", locale)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("runtime %s request: %w", path, err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		if len(detail) > 0 {
			var payload struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(detail, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
				return fmt.Errorf("runtime %s returned %s: %s", path, response.Status, strings.TrimSpace(payload.Error))
			}
			if text := strings.TrimSpace(string(detail)); text != "" {
				return fmt.Errorf("runtime %s returned %s: %s", path, response.Status, text)
			}
		}
		return fmt.Errorf("runtime %s returned %s", path, response.Status)
	}

	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode runtime response: %w", err)
	}
	return nil
}
