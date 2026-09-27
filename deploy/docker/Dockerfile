FROM oven/bun:1 AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build

FROM golang:1.27 AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend/ ./backend/
COPY --from=frontend /src/build/frontend/. ./backend/internal/static/dist
RUN cd backend && CGO_ENABLED=0 go build -o /music-enreachment ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=backend /music-enreachment /music-enreachment
EXPOSE 8080
ENTRYPOINT ["/music-enreachment"]
