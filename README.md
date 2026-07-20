# REST API Go

API REST minimale in Go (standard library `net/http` + driver ufficiale MongoDB) per gestire:
- una risorsa `Task` in memoria (CRUD completo);
- query generiche su MongoDB tramite path `/mongo/{database}/{collection}/{key}={value}`.

## Requisiti
- Go 1.21+
- (Opzionale) Docker e Docker Compose per l'esecuzione containerizzata
- (Opzionale) Istanza MongoDB / Cosmos DB (API MongoDB) per l'endpoint `/mongo/`

## ⚠️ Setup dipendenze (prima esecuzione)

Il progetto usa `go.mongodb.org/mongo-driver`. Se `go.sum` non è presente (es. per mancanza di
connettività verso proxy.golang.org durante lo sviluppo), eseguire su una macchina con accesso
a internet o a un proxy Go interno aziendale:

```powershell
go mod tidy
```

Questo genera/aggiorna `go.sum` con i checksum delle dipendenze, necessario sia per `go build`
sia per la build dell'immagine Docker.

## Avvio locale

```powershell
go run main.go
```

Il server si avvia su `http://localhost:8080`. L'endpoint `/mongo/` si attiva solo se la
variabile d'ambiente `MONGO_CNN` è impostata con la connection string MongoDB.

```powershell
$env:MONGO_CNN = "mongodb://localhost:27017"
go run main.go
```

## Avvio con Docker Compose

```powershell
docker compose up --build
```

Avvia sia l'API (porta 8080) sia un'istanza MongoDB locale (porta 27017), già collegate tra loro
tramite la variabile `MONGO_CNN=mongodb://mongo:27017`.

## ⚠️ Connessione a Cosmos DB (API MongoDB) con replicaSet

Se la connection string include `replicaSet=globaldb` (tipico di Azure Cosmos DB), il driver Go
esegue la discovery della topologia (SDAM) e tenta di raggiungere **tutti** i membri del replica
set riportati da Cosmos DB. Se questi non sono raggiungibili dalla rete corrente (VPN aziendale,
Private Endpoint non collegato, ecc.), **ogni query va in timeout** anche se la connessione
iniziale (`Ping`) riesce — a differenza di MongoDB Compass, che è più tollerante su questo aspetto.

**Soluzione**: rimuovere `replicaSet=globaldb` e aggiungere `directConnection=true` alla
connection string, per bypassare la discovery e parlare solo con l'host indicato:

```
mongodb://<user>:<password>@<account>.mongo.cosmos.azure.com:10255/?authSource=admin&ssl=true&directConnection=true
```

Vedi [.vscode/launch.json](.vscode/launch.json) per un esempio funzionante.

## Endpoint

| Metodo | Path         | Descrizione            |
|--------|--------------|-------------------------|
| GET    | /health      | Stato del servizio      |
| GET    | /mongo/{database}/{collection}/{key}={value} | Cerca un documento MongoDB (richiede `MONGO_CNN`) |
| GET    | /mongo/{database}/{collection}/{key}={value}?all=true | Cerca **tutti** i documenti corrispondenti (array JSON) |
| GET    | /mongo/{database}/{collection} | Scarica **l'intera collection** (array JSON) |
| GET    | /mongo/{database}/{collection}?limit=100 | Come sopra, limitando il numero di documenti restituiti |

Esempio:
```powershell
Invoke-RestMethod "http://localhost:8080/mongo/shop/prodotti/sku=ABC123"
Invoke-RestMethod "http://localhost:8080/mongo/shop/prodotti/categoria=elettronica?all=true"
Invoke-RestMethod "http://localhost:8080/mongo/shop/prodotti?limit=50"
```

### Formato errori endpoint /mongo/

Tutti gli errori dell'endpoint `/mongo/` (400, 404, 405, 500, 504) vengono restituiti in JSON,
con `codiceErrore` coincidente con lo status code HTTP della risposta:

```json
{
  "codiceErrore": 404,
  "descrizioneErrore": "documento non trovato"
}
```

## Struttura del progetto

```
main.go                        # entry point, rotte, connessione Mongo, graceful shutdown
internal/task/model.go         # struct Task
internal/task/store.go         # store in-memory thread-safe
internal/task/handler.go       # handler HTTP CRUD Task
internal/mongoquery/client.go  # client MongoDB incapsulato (no variabili globali)
internal/mongoquery/handler.go # handler HTTP query MongoDB con timeout e gestione errori
```

## Gestione memoria ed errori (rispetto alla versione originale)

- **Nessuna variabile globale mutabile**: connessione MongoDB e stato incapsulati in `mongoquery.Client`,
  iniettato nell'handler (stesso pattern DI usato per `task.Store`).
- **Timeout su ogni query** (`context.WithTimeout`, 5s) per evitare goroutine bloccate indefinitamente.
- **Ciclo di vita esplicito** della connessione: `NewClient` verifica la raggiungibilità (`Ping`) all'avvio,
  `Close` viene chiamato allo shutdown (`SIGINT`/`SIGTERM`) tramite `srv.Shutdown` + `mongoClient.Close`.
- **Errori tipizzati**: `ErrNotFound` mappato a `404`, timeout a `504`, errori generici a `500`
  (invece del singolo `500` indiscriminato della versione originale).
- **Validazione input**: path e formato `key=value` validati prima di interrogare il database (`400` se non validi).
- **Limite massimo risultati (`maxResultsHardCap = 5000`)**: applicato SEMPRE su `FindAll`/`Find`, anche
  senza `?limit` esplicito. Senza questo cap, scaricare una collection molto grande (`GET /mongo/{db}/{coll}`)
  caricherebbe l'intero result set in RAM tramite `cur.All`, con rischio concreto di **OOMKill** in un
  container con risorse limitate (es. sidecar OpenShift/Kubernetes con limit di poche centinaia di MiB).
- **Limite dimensione body richieste (`maxRequestBodyBytes = 1MiB`)**: applicato via `http.MaxBytesReader`
  su POST/PUT `/tasks`, per evitare che un payload abnorme (accidentale o malevolo) esaurisca la memoria
  del processo durante il parsing JSON.
- **Timeout HTTP server** (`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout` in [main.go](main.go)):
  senza questi, connessioni lente/malevole (Slowloris) o client che non chiudono la connessione possono
  esaurire goroutine e file descriptor del processo.

## Compatibilità OpenShift / Kubernetes (uso come sidecar)

Verificato che l'immagine funziona correttamente con un **UID arbitrario non-root**, il pattern imposto
dalla **restricted SCC** di default di OpenShift (mai root, UID casuale appartenente al gruppo 0):

```powershell
docker run --rm --user 123456:0 -p 8080:8080 rest-api:local
```

Per ottenere questo, il [Dockerfile](Dockerfile) applica:
- `RUN chgrp -R 0 /app && chmod -R g=u /app`: rende i file leggibili/eseguibili dal gruppo 0, indipendentemente
  dall'UID numerico assegnato a runtime dalla SCC.
- `USER 1001`: UID non-root esplicito per ambienti che non impongono un UID arbitrario (Docker Compose locale,
  Kubernetes senza SCC restrittive).

Altre raccomandazioni per l'uso come sidecar:
- **Filesystem read-only**: l'app non scrive su disco a runtime, compatibile con
  `securityContext.readOnlyRootFilesystem: true`.
- **Resource limits**: impostare `resources.limits.memory` nel pod OpenShift coerenti con `maxResultsHardCap`
  (5000 documenti bson.M possono occupare diversi MiB a seconda della dimensione media dei documenti);
  abbassare la costante in [client.go](internal/mongoquery/client.go) se i limiti di memoria sono molto stretti.
- **Graceful shutdown**: lo shutdown (`SIGTERM`) ha timeout di 10s in [main.go](main.go); assicurarsi che
  `terminationGracePeriodSeconds` del pod sia superiore (default Kubernetes/OpenShift: 30s, va bene).
- **Liveness vs readiness**: `/health` verifica solo che il server HTTP risponda, non la raggiungibilità di
  MongoDB. Per un readiness probe più accurato in scenari con Mongo remoto, valutare un endpoint dedicato
  che esegua `client.Ping`.

## Build

```powershell
go build ./...
```

