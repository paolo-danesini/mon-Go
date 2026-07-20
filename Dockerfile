#build stage
FROM golang:1.21-alpine AS builder

WORKDIR /src

# Copia prima i file dei moduli per sfruttare la cache Docker sui layer:
# se go.mod/go.sum non cambiano, questo layer non viene ricostruito.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Binario statico (CGO_ENABLED=0) per poterlo eseguire su un'immagine finale
# minimale priva di librerie C (alpine).
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server .

#final stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /out/server .

# Compatibilità OpenShift: la restricted SCC di default esegue i container con
# un UID numerico arbitrario (mai root), sempre appartenente al gruppo 0 (root group).
# Impostando il gruppo proprietario a 0 e i permessi in lettura/esecuzione per il
# gruppo, il binario resta eseguibile indipendentemente dallo UID assegnato a runtime.
RUN chgrp -R 0 /app && chmod -R g=u /app

# USER numerico (non root) per ambienti che non forzano un UID arbitrario
# (es. Docker Compose locale, Kubernetes senza SCC). Su OpenShift questo valore
# viene comunque sovrascritto dalla SCC, ma non causa errori.
USER 1001

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["./server"]
