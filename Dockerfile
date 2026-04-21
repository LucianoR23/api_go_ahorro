# syntax=docker/dockerfile:1.7

# ---------- build stage ----------
# Usamos la imagen oficial de Go. Alpine porque pesa menos (~300MB vs ~1GB).
# Cambiar la versión acá cuando bumpees go.mod.
FROM golang:1.25-alpine AS build

# git es necesario si algún mod tiene deps de repos privados; ca-certs para
# que go mod download pueda hablar con proxy.golang.org por HTTPS.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copiamos primero go.mod/go.sum para aprovechar la cache de Docker: si no
# cambian, no se re-descargan las deps en builds sucesivos.
COPY go.mod go.sum ./
RUN go mod download

# Ahora el código. Los archivos que no queremos copiar están en .dockerignore.
COPY . .

# Build estático. CGO_ENABLED=0 → binario sin dependencias de libc, corre
# en scratch/distroless. -trimpath elimina paths absolutos del binario.
# -ldflags="-s -w" strip symbols para binario ~30% más chico.
ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}
RUN go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/api \
    ./cmd/api

# ---------- runtime stage ----------
# Alpine mínimo con ca-certs + tzdata (necesario para time.LoadLocation si
# algún día usamos timezones distintos). ~8MB final.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 app

WORKDIR /app
COPY --from=build /out/api /app/api
COPY migrations /app/migrations

USER app

# El server escucha en $PORT (default 8080). Coolify setea esta env var.
EXPOSE 8080

# Healthcheck opcional — Coolify puede usarlo para verificar liveness.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health/live || exit 1

ENTRYPOINT ["/app/api"]
