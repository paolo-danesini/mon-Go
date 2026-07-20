# Istruzioni per agenti AI - REST API Go

## Architettura
- API REST in Go, standard library (`net/http`) + driver ufficiale `go.mongodb.org/mongo-driver`.
- Entry point: [main.go](../main.go) - definisce le rotte, avvia il server con graceful shutdown
  (`srv.Shutdown` su `SIGINT`/`SIGTERM`), attiva l'endpoint Mongo solo se `MONGO_CNN` è impostata.
- Due bounded context sotto `internal/`, stesso pattern (Store/Client iniettato in Handler):
  - `internal/task/`: CRUD in-memory. [model.go](../internal/task/model.go) (struct `Task`),
    [store.go](../internal/task/store.go) (mappa `map[int]Task` protetta da `sync.RWMutex`),
    [handler.go](../internal/task/handler.go).
  - `internal/mongoquery/`: query generiche su MongoDB. [client.go](../internal/mongoquery/client.go)
    incapsula `*mongo.Client` (NIENTE variabili globali, a differenza del prototipo originale in
    `API-MONGO/test.go.reference.txt`), [handler.go](../internal/mongoquery/handler.go) espone
    `GET /mongo/{database}/{collection}/{key}={value}` (aggiungere `?all=true` per ottenere tutti
    i documenti corrispondenti come array, invece del solo primo tramite `Client.FindOne`/`Client.Find`).
    Se `{key}={value}` è omesso, scarica l'intera collection (`Client.FindAll`, opzionale `?limit=`).
- `API-MONGO/test.go.reference.txt` è il prototipo originale (rinominato per escluderlo dalla build
  Go, dato che è `package main` con gli stessi import e causava conflitti in `go build ./...`).
  Non ripristinare l'estensione `.go` senza spostarlo in un modulo separato.

## Convenzioni
- Errori di dominio (`task.ErrNotFound`, `mongoquery.ErrNotFound`) gestiti con `errors.Is` e mappati
  a status HTTP specifici (`handleStoreErr` in task, `writeError` in mongoquery: 404/504/500).
- Risposte JSON tramite helper `writeJSON` locale a ciascun package; errori tramite `http.Error`.
- Eccezione: gli errori dell'endpoint `/mongo/` sono strutturati come JSON `{codiceErrore, descrizioneErrore}`
  (vedi `ErrorResponse` e `writeErrorJSON` in [handler.go](../internal/mongoquery/handler.go)), non testo semplice.
- Routing manuale senza router esterni: `/risorsa` per lista/creazione, `/risorsa/{id}` con
  `strings.TrimPrefix` + parsing manuale (vedi `TaskByID`, `mongoquery.Handler.Query`).
- Ogni query Mongo usa `context.WithTimeout` (5s, costante `queryTimeout`) per evitare goroutine bloccate.
- Il client Mongo viene creato una sola volta in `main.go` e chiuso allo shutdown: non istanziare
  nuovi `mongo.Client` per singola richiesta.

## Workflow di sviluppo
- Build: `go build ./...`
- Avvio locale: `go run main.go` (task VS Code "go: run server" già configurato).
- **Importante**: `GO111MODULE` deve essere `on` (già impostato globalmente con `go env -w GO111MODULE=on`).
- **Dipendenza mongo-driver**: se `go.sum` manca o `go build` segnala "missing go.sum entry", serve
  connettività verso `proxy.golang.org` (o un proxy Go interno) ed eseguire `go mod tidy`.
- Endpoint Mongo attivo solo con `MONGO_CNN` impostata (env var); altrimenti l'app parte comunque
  con la sola API `/tasks`.
- Docker: `docker compose up --build` avvia API + MongoDB locale collegati (vedi
  [docker-compose.yml](../docker-compose.yml) e [Dockerfile](../Dockerfile), build multi-stage
  con binario statico `CGO_ENABLED=0`).
- **Compatibilità OpenShift/SCC restrittive**: il Dockerfile applica `chgrp -R 0 /app && chmod -R g=u /app`
  e `USER 1001`, verificato funzionante anche con `docker run --user <UID arbitrario>:0`. Non rimuovere
  questi step: senza di essi il pod va in CrashLoopBackOff sotto la restricted SCC di OpenShift.
- **Cap di sicurezza memoria**: `maxResultsHardCap` (5000) in [client.go](../internal/mongoquery/client.go)
  e `maxRequestBodyBytes` (1MiB) in [task/handler.go](../internal/task/handler.go) proteggono da OOM in
  container con risorse limitate; non rimuoverli quando si estendono le query Mongo.
- Nessun test presente ancora: aggiungere file `_test.go` accanto ai file in `internal/<package>/`.

## Estendere il progetto
- Nuove risorse in-memory: nuovo package sotto `internal/<risorsa>/` seguendo il pattern model/store/handler.
- Nuove query Mongo: aggiungere metodi a `mongoquery.Client` seguendo lo stile di `FindOne`
  (context con timeout, wrapping errori con `fmt.Errorf("mongo: ...: %w", err)`).
- Se serve un router più avanzato (path params, middleware), valutare `chi` o `gorilla/mux`.
