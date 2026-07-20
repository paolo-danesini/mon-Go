package mongoquery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// queryTimeout limita la durata massima di una query MongoDB, evitando che
// richieste lente o un cluster irraggiungibile blocchino indefinitamente
// una goroutine del server HTTP.
const queryTimeout = 5 * time.Second

// ErrorResponse è il formato JSON restituito per ogni errore dell'endpoint /mongo/.
// CodiceErrore coincide con lo status code HTTP della risposta.
type ErrorResponse struct {
	CodiceErrore      int    `json:"codiceErrore"`
	DescrizioneErrore string `json:"descrizioneErrore"`
}

// Handler espone l'endpoint HTTP per interrogare MongoDB.
type Handler struct {
	client *Client
}

// NewHandler crea un nuovo Handler collegato al Client fornito.
func NewHandler(client *Client) *Handler {
	return &Handler{client: client}
}

// Query gestisce GET /mongo/{database}/{collection}[/{key}={value}].
// Esempio: /mongo/shop/prodotti/sku=ABC123
//
// Se {key}={value} viene omesso (path /mongo/{database}/{collection}), restituisce
// l'intera collection come array JSON. Parametro query opzionale "limit" per limitare
// il numero di documenti restituiti in questo caso (default: nessun limite).
// Esempio: /mongo/shop/prodotti?limit=100
//
// Parametro query opzionale "all": se impostato a "true" (o "1") insieme a {key}={value},
// restituisce un array JSON con tutti i documenti che soddisfano la chiave di ricerca,
// invece del solo primo documento trovato.
// Esempio: /mongo/shop/prodotti/categoria=elettronica?all=true
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "metodo non consentito")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/mongo/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeErrorJSON(w, http.StatusBadRequest, "path non valido: atteso /mongo/{database}/{collection}[/{key}={value}]")
		return
	}

	database, collection := parts[0], parts[1]

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	// Path senza {key}={value}: scarica l'intera collection.
	if len(parts) == 2 || parts[2] == "" {
		var limit int64
		if l, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && l > 0 {
			limit = l
		}

		results, err := h.client.FindAll(ctx, database, collection, limit)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, results)
		return
	}

	kv := strings.SplitN(parts[2], "=", 2)
	if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
		writeErrorJSON(w, http.StatusBadRequest, "formato chiave non valido: atteso {key}={value}")
		return
	}
	key, value := kv[0], kv[1]

	if findAll, _ := strconv.ParseBool(r.URL.Query().Get("all")); findAll {
		results, err := h.client.Find(ctx, database, collection, key, value)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, results)
		return
	}

	result, err := h.client.FindOne(ctx, database, collection, key, value)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErrorJSON(w, http.StatusNotFound, "documento non trovato")
	case errors.Is(err, context.DeadlineExceeded):
		writeErrorJSON(w, http.StatusGatewayTimeout, "timeout durante la query MongoDB")
	default:
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}

func writeErrorJSON(w http.ResponseWriter, status int, descrizione string) {
	writeJSON(w, status, ErrorResponse{
		CodiceErrore:      status,
		DescrizioneErrore: descrizione,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
