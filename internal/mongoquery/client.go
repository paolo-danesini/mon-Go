// Package mongoquery fornisce un client MongoDB e un handler HTTP
// per interrogare documenti tramite chiave/valore.
//
// A differenza dell'implementazione originale (variabili globali mutabili
// database/collectionMongo/methodName), lo stato di connessione è incapsulato
// in un Client, reso thread-safe dal driver stesso e con un ciclo di vita
// esplicito (NewClient / Close) gestito dal chiamante (main.go), per evitare
// leak di connessioni e race condition tra richieste concorrenti.
package mongoquery

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ErrNotFound viene restituito quando nessun documento soddisfa il filtro.
var ErrNotFound = errors.New("documento non trovato")

// maxResultsHardCap è il tetto massimo assoluto di documenti restituiti da
// FindAll/Find, applicato SEMPRE anche se il chiamante non specifica un limite
// (o ne richiede uno più alto). Protegge la memoria del processo: senza questo
// cap, `cur.All` carica l'intero result set in RAM, con rischio concreto di
// OOMKill quando l'app gira come sidecar con risorse limitate (es. OpenShift/
// Kubernetes con requests/limits di poche centinaia di MiB).
const maxResultsHardCap = 5000

// Client incapsula la connessione MongoDB. Va creato una sola volta
// all'avvio dell'applicazione e chiuso con Close allo shutdown.
type Client struct {
	mongo *mongo.Client
}

// NewClient si connette a MongoDB usando l'URI fornito.
// Restituisce un errore se l'URI è vuoto o la connessione/ping falliscono.
func NewClient(ctx context.Context, uri string) (*Client, error) {
	if uri == "" {
		return nil, errors.New("mongo: URI di connessione mancante (variabile MONGO_CNN)")
	}

	structCodec, err := bsoncodec.NewStructCodec(bsoncodec.JSONFallbackStructTagParser)
	if err != nil {
		return nil, fmt.Errorf("mongo: impossibile creare lo struct codec: %w", err)
	}
	registry := bson.NewRegistryBuilder().
		RegisterDefaultEncoder(reflect.Struct, structCodec).
		RegisterDefaultDecoder(reflect.Struct, structCodec).
		Build()

	opts := options.Client().SetRegistry(registry).ApplyURI(uri)

	mc, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: connessione fallita: %w", err)
	}

	// Verifica immediata della raggiungibilità: evita di scoprire
	// un URI errato solo alla prima richiesta HTTP.
	if err := mc.Ping(ctx, nil); err != nil {
		_ = mc.Disconnect(ctx)
		return nil, fmt.Errorf("mongo: ping fallito: %w", err)
	}

	return &Client{mongo: mc}, nil
}

// Close rilascia la connessione. Va chiamata allo shutdown dell'applicazione
// (vedi main.go) per liberare correttamente le risorse di rete.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.mongo == nil {
		return nil
	}
	return c.mongo.Disconnect(ctx)
}

// FindOne cerca il primo documento in database.collection dove key == value
// e lo restituisce come bson.M. Restituisce ErrNotFound se non esiste alcun
// documento corrispondente.
//
// Il valore da cercare arriva sempre come stringa dal path HTTP (es. "n_polizza=123"),
// ma nel documento MongoDB il campo potrebbe essere memorizzato come stringa o come
// numero (Int32/Int64/Double). Per non richiedere all'utente di conoscere il tipo
// esatto, il filtro usa $or provando sia il valore come stringa sia, se convertibile,
// come numero (int64 e double).
func (c *Client) FindOne(ctx context.Context, database, collection, key, value string) (bson.M, error) {
	if database == "" || collection == "" || key == "" {
		return nil, errors.New("mongo: database, collection e key sono obbligatori")
	}

	coll := c.mongo.Database(database).Collection(collection)

	filter := buildFlexibleFilter(key, value)

	var result bson.M
	err := coll.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("mongo: query fallita: %w", err)
	}

	return result, nil
}

// Find cerca tutti i documenti in database.collection dove key == value
// (stesso matching flessibile stringa/numero di FindOne) e li restituisce
// come slice di bson.M. Restituisce ErrNotFound se non esiste alcun documento
// corrispondente, per uniformità con FindOne.
func (c *Client) Find(ctx context.Context, database, collection, key, value string) ([]bson.M, error) {
	if database == "" || collection == "" || key == "" {
		return nil, errors.New("mongo: database, collection e key sono obbligatori")
	}

	coll := c.mongo.Database(database).Collection(collection)

	filter := buildFlexibleFilter(key, value)

	// Cap di sicurezza anche qui: senza limite, una chiave poco selettiva
	// (es. un campo con molti duplicati) potrebbe restituire un numero enorme
	// di documenti e saturare la memoria del pod.
	opts := options.Find().SetLimit(maxResultsHardCap)

	cur, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: query fallita: %w", err)
	}
	defer cur.Close(ctx)

	var results []bson.M
	if err := cur.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("mongo: lettura risultati fallita: %w", err)
	}

	if len(results) == 0 {
		return nil, ErrNotFound
	}

	return results, nil
}

// FindAll restituisce tutti i documenti di database.collection, senza alcun
// filtro. Il parametro limit, se > 0, limita il numero massimo di documenti
// restituiti (protezione contro download accidentali di collection enormi).
// Restituisce ErrNotFound se la collection è vuota.
func (c *Client) FindAll(ctx context.Context, database, collection string, limit int64) ([]bson.M, error) {
	if database == "" || collection == "" {
		return nil, errors.New("mongo: database e collection sono obbligatori")
	}

	// Il cap di sicurezza si applica sempre: se limit è 0 (non richiesto) o
	// superiore al cap, viene forzato a maxResultsHardCap.
	if limit <= 0 || limit > maxResultsHardCap {
		limit = maxResultsHardCap
	}

	coll := c.mongo.Database(database).Collection(collection)

	opts := options.Find().SetLimit(limit)

	cur, err := coll.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: query fallita: %w", err)
	}
	defer cur.Close(ctx)

	var results []bson.M
	if err := cur.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("mongo: lettura risultati fallita: %w", err)
	}

	if len(results) == 0 {
		return nil, ErrNotFound
	}

	return results, nil
}

// buildFlexibleFilter costruisce un filtro che confronta value con key
// sia come stringa sia, se numericamente valido, come int64/float64.
// Questo evita falsi "documento non trovato" quando il campo è tipizzato
// come numero nel documento invece che come stringa.
func buildFlexibleFilter(key, value string) bson.M {
	candidates := bson.A{value}

	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		candidates = append(candidates, i)
	} else if f, err := strconv.ParseFloat(value, 64); err == nil {
		candidates = append(candidates, f)
	}

	return bson.M{key: bson.M{"$in": candidates}}
}
