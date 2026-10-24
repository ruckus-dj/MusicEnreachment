FROM node:24-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.27-bookworm AS backend
WORKDIR /src
RUN apt-get update && apt-get install --no-install-recommends -y g++ libvips-dev pkg-config && rm -rf /var/lib/apt/lists/*
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend/ ./backend/
COPY --from=frontend /src/build/frontend/. ./backend/internal/static/dist
RUN cd backend && CGO_ENABLED=1 go build -o /melotrove ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates libstdc++6 libvips42 && rm -rf /var/lib/apt/lists/*
COPY --from=backend /melotrove /melotrove
COPY --from=backend /src/backend/THIRD_PARTY_NOTICES.md /usr/share/doc/melotrove/THIRD_PARTY_NOTICES.md
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/melotrove", "-healthcheck"]
ENTRYPOINT ["/melotrove"]
