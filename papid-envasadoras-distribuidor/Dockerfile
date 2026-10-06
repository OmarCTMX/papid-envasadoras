# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /distribuidor .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=America/Mexico_City
WORKDIR /app
COPY --from=build /distribuidor /app/distribuidor
# Las variables se pasan en runtime:
#   docker run --env-file .env papid-envasadoras-distribuidor:latest
CMD ["/app/distribuidor"]
