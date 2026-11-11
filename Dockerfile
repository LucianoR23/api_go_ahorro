# syntax=docker/dockerfile:1.7

# ---------- build stage ----------
# Usamos la imagen oficial de Go. Alpine porque pesa menos (~300MB vs ~1GB).
# Keep in sync with the `toolchain` line in go.mod (exact tag, supported Alpine).
FROM golang:1.27.4-alpine3.24 AS build

# No apk here: modules come from proxy.golang.org (no git needed) and the
# image already ships the CA bundle. Fetching from Alpine's CDN failed twice
# on the server (TLS errors), so the build no longer depends on it.

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
# Minimal Alpine, no apk: the CA bundle is copied from the build stage and the
# binary embeds the zone database (time/tzdata, plan 0.2), so TZ works without
# the tzdata package. busybox provides adduser and the healthcheck's wget.
FROM alpine:3.24.3
ENV TZ=America/Argentina/Buenos_Aires

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN adduser -D -u 10001 app

WORKDIR /app
COPY --from=build /out/api /app/api
# Migrations are embedded in the binary (migrations/embed.go); no folder needed.

USER app

# El server escucha en $PORT (default 8080). Coolify setea esta env var.
EXPOSE 8080

# Healthcheck opcional — Coolify puede usarlo para verificar liveness.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health/live || exit 1

ENTRYPOINT ["/app/api"]
