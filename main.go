package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rest-api/internal/mongoquery"
	"rest-api/internal/task"
)

func main() {
	store := task.NewStore()
	taskHandler := task.NewHandler(store)

	mux := http.NewServeMux()
	mux.HandleFunc("/tasks", taskHandler.Tasks)     // GET (list), POST (create)
	mux.HandleFunc("/tasks/", taskHandler.TaskByID) // GET, PUT, DELETE by id
	mux.HandleFunc("/health", taskHandler.Health)

	// Connessione MongoDB opzionale: attiva solo se MONGO_CNN è impostata,
	// così l'API task funziona anche senza un'istanza Mongo disponibile.
	var mongoClient *mongoquery.Client
	if mongoURI := os.Getenv("MONGO_CNN"); mongoURI != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		client, err := mongoquery.NewClient(ctx, mongoURI)
		cancel()
		if err != nil {
			log.Fatalf("impossibile connettersi a MongoDB: %v", err)
		}
		mongoClient = client

		mongoHandler := mongoquery.NewHandler(mongoClient)
		mux.HandleFunc("/mongo/", mongoHandler.Query)
		log.Println("endpoint MongoDB attivo su /mongo/{database}/{collection}/{key}={value}")
	} else {
		log.Println("MONGO_CNN non impostata: endpoint /mongo/ disabilitato")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
		// Timeout espliciti: senza questi, connessioni lente o malevole (Slowloris)
		// possono tenere impegnate goroutine/file descriptor indefinitamente.
		// Importante su OpenShift/Kubernetes dove il pod ha risorse (CPU/memoria)
		// e limiti di connessione condivisi con altri workload sul nodo.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Avvio del server in una goroutine per poter intercettare i segnali
	// di terminazione e chiudere correttamente le risorse (shutdown pulito).
	go func() {
		log.Printf("server in ascolto su %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("errore server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutdown in corso...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("errore durante lo shutdown del server: %v", err)
	}
	if err := mongoClient.Close(shutdownCtx); err != nil {
		log.Printf("errore durante la disconnessione da MongoDB: %v", err)
	}
	log.Println("shutdown completato")
}
