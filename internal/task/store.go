package task

import (
	"errors"
	"sync"
)

// ErrNotFound viene restituito quando una task non esiste.
var ErrNotFound = errors.New("task non trovata")

// Store gestisce le task in memoria in modo thread-safe.
type Store struct {
	mu     sync.RWMutex
	tasks  map[int]Task
	nextID int
}

// NewStore crea un nuovo Store vuoto.
func NewStore() *Store {
	return &Store{
		tasks:  make(map[int]Task),
		nextID: 1,
	}
}

// List restituisce tutte le task.
func (s *Store) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		result = append(result, t)
	}
	return result
}

// Get restituisce una task per ID.
func (s *Store) Get(id int) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return t, nil
}

// Create aggiunge una nuova task e ne restituisce l'ID assegnato.
func (s *Store) Create(title string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := Task{ID: s.nextID, Title: title, Done: false}
	s.tasks[t.ID] = t
	s.nextID++
	return t
}

// Update sostituisce i dati di una task esistente.
func (s *Store) Update(id int, title string, done bool) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	t.Title = title
	t.Done = done
	s.tasks[id] = t
	return t, nil
}

// Delete rimuove una task per ID.
func (s *Store) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[id]; !ok {
		return ErrNotFound
	}
	delete(s.tasks, id)
	return nil
}
