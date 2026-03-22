package session

import (
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"scagent/internal/models"
)

type Store struct {
	mu          sync.RWMutex
	counter     atomic.Uint64
	sessions    map[string]*models.Session
	objects     map[string]*models.ObjectMeta
	jobs        map[string]*models.Job
	artifacts   map[string]*models.Artifact
	messages    map[string][]*models.Message
	subscribers map[string]map[chan models.Event]struct{}
}

func NewStore() *Store {
	return &Store{
		sessions:    make(map[string]*models.Session),
		objects:     make(map[string]*models.ObjectMeta),
		jobs:        make(map[string]*models.Job),
		artifacts:   make(map[string]*models.Artifact),
		messages:    make(map[string][]*models.Message),
		subscribers: make(map[string]map[chan models.Event]struct{}),
	}
}

func (s *Store) NextID(prefix string) string {
	return fmt.Sprintf("%s_%06d", prefix, s.counter.Add(1))
}

func (s *Store) CreateSession(label string) *models.Session {
	now := time.Now().UTC()
	session := &models.Session{
		ID:             s.NextID("sess"),
		Label:          label,
		DatasetID:      s.NextID("ds"),
		Status:         models.SessionActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = cloneSession(session)
	return cloneSession(session)
}

func (s *Store) SaveSession(session *models.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = cloneSession(session)
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

func (s *Store) SaveArtifact(artifact *models.Artifact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts[artifact.ID] = cloneArtifact(artifact)
}

func (s *Store) AddMessage(message *models.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[message.SessionID] = append(s.messages[message.SessionID], cloneMessage(message))
}

func (s *Store) Snapshot(sessionID string) (*models.SessionSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}

	objects := make([]*models.ObjectMeta, 0)
	for _, object := range s.objects {
		if object.SessionID == sessionID {
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
		if artifact.SessionID == sessionID {
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
		Session:   cloneSession(session),
		Objects:   objects,
		Jobs:      jobs,
		Artifacts: artifacts,
		Messages:  messages,
	}, nil
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
			delete(subscribers, ch)
			if len(subscribers) == 0 {
				delete(s.subscribers, sessionID)
			}
		}
		close(ch)
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

func cloneSession(in *models.Session) *models.Session {
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
	if len(in.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(in.Metadata))
		for key, value := range in.Metadata {
			out.Metadata[key] = value
		}
	}
	return out
}
