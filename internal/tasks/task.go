// Package tasks provides a structured task tracking system inspired by
// claude-code's TaskCreate/Get/List/Update tools. Tasks are persisted as
// JSON files on disk and support dependency tracking via blocks/blockedBy.
package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Status is a task status.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusDeleted    Status = "deleted"
)

// Task is a structured unit of work, modeled after claude-code's Task type.
type Task struct {
	ID          string                 `json:"id"`
	Subject     string                 `json:"subject"`
	Description string                 `json:"description"`
	ActiveForm  string                 `json:"activeForm,omitempty"`
	Owner       string                 `json:"owner,omitempty"`
	Status      Status                 `json:"status"`
	Blocks      []string               `json:"blocks"`
	BlockedBy   []string               `json:"blockedBy"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Store persists tasks to a JSON file directory.
type Store struct {
	mu            sync.Mutex
	dir           string
	highWatermark int
}

// NewStore creates a task store rooted at dir (e.g. ".crush/tasks").
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create task dir: %w", err)
	}
	s := &Store{dir: dir}
	s.loadHighWatermark()
	return s, nil
}

func (s *Store) loadHighWatermark() {
	data, err := os.ReadFile(filepath.Join(s.dir, ".highwatermark"))
	if err != nil {
		return
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	s.highWatermark = n
}

func (s *Store) saveHighWatermark() {
	_ = os.WriteFile(filepath.Join(s.dir, ".highwatermark"), []byte(strconv.Itoa(s.highWatermark)), 0o644)
}

// Create adds a new task and returns it.
func (s *Store) Create(subject, description, activeForm string, metadata map[string]interface{}) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.highWatermark++
	id := strconv.Itoa(s.highWatermark)
	t := &Task{
		ID:          id,
		Subject:     subject,
		Description: description,
		ActiveForm:  activeForm,
		Status:      StatusPending,
		Blocks:      []string{},
		BlockedBy:   []string{},
		Metadata:    metadata,
	}
	if err := s.write(t); err != nil {
		return nil, err
	}
	s.saveHighWatermark()
	return t, nil
}

// Get returns a task by ID, or nil.
func (s *Store) Get(id string) (*Task, error) {
	t, err := s.read(id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

// List returns all non-deleted tasks in ID order.
func (s *Store) List() ([]*Task, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var tasks []*Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if _, err := strconv.Atoi(id); err != nil {
			continue // skip non-numeric files
		}
		t, err := s.read(id)
		if err != nil {
			continue
		}
		if t.Status != StatusDeleted {
			tasks = append(tasks, t)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		ai, _ := strconv.Atoi(tasks[i].ID)
		aj, _ := strconv.Atoi(tasks[j].ID)
		return ai < aj
	})
	return tasks, nil
}

// Update saves changes to a task. Fields that are non-zero are applied;
// "" strings, nil slices, and nil metadata are ignored (use explicit
// delete for slices/metadata). Returns the updated task or an error.
func (s *Store) Update(id string, fn func(t *Task)) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, err := s.read(id)
	if err != nil {
		return nil, err
	}
	fn(t)
	return t, s.write(t)
}

// Delete soft-deletes a task and removes references from other tasks.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Soft-delete the task.
	t, err := s.read(id)
	if err != nil {
		return err
	}
	t.Status = StatusDeleted
	if err := s.write(t); err != nil {
		return err
	}

	// Remove references from other tasks.
	all, err := s.listUnlocked()
	if err != nil {
		return err
	}
	for _, other := range all {
		changed := false
		other.Blocks = removeStr(other.Blocks, id)
		other.BlockedBy = removeStr(other.BlockedBy, id)
		if changed {
			if err := s.write(other); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) read(id string) (*Task, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	t := &Task{}
	if err := json.Unmarshal(data, t); err != nil {
		return nil, fmt.Errorf("parse task %s: %w", id, err)
	}
	if t.Blocks == nil {
		t.Blocks = []string{}
	}
	if t.BlockedBy == nil {
		t.BlockedBy = []string{}
	}
	return t, nil
}

func (s *Store) write(t *Task) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(t.ID), data, 0o644)
}

func (s *Store) listUnlocked() ([]*Task, error) {
	return s.List()
}

func removeStr(slice []string, s string) []string {
	for i, v := range slice {
		if v == s {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}
