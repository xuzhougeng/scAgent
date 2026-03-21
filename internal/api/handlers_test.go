package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"scagent/internal/models"
	"scagent/internal/orchestrator"
	runtimeclient "scagent/internal/runtime"
	"scagent/internal/session"
	"scagent/internal/skill"
)

func TestFakePlanEndpoint(t *testing.T) {
	service := newTestService(t, orchestrator.NewFakePlanner(), &fakeRuntime{})
	handler := NewHandler(service, docsPath())
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/fake/plan", bytes.NewBufferString(`{"message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}`))
	request.Header.Set("Content-Type", "application/json")

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		PlannerMode string      `json:"planner_mode"`
		Plan        models.Plan `json:"plan"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response.PlannerMode != "fake" {
		t.Fatalf("expected fake mode, got %q", response.PlannerMode)
	}
	if len(response.Plan.Steps) != 3 {
		t.Fatalf("expected 3 plan steps, got %d", len(response.Plan.Steps))
	}
	if response.Plan.Steps[0].Skill != "subset_cells" {
		t.Fatalf("expected first skill subset_cells, got %q", response.Plan.Steps[0].Skill)
	}
}

func TestMessageExecutionFlow(t *testing.T) {
	service := newTestService(t, orchestrator.NewFakePlanner(), &fakeRuntime{})
	handler := NewHandler(service, docsPath())
	mux := http.NewServeMux()
	handler.Register(mux)

	createSessionRecorder := httptest.NewRecorder()
	createSessionRequest := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"label":"test session"}`))
	createSessionRequest.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(createSessionRecorder, createSessionRequest)

	if createSessionRecorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createSessionRecorder.Code, createSessionRecorder.Body.String())
	}

	var sessionSnapshot models.SessionSnapshot
	if err := json.Unmarshal(createSessionRecorder.Body.Bytes(), &sessionSnapshot); err != nil {
		t.Fatalf("decode session snapshot: %v", err)
	}
	sessionID := sessionSnapshot.Session.ID

	messageRecorder := httptest.NewRecorder()
	messageRequest := httptest.NewRequest(http.MethodPost, "/api/messages", bytes.NewBufferString(`{"session_id":"`+sessionID+`","message":"把 cortex 细胞拿出来重新聚类，然后画一下 marker"}`))
	messageRequest.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(messageRecorder, messageRequest)

	if messageRecorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", messageRecorder.Code, messageRecorder.Body.String())
	}

	var finalSnapshot models.SessionSnapshot
	var succeeded bool
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("expected 200 on snapshot read, got %d: %s", recorder.Code, recorder.Body.String())
		}

		if err := json.Unmarshal(recorder.Body.Bytes(), &finalSnapshot); err != nil {
			t.Fatalf("decode final snapshot: %v", err)
		}

		if len(finalSnapshot.Jobs) > 0 && finalSnapshot.Jobs[0].Status == models.JobSucceeded {
			succeeded = true
			break
		}
	}

	if !succeeded {
		t.Fatalf("job did not succeed: %+v", finalSnapshot.Jobs)
	}
	if len(finalSnapshot.Objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(finalSnapshot.Objects))
	}
	if len(finalSnapshot.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(finalSnapshot.Artifacts))
	}
	if finalSnapshot.Session.ActiveObjectID == sessionSnapshot.Session.ActiveObjectID {
		t.Fatalf("expected active object to advance after execution")
	}
	if finalSnapshot.Messages[len(finalSnapshot.Messages)-1].Role != models.MessageAssistant {
		t.Fatalf("expected final message to be assistant")
	}
}

func TestUploadH5ADFlow(t *testing.T) {
	service := newTestService(t, orchestrator.NewFakePlanner(), &fakeRuntime{})
	handler := NewHandler(service, docsPath())
	mux := http.NewServeMux()
	handler.Register(mux)

	createSessionRecorder := httptest.NewRecorder()
	createSessionRequest := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"label":"upload session"}`))
	createSessionRequest.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(createSessionRecorder, createSessionRequest)

	if createSessionRecorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createSessionRecorder.Code, createSessionRecorder.Body.String())
	}

	var sessionSnapshot models.SessionSnapshot
	if err := json.Unmarshal(createSessionRecorder.Body.Bytes(), &sessionSnapshot); err != nil {
		t.Fatalf("decode session snapshot: %v", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "uploaded_pbmc.h5ad")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.WriteString(part, "fake h5ad bytes"); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	uploadRecorder := httptest.NewRecorder()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionSnapshot.Session.ID+"/upload", body)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	mux.ServeHTTP(uploadRecorder, uploadRequest)

	if uploadRecorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	var response struct {
		Object   models.ObjectMeta      `json:"object"`
		Snapshot models.SessionSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(uploadRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	if response.Object.Label != "uploaded_pbmc" {
		t.Fatalf("unexpected upload label: %q", response.Object.Label)
	}
	if response.Object.MaterializedURL == "" {
		t.Fatalf("expected materialized URL for uploaded object")
	}
	if response.Snapshot.Session.ActiveObjectID != response.Object.ID {
		t.Fatalf("expected uploaded object to become active")
	}
}

func TestPlannerPreviewIncludesActiveObjectContext(t *testing.T) {
	service := newTestService(t, orchestrator.NewFakePlanner(), &fakeRuntime{})
	handler := NewHandler(service, docsPath())
	mux := http.NewServeMux()
	handler.Register(mux)

	createSessionRecorder := httptest.NewRecorder()
	createSessionRequest := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"label":"preview session"}`))
	createSessionRequest.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(createSessionRecorder, createSessionRequest)

	if createSessionRecorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createSessionRecorder.Code, createSessionRecorder.Body.String())
	}

	var sessionSnapshot models.SessionSnapshot
	if err := json.Unmarshal(createSessionRecorder.Body.Bytes(), &sessionSnapshot); err != nil {
		t.Fatalf("decode session snapshot: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionSnapshot.Session.ID+"/planner-preview", bytes.NewBufferString(`{"message":"inspect this uploaded h5ad"}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var preview struct {
		PlannerMode     string `json:"planner_mode"`
		PlanningRequest struct {
			Message      string `json:"message"`
			ActiveObject struct {
				Label    string         `json:"label"`
				NObs     int            `json:"n_obs"`
				NVars    int            `json:"n_vars"`
				Metadata map[string]any `json:"metadata"`
			} `json:"active_object"`
		} `json:"planning_request"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode planner preview: %v", err)
	}

	if preview.PlannerMode != "fake" {
		t.Fatalf("expected fake planner mode, got %q", preview.PlannerMode)
	}
	if preview.PlanningRequest.ActiveObject.Label != "pbmc3k" {
		t.Fatalf("unexpected active object label: %q", preview.PlanningRequest.ActiveObject.Label)
	}
	if preview.PlanningRequest.ActiveObject.NObs != 2638 || preview.PlanningRequest.ActiveObject.NVars != 1838 {
		t.Fatalf("unexpected active object shape: %+v", preview.PlanningRequest.ActiveObject)
	}
}

func TestDocsAPI(t *testing.T) {
	service := newTestService(t, orchestrator.NewFakePlanner(), &fakeRuntime{})
	handler := NewHandler(service, docsPath())
	mux := http.NewServeMux()
	handler.Register(mux)

	indexRecorder := httptest.NewRecorder()
	indexRequest := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	mux.ServeHTTP(indexRecorder, indexRequest)

	if indexRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", indexRecorder.Code, indexRecorder.Body.String())
	}

	var indexResponse struct {
		Docs []struct {
			Slug  string `json:"slug"`
			Title string `json:"title"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(indexRecorder.Body.Bytes(), &indexResponse); err != nil {
		t.Fatalf("decode docs index: %v", err)
	}
	if len(indexResponse.Docs) == 0 {
		t.Fatalf("expected at least one doc in index")
	}

	contentRecorder := httptest.NewRecorder()
	contentRequest := httptest.NewRequest(http.MethodGet, "/api/docs/protocol", nil)
	mux.ServeHTTP(contentRecorder, contentRequest)

	if contentRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", contentRecorder.Code, contentRecorder.Body.String())
	}

	var docResponse struct {
		Slug    string `json:"slug"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(contentRecorder.Body.Bytes(), &docResponse); err != nil {
		t.Fatalf("decode doc payload: %v", err)
	}
	if docResponse.Slug != "protocol" {
		t.Fatalf("unexpected doc slug: %q", docResponse.Slug)
	}
	if docResponse.Title != "Protocol" {
		t.Fatalf("unexpected doc title: %q", docResponse.Title)
	}
	if !strings.Contains(docResponse.Content, "POST /api/messages") {
		t.Fatalf("expected protocol doc content in payload")
	}
}

func TestStatusAPI(t *testing.T) {
	service := newTestService(t, orchestrator.NewFakePlanner(), &fakeRuntime{})
	handler := NewHandler(service, docsPath())
	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		SystemMode            string   `json:"system_mode"`
		PlannerMode           string   `json:"planner_mode"`
		LLMLoaded             bool     `json:"llm_loaded"`
		RuntimeConnected      bool     `json:"runtime_connected"`
		RealH5ADInspection    bool     `json:"real_h5ad_inspection"`
		RealAnalysisExecution bool     `json:"real_analysis_execution"`
		ExecutableSkills      []string `json:"executable_skills"`
		Runtime               struct {
			PythonVersion     string `json:"python_version"`
			EnvironmentChecks []struct {
				Name string `json:"name"`
				OK   bool   `json:"ok"`
			} `json:"environment_checks"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode status response: %v", err)
	}

	if response.SystemMode != "demo" {
		t.Fatalf("expected demo mode, got %q", response.SystemMode)
	}
	if response.PlannerMode != "fake" {
		t.Fatalf("expected fake planner mode, got %q", response.PlannerMode)
	}
	if response.LLMLoaded {
		t.Fatalf("expected llm_loaded=false")
	}
	if !response.RuntimeConnected || !response.RealH5ADInspection {
		t.Fatalf("expected runtime to be connected with real h5ad inspection")
	}
	if response.RealAnalysisExecution {
		t.Fatalf("expected mock analysis execution in test runtime")
	}
	if len(response.ExecutableSkills) == 0 {
		t.Fatalf("expected executable skills in status payload")
	}
	if response.Runtime.PythonVersion == "" {
		t.Fatalf("expected runtime python version in status payload")
	}
	if len(response.Runtime.EnvironmentChecks) == 0 {
		t.Fatalf("expected runtime environment checks in status payload")
	}
}

func newTestService(t *testing.T, planner orchestrator.Planner, runtimeService runtimeclient.Service) *orchestrator.Service {
	t.Helper()

	registry, err := skill.LoadRegistry(skillsRegistryPath())
	if err != nil {
		t.Fatalf("load skills registry: %v", err)
	}

	dataRoot := t.TempDir()
	store := session.NewStore()
	return orchestrator.NewService(store, registry, runtimeService, planner, dataRoot)
}

func docsPath() string {
	_, currentFile, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "docs"))
}

func skillsRegistryPath() string {
	_, currentFile, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "skills", "registry.json"))
}

type fakeRuntime struct{}

func (f *fakeRuntime) Health(context.Context) error {
	return nil
}

func (f *fakeRuntime) Status(context.Context) (*runtimeclient.HealthStatus, error) {
	return &runtimeclient.HealthStatus{
		Status:                "ok",
		RuntimeMode:           "hybrid_demo",
		RealH5ADInspection:    true,
		RealAnalysisExecution: false,
		PythonVersion:         "3.11.9",
		EnvironmentChecks: []runtimeclient.EnvironmentCheck{
			{Name: "numpy", OK: true, Detail: "1.26.4"},
			{Name: "scanpy", OK: false, Detail: "missing optional dependency"},
		},
		ExecutableSkills: []string{
			"inspect_dataset",
			"assess_dataset",
			"subset_cells",
			"recluster",
			"find_markers",
			"plot_umap",
			"plot_dotplot",
			"plot_violin",
			"export_h5ad",
		},
		Notes: []string{"Fake test runtime: real metadata inspection, mock execution."},
	}, nil
}

func (f *fakeRuntime) InitSession(_ context.Context, payload runtimeclient.InitSessionRequest) (*runtimeclient.InitSessionResponse, error) {
	return &runtimeclient.InitSessionResponse{
		Object: runtimeclient.ObjectDescriptor{
			BackendRef:       "py:" + payload.SessionID + ":adata_1",
			Kind:             models.ObjectRawDataset,
			Label:            "pbmc3k",
			NObs:             2638,
			NVars:            1838,
			State:            models.ObjectResident,
			InMemory:         true,
			MaterializedPath: filepath.Join(payload.SessionRoot, "objects", "pbmc3k.h5ad"),
			Metadata: map[string]any{
				"obs_fields": []string{"cell_type", "sample", "leiden"},
				"obsm_keys":  []string{"X_umap"},
			},
		},
		Summary: "Session bootstrapped from fake runtime.",
	}, nil
}

func (f *fakeRuntime) LoadFile(_ context.Context, payload runtimeclient.LoadFileRequest) (*runtimeclient.LoadFileResponse, error) {
	return &runtimeclient.LoadFileResponse{
		Object: runtimeclient.ObjectDescriptor{
			BackendRef:       "py:" + payload.SessionID + ":adata_upload",
			Kind:             models.ObjectRawDataset,
			Label:            payload.Label,
			NObs:             3000,
			NVars:            2000,
			State:            models.ObjectResident,
			InMemory:         true,
			MaterializedPath: payload.FilePath,
			Metadata: map[string]any{
				"obs_fields": []string{"batch", "cell_type"},
			},
		},
		Summary: "Uploaded " + filepath.Base(payload.FilePath) + " and registered it.",
	}, nil
}

func (f *fakeRuntime) Execute(_ context.Context, payload runtimeclient.ExecuteRequest) (*runtimeclient.ExecuteResponse, error) {
	switch payload.Skill {
	case "subset_cells":
		return &runtimeclient.ExecuteResponse{
			Summary: "Created subset_cortex from pbmc3k with 1160 cells.",
			Object: &runtimeclient.ObjectDescriptor{
				BackendRef:       "py:" + payload.SessionID + ":adata_2",
				Kind:             models.ObjectSubset,
				Label:            "subset_cortex",
				NObs:             1160,
				NVars:            1838,
				State:            models.ObjectResident,
				InMemory:         true,
				MaterializedPath: filepath.Join(payload.SessionRoot, "objects", "subset_cortex.h5ad"),
			},
		}, nil
	case "recluster":
		return &runtimeclient.ExecuteResponse{
			Summary: "Reclustered subset_cortex at resolution 0.6.",
			Object: &runtimeclient.ObjectDescriptor{
				BackendRef:       "py:" + payload.SessionID + ":adata_3",
				Kind:             models.ObjectReclustered,
				Label:            "reclustered_subset_cortex",
				NObs:             1160,
				NVars:            1838,
				State:            models.ObjectResident,
				InMemory:         true,
				MaterializedPath: filepath.Join(payload.SessionRoot, "objects", "reclustered_subset_cortex.h5ad"),
			},
		}, nil
	case "find_markers":
		return &runtimeclient.ExecuteResponse{
			Summary: "Marker table generated for reclustered_subset_cortex.",
			Artifacts: []runtimeclient.ArtifactDescriptor{
				{
					Kind:        models.ArtifactTable,
					Title:       "Markers for reclustered_subset_cortex",
					Path:        filepath.Join(payload.SessionRoot, "artifacts", "markers_reclustered_subset_cortex.csv"),
					ContentType: "text/csv",
					Summary:     "Top marker genes grouped by leiden cluster.",
				},
			},
			Metadata: map[string]any{"groupby": "leiden"},
		}, nil
	default:
		return &runtimeclient.ExecuteResponse{
			Summary: "No-op",
		}, nil
	}
}
