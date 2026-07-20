package task

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Handler espone gli endpoint HTTP per la risorsa Task.
type Handler struct {
	store *Store
}

// maxRequestBodyBytes limita la dimensione massima del corpo delle richieste
// POST/PUT, a protezione della memoria del processo da payload abnormi.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// NewHandler crea un nuovo Handler collegato allo Store fornito.
func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

// Health risponde con lo stato del servizio.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Tasks gestisce GET (lista) e POST (creazione) su /tasks.
func (h *Handler) Tasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.store.List())
	case http.MethodPost:
		var input struct {
			Title string `json:"title"`
		}
		// MaxBytesReader impedisce che un body abnormemente grande (accidentale
		// o malevolo) venga letto interamente in memoria prima del parsing JSON.
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "corpo richiesta non valido o troppo grande", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(input.Title) == "" {
			http.Error(w, "il campo title è obbligatorio", http.StatusBadRequest)
			return
		}
		t := h.store.Create(input.Title)
		writeJSON(w, http.StatusCreated, t)
	default:
		http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
	}
}

// TaskByID gestisce GET, PUT e DELETE su /tasks/{id}.
func (h *Handler) TaskByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/tasks/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "id non valido", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		t, err := h.store.Get(id)
		if handleStoreErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, t)

	case http.MethodPut:
		var input struct {
			Title string `json:"title"`
			Done  bool   `json:"done"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "corpo richiesta non valido o troppo grande", http.StatusBadRequest)
			return
		}
		t, err := h.store.Update(id, input.Title, input.Done)
		if handleStoreErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, t)

	case http.MethodDelete:
		if err := h.store.Delete(id); handleStoreErr(w, err) {
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "metodo non consentito", http.StatusMethodNotAllowed)
	}
}

func handleStoreErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
