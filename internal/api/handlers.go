package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scagent/internal/orchestrator"
	"scagent/internal/skill"
)

type Handler struct {
	service *orchestrator.Service
	docsDir string
}

func NewHandler(service *orchestrator.Service, docsDir string) *Handler {
	return &Handler{service: service, docsDir: docsDir}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/docs", h.handleDocsIndex)
	mux.HandleFunc("/api/docs/", h.handleDocContent)
	mux.HandleFunc("/api/fake/plan", h.handleFakePlan)
	mux.HandleFunc("/api/skills", h.handleSkills)
	mux.HandleFunc("/api/plugins", h.handlePlugins)
	mux.HandleFunc("/api/plugins/", h.handlePluginRoutes)
	mux.HandleFunc("/api/workspaces", h.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", h.handleWorkspaceRoutes)
	mux.HandleFunc("/api/sessions", h.handleSessions)
	mux.HandleFunc("/api/sessions/", h.handleSessionRoutes)
	mux.HandleFunc("/api/messages", h.handleMessages)
	mux.HandleFunc("/api/jobs/", h.handleJobRoutes)
	mux.HandleFunc("/healthz", h.handleHealth)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, h.service.Status(r.Context()))
}

func (h *Handler) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":       h.service.Skills(),
		"planner_mode": h.service.PlannerMode(),
	})
}

func (h *Handler) handlePlugins(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		plugins, err := h.service.PluginBundles()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"plugins": filterUploadedBundles(plugins),
			"bundles": plugins,
			"skills":  h.service.Skills(),
		})
	case http.MethodPost:
		if err := r.ParseMultipartForm(256 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart plugin upload"})
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing plugin bundle file"})
			return
		}
		defer file.Close()

		pluginBundle, err := h.service.UploadPluginBundle(header.Filename, file)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"plugin":  pluginBundle,
			"bundles": bundlesPayload(h.service),
			"skills":  h.service.Skills(),
		})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) handlePluginRoutes(w http.ResponseWriter, r *http.Request) {
	bundleID := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" || strings.Contains(bundleID, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "未找到插件资源"})
		return
	}

	if r.Method != http.MethodPatch {
		writeMethodNotAllowed(w, http.MethodPatch)
		return
	}

	var payload struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plugin state payload"})
		return
	}
	if payload.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing enabled field"})
		return
	}

	bundle, err := h.service.SetPluginBundleEnabled(bundleID, *payload.Enabled)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bundle":  bundle,
		"bundles": bundlesPayload(h.service),
		"skills":  h.service.Skills(),
	})
}

func (h *Handler) handleFakePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var payload struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plan payload"})
		return
	}

	plan, err := h.service.PreviewFakePlan(r.Context(), payload.Message)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"planner_mode": "fake",
		"plan":         plan,
	})
}

func (h *Handler) handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	docs, err := h.listDocs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"docs": docs,
	})
}

func filterUploadedBundles(bundles []skill.PluginBundle) []skill.PluginBundle {
	out := make([]skill.PluginBundle, 0, len(bundles))
	for _, bundle := range bundles {
		if bundle.Builtin {
			continue
		}
		out = append(out, bundle)
	}
	return out
}

func bundlesPayload(service *orchestrator.Service) []skill.PluginBundle {
	bundles, err := service.PluginBundles()
	if err != nil {
		return nil
	}
	return bundles
}

func (h *Handler) handleDocContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	slug := strings.TrimPrefix(r.URL.Path, "/api/docs/")
	slug = strings.Trim(slug, "/")
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing doc slug"})
		return
	}

	doc, err := h.readDoc(slug)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, doc)
}

func (h *Handler) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var payload struct {
		Label      string `json:"label"`
		WithSample *bool  `json:"with_sample,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session payload"})
		return
	}
	withSample := payload.WithSample == nil || *payload.WithSample

	snapshot, err := h.service.CreateSession(r.Context(), payload.Label, withSample)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, snapshot)
}

func (h *Handler) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.service.ListWorkspaces())
	case http.MethodPost:
		var payload struct {
			Label      string `json:"label"`
			WithSample *bool  `json:"with_sample,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace payload"})
			return
		}
		withSample := payload.WithSample == nil || *payload.WithSample

		snapshot, err := h.service.CreateSession(r.Context(), payload.Label, withSample)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, snapshot)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) handleWorkspaceRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	if strings.HasSuffix(path, "/conversations") {
		workspaceID := strings.TrimSuffix(path, "/conversations")
		workspaceID = strings.TrimSuffix(workspaceID, "/")
		h.handleCreateConversation(w, r, workspaceID)
		return
	}

	workspaceID := strings.Trim(path, "/")
	switch r.Method {
	case http.MethodGet:
		workspaceSnapshot, err := h.service.GetWorkspaceSnapshot(workspaceID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, workspaceSnapshot)
	case http.MethodPatch:
		h.handleRenameWorkspace(w, r, workspaceID)
	case http.MethodDelete:
		if err := h.service.DeleteWorkspace(r.Context(), workspaceID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPatch+", "+http.MethodDelete)
	}
}

func (h *Handler) handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if strings.HasSuffix(path, "/events") {
		sessionID := strings.TrimSuffix(path, "/events")
		sessionID = strings.TrimSuffix(sessionID, "/")
		h.handleEvents(w, r, sessionID)
		return
	}
	if strings.HasSuffix(path, "/planner-preview") {
		sessionID := strings.TrimSuffix(path, "/planner-preview")
		sessionID = strings.TrimSuffix(sessionID, "/")
		h.handlePlannerPreview(w, r, sessionID)
		return
	}
	if strings.HasSuffix(path, "/upload") {
		sessionID := strings.TrimSuffix(path, "/upload")
		sessionID = strings.TrimSuffix(sessionID, "/")
		h.handleUpload(w, r, sessionID)
		return
	}

	sessionID := strings.Trim(path, "/")
	switch r.Method {
	case http.MethodGet:
		snapshot, err := h.service.GetSnapshot(sessionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	case http.MethodPatch:
		h.handleRenameConversation(w, r, sessionID)
	case http.MethodDelete:
		if err := h.service.DeleteConversation(r.Context(), sessionID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPatch+", "+http.MethodDelete)
	}
}

func (h *Handler) handleRenameWorkspace(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var payload struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	label := strings.TrimSpace(payload.Label)
	if label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label must not be empty"})
		return
	}
	snapshot, err := h.service.RenameWorkspace(r.Context(), workspaceID, label)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handleRenameConversation(w http.ResponseWriter, r *http.Request, sessionID string) {
	var payload struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	label := strings.TrimSpace(payload.Label)
	if label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label must not be empty"})
		return
	}
	snapshot, err := h.service.RenameConversation(r.Context(), sessionID, label)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handleCreateConversation(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var payload struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid conversation payload"})
		return
	}

	snapshot, err := h.service.CreateConversation(r.Context(), workspaceID, payload.Label)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, snapshot)
}

func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	events, cancel := h.service.Subscribe(sessionID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fmt.Fprint(w, ": stream opened\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\n", event.Type)
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var payload struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message payload"})
		return
	}

	job, snapshot, err := h.service.SubmitMessage(r.Context(), payload.SessionID, payload.Message)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusAccepted
	if job == nil {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"job":      job,
		"snapshot": snapshot,
	})
}

func (h *Handler) handleJobRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	if strings.HasSuffix(path, "/retry") {
		jobID := strings.TrimSuffix(path, "/retry")
		jobID = strings.TrimSuffix(jobID, "/")
		h.handleRetryJob(w, r, jobID)
		return
	}
	if strings.HasSuffix(path, "/cancel") {
		jobID := strings.TrimSuffix(path, "/cancel")
		jobID = strings.TrimSuffix(jobID, "/")
		h.handleCancelJob(w, r, jobID)
		return
	}
	if strings.HasSuffix(path, "/regenerate") {
		jobID := strings.TrimSuffix(path, "/regenerate")
		jobID = strings.TrimSuffix(jobID, "/")
		h.handleRegenerateResponse(w, r, jobID)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown job route"})
}

func (h *Handler) handleRetryJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var payload struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid retry payload"})
		return
	}

	job, snapshot, err := h.service.RetryJob(r.Context(), jobID, payload.Message)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusAccepted
	if job == nil {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"job":      job,
		"snapshot": snapshot,
	})
}

func (h *Handler) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	job, snapshot, err := h.service.CancelJob(r.Context(), jobID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusAccepted
	if job != nil && job.Status == "canceled" {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"job":      job,
		"snapshot": snapshot,
	})
}

func (h *Handler) handleRegenerateResponse(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	job, snapshot, err := h.service.RegenerateResponse(r.Context(), jobID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusAccepted
	if job == nil {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"job":      job,
		"snapshot": snapshot,
	})
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart upload"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing upload file"})
		return
	}
	defer file.Close()

	objectRecord, snapshot, err := h.service.UploadH5AD(r.Context(), sessionID, header.Filename, file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"object":   objectRecord,
		"snapshot": snapshot,
	})
}

func (h *Handler) handlePlannerPreview(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var payload struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid planner preview payload"})
		return
	}

	preview, err := h.service.PreviewPlannerDebug(r.Context(), sessionID, payload.Message)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, preview)
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type docDescriptor struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

type docPayload struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (h *Handler) listDocs() ([]docDescriptor, error) {
	if strings.TrimSpace(h.docsDir) == "" {
		return []docDescriptor{}, nil
	}

	var docs []docDescriptor
	err := filepath.Walk(h.docsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		relativePath, err := filepath.Rel(h.docsDir, path)
		if err != nil {
			return err
		}
		slug := strings.TrimSuffix(filepath.ToSlash(relativePath), ".md")
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		docs = append(docs, docDescriptor{
			Slug:  slug,
			Title: extractDocTitle(string(content), filepath.Base(slug)),
			Path:  filepath.ToSlash(relativePath),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Slug < docs[j].Slug
	})
	return docs, nil
}

func (h *Handler) readDoc(slug string) (*docPayload, error) {
	if strings.TrimSpace(h.docsDir) == "" {
		return nil, fmt.Errorf("docs are not configured")
	}

	cleanSlug := filepath.ToSlash(filepath.Clean(slug))
	cleanSlug = strings.TrimPrefix(cleanSlug, "/")
	if cleanSlug == "." || cleanSlug == "" || strings.HasPrefix(cleanSlug, "../") {
		return nil, fmt.Errorf("invalid doc slug")
	}

	path := filepath.Join(h.docsDir, filepath.FromSlash(cleanSlug)+".md")
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("doc %q not found", slug)
		}
		return nil, err
	}

	relativePath, _ := filepath.Rel(h.docsDir, path)
	return &docPayload{
		Slug:    cleanSlug,
		Title:   extractDocTitle(string(content), filepath.Base(cleanSlug)),
		Path:    filepath.ToSlash(relativePath),
		Content: string(content),
	}, nil
}

func extractDocTitle(content, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return fallback
}
