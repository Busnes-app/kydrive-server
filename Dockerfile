# Multi-stage build for kydrive-server

# Stage 1: Build React PWA Frontend
FROM node:26-alpine@sha256:0b36e8c136b94cd4fcf02188228e76c31ad5872eef3fec8cbd2eee500cfd9e80 AS frontend-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go Standalone Binary
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o kydrive-server ./cmd/server

# Stage 3: Minimal Production Container
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk --no-cache add ca-certificates tzdata restic
ARG SOURCE_REVISION=unknown
ARG SOURCE_URL=https://github.com/Busnes-app/kydrive-server
LABEL org.opencontainers.image.source=$SOURCE_URL \
      org.opencontainers.image.revision=$SOURCE_REVISION \
      org.opencontainers.image.licenses=MIT
WORKDIR /app
COPY --from=backend-builder /app/kydrive-server /app/kydrive-server
COPY --from=frontend-builder /app/web/dist-fonts /app/excalidraw-fonts
# /app/backups is the optional mount for sealed local capsules; KY_BACKUP_DIR is set by the
# operator (compose does), so an image run bare keeps no local copies.
RUN mkdir -p /app/data /app/backups /app/bulk && chown -R 1000:1000 /app
USER 1000:1000

ENV KY_PORT=8080
ENV KY_HOST=0.0.0.0
ENV KY_DATA_DIR=/app/data
ENV KY_EXCALIDRAW_FONTS_DIR=/app/excalidraw-fonts

EXPOSE 8080
VOLUME ["/app/data", "/app/backups"]

ENTRYPOINT ["/app/kydrive-server"]
