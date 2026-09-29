# syntax=docker/dockerfile:1

# ---------- Build ----------
FROM golang:1.26-alpine AS build

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache de dependencias
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /dashboard-envasadoras ./cmd/dashboard

# ---------- Runtime ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

ENV TZ=America/Mexico_City

WORKDIR /app

COPY --from=build /dashboard-envasadoras /app/dashboard-envasadoras
COPY internal/dashboard/web ./internal/dashboard/web

EXPOSE 3000

# Las variables del .env se pasan en runtime:
#   docker run --env-file .env -p 3000:3000 dashboard-envasadoras:latest
CMD ["/app/dashboard-envasadoras"]
