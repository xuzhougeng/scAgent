package session

import (
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"scagent/internal/models"
)

type Store struct {
	mu          sync.RWMutex
	counter     atomic.Uint64
	persistence statePersistence
	workspaces  map[string]*models.Workspace
	sessions    map[string]*models.Session
	objects     map[string]*models.ObjectMeta
	jobs        map[string]*models.Job
	artifacts   map[string]*models.Artifact
	messages    map[string][]*models.Message
	subscribers map[string]map[chan models.Event]struct{}
}

func NewStore() *Store {
	store, err := newStore(nil)
	if err != nil {
		return &Store{
			workspaces:  make(map[string]*models.Workspace),
			sessions:    make(map[string]*models.Session),
			objects:     make(map[string]*models.ObjectMeta),
			jobs:        make(map[string]*models.Job),
			artifacts:   make(map[string]*models.Artifact),
			messages:    make(map[string][]*models.Message),
			subscribers: make(map[string]map[chan models.Event]struct{}),
		}
	}
	return store
}

func NewPersistentStore(path string) (*Store, error) {
	return newStore(newSQLitePersistence(path))
}

func newStore(persistence statePersistence) (*Store, error) {
	store := &Store{
		persistence: persistence,
		workspaces:  make(map[string]*models.Workspace),
		sessions:    make(map[string]*models.Session),
		objects:     make(map[string]*models.ObjectMeta),
		jobs:        make(map[string]*models.Job),
		artifacts:   make(map[string]*models.Artifact),
		messages:    make(map[string][]*models.Message),
		subscribers: make(map[string]map[chan models.Event]struct{}),
	}
	if persistence == nil {
		return store, nil
	}

	state, err := persistence.Load()
	if err != nil {
		return nil, err
	}
	store.restore(state)
	return store, nil
}

func (s *Store) NextID(prefix string) string {
	return fmt.Sprintf("%s_%06d", prefix, s.counter.Add(1))
}

func (s *Store) CreateWorkspace(label string) *models.Workspace {
	now := time.Now().UTC()
	workspace := &models.Workspace{
		ID:             s.NextID("ws"),
		Label:          label,
		DatasetID:      s.NextID("ds"),
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaces[workspace.ID] = cloneWorkspace(workspace)
	s.persistLocked()
	return cloneWorkspace(workspace)
}

func (s *Store) SaveWorkspace(workspace *models.Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaces[workspace.ID] = cloneWorkspace(workspace)
	s.persistLocked()
}

func (s *Store) GetWorkspace(id string) (*models.Workspace, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspace, ok := s.workspaces[id]
	if !ok {
		return nil, false
	}
	return cloneWorkspace(workspace), true
}

func (s *Store) CreateSession(label string) *models.Session {
	workspace := s.CreateWorkspace(label)
	session, err := s.CreateConversation(workspace.ID, label)
	if err != nil {
		return nil
	}
	return session
}

func (s *Store) CreateConversation(workspaceID, label string) (*models.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace, ok := s.workspaces[workspaceID]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found", workspaceID)
	}

	now := time.Now().UTC()
	session := &models.Session{
		ID:             s.NextID("sess"),
		WorkspaceID:    workspaceID,
		Label:          label,
		DatasetID:      workspace.DatasetID,
		ActiveObjectID: workspace.ActiveObjectID,
		Status:         models.SessionActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}

	s.sessions[session.ID] = cloneSession(session)
	s.persistLocked()
	return cloneSession(session), nil
}

func (s *Store) SaveSession(session *models.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = cloneSession(session)
	s.persistLocked()
}

func (s *Store) GetSession(id string) (*models.Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	return cloneSession(session), true
}

func (s *Store) SaveObject(object *models.ObjectMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[object.ID] = cloneObject(object)
	s.persistLocked()
}

func (s *Store) GetObject(id string) (*models.ObjectMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[id]
	if !ok {
		return nil, false
	}
	return cloneObject(object), true
}

func (s *Store) SaveJob(job *models.Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = cloneJob(job)
	s.persistLocked()
}

func (s *Store) GetJob(id string) (*models.Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	return cloneJob(job), true
}

func (s *Store) ListSessionJobs(sessionID string) []*models.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]*models.Job, 0)
	for _, job := range s.jobs {
		if job.SessionID == sessionID {
			jobs = append(jobs, cloneJob(job))
		}
	}
	slices.SortFunc(jobs, func(a, b *models.Job) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return jobs
}

func (s *Store) SaveArtifact(artifact *models.Artifact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts[artifact.ID] = cloneArtifact(artifact)
	s.persistLocked()
}

func (s *Store) AddMessage(message *models.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[message.SessionID] = append(s.messages[message.SessionID], cloneMessage(message))
	s.persistLocked()
}

func (s *Store) GetMessage(sessionID, messageID string) (*models.Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, msg := range s.messages[sessionID] {
		if msg.ID == messageID {
			return cloneMessage(msg), true
		}
	}
	return nil, false
}

func (s *Store) DeleteMessage(sessionID, messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := s.messages[sessionID]
	filtered := make([]*models.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.ID != messageID {
			filtered = append(filtered, msg)
		}
	}
	s.messages[sessionID] = filtered
	s.persistLocked()
}

func (s *Store) DeleteMessagesByJobID(sessionID, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := s.messages[sessionID]
	filtered := make([]*models.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.JobID != jobID {
			filtered = append(filtered, msg)
		}
	}
	s.messages[sessionID] = filtered
	s.persistLocked()
}

func (s *Store) DeleteJob(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, jobID)
	s.persistLocked()
}

func (s *Store) Snapshot(sessionID string) (*models.SessionSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}

	workspace, ok := s.workspaces[session.WorkspaceID]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found", session.WorkspaceID)
	}

	sessionCopy := cloneSession(session)
	workspaceCopy := cloneWorkspace(workspace)
	sessionCopy.DatasetID = workspaceCopy.DatasetID
	if sessionCopy.ActiveObjectID == "" {
		sessionCopy.ActiveObjectID = workspaceCopy.ActiveObjectID
	}

	objects := make([]*models.ObjectMeta, 0)
	for _, object := range s.objects {
		if objectInWorkspace(object, workspaceCopy.ID, sessionID) {
			objects = append(objects, cloneObject(object))
		}
	}
	slices.SortFunc(objects, func(a, b *models.ObjectMeta) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	jobs := make([]*models.Job, 0)
	for _, job := range s.jobs {
		if job.SessionID == sessionID {
			jobs = append(jobs, cloneJob(job))
		}
	}
	slices.SortFunc(jobs, func(a, b *models.Job) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	artifacts := make([]*models.Artifact, 0)
	for _, artifact := range s.artifacts {
		if artifactInWorkspace(artifact, workspaceCopy.ID, sessionID) {
			artifacts = append(artifacts, cloneArtifact(artifact))
		}
	}
	slices.SortFunc(artifacts, func(a, b *models.Artifact) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	messages := make([]*models.Message, 0, len(s.messages[sessionID]))
	for _, message := range s.messages[sessionID] {
		messages = append(messages, cloneMessage(message))
	}

	return &models.SessionSnapshot{
		Session:       sessionCopy,
		Workspace:     workspaceCopy,
		Objects:       objects,
		Jobs:          jobs,
		Artifacts:     artifacts,
		Messages:      messages,
		WorkingMemory: buildWorkingMemory(sessionCopy, objects, jobs, artifacts),
	}, nil
}

func (s *Store) WorkspaceSnapshot(workspaceID string) (*models.WorkspaceSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, ok := s.workspaces[workspaceID]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found", workspaceID)
	}

	conversations := make([]*models.Session, 0)
	for _, session := range s.sessions {
		if session.WorkspaceID == workspaceID {
			sessionCopy := cloneSession(session)
			sessionCopy.DatasetID = workspace.DatasetID
			conversations = append(conversations, sessionCopy)
		}
	}
	slices.SortFunc(conversations, func(a, b *models.Session) int {
		return compareTimes(a.LastAccessedAt, b.LastAccessedAt, a.CreatedAt, b.CreatedAt)
	})

	objects := make([]*models.ObjectMeta, 0)
	for _, object := range s.objects {
		if object.WorkspaceID == workspaceID {
			objects = append(objects, cloneObject(object))
		}
	}
	slices.SortFunc(objects, func(a, b *models.ObjectMeta) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	artifacts := make([]*models.Artifact, 0)
	for _, artifact := range s.artifacts {
		if artifact.WorkspaceID == workspaceID {
			artifacts = append(artifacts, cloneArtifact(artifact))
		}
	}
	slices.SortFunc(artifacts, func(a, b *models.Artifact) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	return &models.WorkspaceSnapshot{
		Workspace:     cloneWorkspace(workspace),
		Conversations: conversations,
		Objects:       objects,
		Artifacts:     artifacts,
	}, nil
}

func (s *Store) ListWorkspaces() []*models.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspaces := make([]*models.Workspace, 0, len(s.workspaces))
	for _, workspace := range s.workspaces {
		workspaces = append(workspaces, cloneWorkspace(workspace))
	}
	slices.SortFunc(workspaces, func(a, b *models.Workspace) int {
		return compareTimes(a.LastAccessedAt, b.LastAccessedAt, a.CreatedAt, b.CreatedAt)
	})
	return workspaces
}

func (s *Store) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}

	conversationCount := 0
	for _, candidate := range s.sessions {
		if candidate.WorkspaceID == session.WorkspaceID {
			conversationCount++
		}
	}
	if conversationCount <= 1 {
		return fmt.Errorf("当前 workspace 只剩最后一个对话，请直接删除 workspace")
	}

	delete(s.sessions, sessionID)
	delete(s.messages, sessionID)
	for id, job := range s.jobs {
		if job.SessionID == sessionID {
			delete(s.jobs, id)
		}
	}
	for id, object := range s.objects {
		if object.SessionID == sessionID && object.WorkspaceID == "" {
			delete(s.objects, id)
		}
	}
	for id, artifact := range s.artifacts {
		if artifact.SessionID == sessionID && artifact.WorkspaceID == "" {
			delete(s.artifacts, id)
		}
	}
	s.closeSubscribersLocked(sessionID)
	s.persistLocked()
	return nil
}

func (s *Store) DeleteWorkspace(workspaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[workspaceID]; !ok {
		return fmt.Errorf("workspace %q not found", workspaceID)
	}

	sessionIDs := make(map[string]struct{})
	for id, session := range s.sessions {
		if session.WorkspaceID != workspaceID {
			continue
		}
		sessionIDs[id] = struct{}{}
		delete(s.sessions, id)
		delete(s.messages, id)
		s.closeSubscribersLocked(id)
	}

	for id, job := range s.jobs {
		if _, ok := sessionIDs[job.SessionID]; ok {
			delete(s.jobs, id)
		}
	}
	for id, object := range s.objects {
		if object.WorkspaceID == workspaceID {
			delete(s.objects, id)
			continue
		}
		if object.WorkspaceID == "" {
			if _, ok := sessionIDs[object.SessionID]; ok {
				delete(s.objects, id)
			}
		}
	}
	for id, artifact := range s.artifacts {
		if artifact.WorkspaceID == workspaceID {
			delete(s.artifacts, id)
			continue
		}
		if artifact.WorkspaceID == "" {
			if _, ok := sessionIDs[artifact.SessionID]; ok {
				delete(s.artifacts, id)
			}
		}
	}

	delete(s.workspaces, workspaceID)
	s.persistLocked()
	return nil
}

func (s *Store) Subscribe(sessionID string) (<-chan models.Event, func()) {
	ch := make(chan models.Event, 16)

	s.mu.Lock()
	if s.subscribers[sessionID] == nil {
		s.subscribers[sessionID] = make(map[chan models.Event]struct{})
	}
	s.subscribers[sessionID][ch] = struct{}{}
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if subscribers, ok := s.subscribers[sessionID]; ok {
			if _, present := subscribers[ch]; present {
				delete(subscribers, ch)
				if len(subscribers) == 0 {
					delete(s.subscribers, sessionID)
				}
				close(ch)
			}
		}
	}

	return ch, cancel
}

func (s *Store) Publish(sessionID string, event models.Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for subscriber := range s.subscribers[sessionID] {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (s *Store) closeSubscribersLocked(sessionID string) {
	subscribers, ok := s.subscribers[sessionID]
	if !ok {
		return
	}
	for subscriber := range subscribers {
		delete(subscribers, subscriber)
		close(subscriber)
	}
	delete(s.subscribers, sessionID)
}

func cloneSession(in *models.Session) *models.Session {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneWorkspace(in *models.Workspace) *models.Workspace {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneObject(in *models.ObjectMeta) *models.ObjectMeta {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(in.Metadata))
		for key, value := range in.Metadata {
			out.Metadata[key] = value
		}
	}
	return &out
}

func cloneMessage(in *models.Message) *models.Message {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneArtifact(in *models.Artifact) *models.Artifact {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneJob(in *models.Job) *models.Job {
	if in == nil {
		return nil
	}
	out := *in
	if in.Plan != nil {
		planCopy := models.Plan{
			Steps: make([]models.PlanStep, len(in.Plan.Steps)),
		}
		for i, step := range in.Plan.Steps {
			planCopy.Steps[i] = clonePlanStep(step)
		}
		out.Plan = &planCopy
	}
	if len(in.Steps) > 0 {
		out.Steps = make([]models.JobStep, len(in.Steps))
		for i, step := range in.Steps {
			out.Steps[i] = cloneJobStep(step)
		}
	}
	if len(in.Phases) > 0 {
		out.Phases = make([]models.JobPhase, len(in.Phases))
		for i, phase := range in.Phases {
			out.Phases[i] = cloneJobPhase(phase)
		}
	}
	if len(in.Checkpoints) > 0 {
		out.Checkpoints = append([]models.JobCheckpoint(nil), in.Checkpoints...)
	}
	return &out
}

func clonePlanStep(in models.PlanStep) models.PlanStep {
	out := in
	if len(in.Params) > 0 {
		out.Params = make(map[string]any, len(in.Params))
		for key, value := range in.Params {
			out.Params[key] = value
		}
	}
	if len(in.MemoryRefs) > 0 {
		out.MemoryRefs = append([]string(nil), in.MemoryRefs...)
	}
	return out
}

func cloneJobStep(in models.JobStep) models.JobStep {
	out := in
	if len(in.Params) > 0 {
		out.Params = make(map[string]any, len(in.Params))
		for key, value := range in.Params {
			out.Params[key] = value
		}
	}
	if len(in.ArtifactIDs) > 0 {
		out.ArtifactIDs = append([]string(nil), in.ArtifactIDs...)
	}
	if len(in.Facts) > 0 {
		out.Facts = make(map[string]any, len(in.Facts))
		for key, value := range in.Facts {
			out.Facts[key] = value
		}
	}
	if len(in.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(in.Metadata))
		for key, value := range in.Metadata {
			out.Metadata[key] = value
		}
	}
	return out
}

func cloneJobPhase(in models.JobPhase) models.JobPhase {
	out := in
	if len(in.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(in.Metadata))
		for key, value := range in.Metadata {
			out.Metadata[key] = value
		}
	}
	return out
}

func objectInWorkspace(object *models.ObjectMeta, workspaceID, sessionID string) bool {
	return object != nil && (object.WorkspaceID == workspaceID || (object.WorkspaceID == "" && object.SessionID == sessionID))
}

func artifactInWorkspace(artifact *models.Artifact, workspaceID, sessionID string) bool {
	return artifact != nil && (artifact.WorkspaceID == workspaceID || (artifact.WorkspaceID == "" && artifact.SessionID == sessionID))
}

func compareTimes(primaryA, primaryB, fallbackA, fallbackB time.Time) int {
	if !primaryA.Equal(primaryB) {
		return primaryB.Compare(primaryA)
	}
	return fallbackB.Compare(fallbackA)
}

func (s *Store) persistLocked() {
	if s.persistence == nil {
		return
	}
	if err := s.persistence.Save(s.snapshotLocked()); err != nil {
		log.Printf("session store persistence failed: %v", err)
	}
}

func (s *Store) snapshotLocked() *persistedState {
	state := &persistedState{
		Counter:    s.counter.Load(),
		Workspaces: make([]*models.Workspace, 0, len(s.workspaces)),
		Sessions:   make([]*models.Session, 0, len(s.sessions)),
		Objects:    make([]*models.ObjectMeta, 0, len(s.objects)),
		Jobs:       make([]*models.Job, 0, len(s.jobs)),
		Artifacts:  make([]*models.Artifact, 0, len(s.artifacts)),
		Messages:   make([]*models.Message, 0),
	}

	for _, workspace := range s.workspaces {
		state.Workspaces = append(state.Workspaces, cloneWorkspace(workspace))
	}
	for _, session := range s.sessions {
		state.Sessions = append(state.Sessions, cloneSession(session))
	}
	for _, object := range s.objects {
		state.Objects = append(state.Objects, cloneObject(object))
	}
	for _, job := range s.jobs {
		state.Jobs = append(state.Jobs, cloneJob(job))
	}
	for _, artifact := range s.artifacts {
		state.Artifacts = append(state.Artifacts, cloneArtifact(artifact))
	}
	for _, messages := range s.messages {
		for _, message := range messages {
			state.Messages = append(state.Messages, cloneMessage(message))
		}
	}

	slices.SortFunc(state.Workspaces, func(a, b *models.Workspace) int {
		return compareTimes(a.LastAccessedAt, b.LastAccessedAt, a.CreatedAt, b.CreatedAt)
	})
	slices.SortFunc(state.Sessions, func(a, b *models.Session) int {
		return compareTimes(a.LastAccessedAt, b.LastAccessedAt, a.CreatedAt, b.CreatedAt)
	})
	slices.SortFunc(state.Objects, func(a, b *models.ObjectMeta) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	slices.SortFunc(state.Jobs, func(a, b *models.Job) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	slices.SortFunc(state.Artifacts, func(a, b *models.Artifact) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	slices.SortFunc(state.Messages, func(a, b *models.Message) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	return state
}

func (s *Store) restore(state *persistedState) {
	if state == nil {
		return
	}

	for _, workspace := range state.Workspaces {
		if workspace != nil {
			s.workspaces[workspace.ID] = cloneWorkspace(workspace)
		}
	}
	for _, session := range state.Sessions {
		if session != nil {
			s.sessions[session.ID] = cloneSession(session)
		}
	}
	for _, object := range state.Objects {
		if object != nil {
			s.objects[object.ID] = cloneObject(object)
		}
	}
	for _, job := range state.Jobs {
		if job != nil {
			s.jobs[job.ID] = cloneJob(job)
		}
	}
	for _, artifact := range state.Artifacts {
		if artifact != nil {
			s.artifacts[artifact.ID] = cloneArtifact(artifact)
		}
	}
	for _, message := range state.Messages {
		if message != nil {
			s.messages[message.SessionID] = append(s.messages[message.SessionID], cloneMessage(message))
		}
	}
	for sessionID, messages := range s.messages {
		slices.SortFunc(messages, func(a, b *models.Message) int {
			return a.CreatedAt.Compare(b.CreatedAt)
		})
		s.messages[sessionID] = messages
	}

	nextCounter := state.Counter
	if derived := deriveCounter(state); derived > nextCounter {
		nextCounter = derived
	}
	s.counter.Store(nextCounter)
}

func deriveCounter(state *persistedState) uint64 {
	if state == nil {
		return 0
	}
	maxID := uint64(0)
	collect := func(id string) {
		if value, ok := parseCounter(id); ok && value > maxID {
			maxID = value
		}
	}

	for _, workspace := range state.Workspaces {
		if workspace != nil {
			collect(workspace.ID)
		}
	}
	for _, session := range state.Sessions {
		if session != nil {
			collect(session.ID)
		}
	}
	for _, object := range state.Objects {
		if object != nil {
			collect(object.ID)
		}
	}
	for _, job := range state.Jobs {
		if job != nil {
			collect(job.ID)
			collect(job.MessageID)
		}
	}
	for _, artifact := range state.Artifacts {
		if artifact != nil {
			collect(artifact.ID)
			collect(artifact.JobID)
			collect(artifact.ObjectID)
		}
	}
	for _, message := range state.Messages {
		if message != nil {
			collect(message.ID)
			collect(message.JobID)
		}
	}
	return maxID
}

func parseCounter(id string) (uint64, bool) {
	if id == "" {
		return 0, false
	}
	index := strings.LastIndex(id, "_")
	if index < 0 || index == len(id)-1 {
		return 0, false
	}
	value, err := strconv.ParseUint(id[index+1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
