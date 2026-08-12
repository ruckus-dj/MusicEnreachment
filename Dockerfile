FROM ghcr.io/astral-sh/uv:0.11.32 AS uv

FROM node:24-bookworm-slim AS frontend

WORKDIR /app/frontend

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend ./
RUN npm run build

FROM python:3.14-slim-trixie

COPY --from=uv /uv /uvx /bin/

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ffmpeg \
        libchromaprint-tools \
    && rm -rf /var/lib/apt/lists/*

RUN set -eu; \
    test -x /usr/bin/fpcalc; \
    /usr/bin/fpcalc -version; \
    ffprobe -version; \
    ffmpeg -hide_banner -loglevel error -f lavfi -i anoisesrc=color=white:sample_rate=44100 -t 10 -c:a pcm_s16le /tmp/fingerprint-smoke.wav; \
    set +e; /usr/bin/fpcalc -json -length 1 /tmp/fingerprint-smoke.wav >/tmp/fpcalc-short.out 2>/tmp/fpcalc-short.err; short_status=$?; set -e; \
    test "$short_status" -eq 2; \
    test "$(tr -d '\r\n' </tmp/fpcalc-short.err)" = 'ERROR: Empty fingerprint'; \
    /usr/bin/fpcalc -json -length 10 /tmp/fingerprint-smoke.wav >/tmp/fpcalc.json; \
    python3 -c 'import json; from pathlib import Path; payload = json.loads(Path("/tmp/fpcalc.json").read_text()); assert payload["duration"] > 0 and payload["fingerprint"]'; \
    rm /tmp/fingerprint-smoke.wav /tmp/fpcalc-short.out /tmp/fpcalc-short.err /tmp/fpcalc.json

WORKDIR /app

ENV FPCALC=/usr/bin/fpcalc \
    PATH=/app/.venv/bin:$PATH \
    PYTHONPATH=/app/src \
    UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy

COPY pyproject.toml uv.lock ./
RUN uv sync --locked --no-dev --no-install-project

COPY alembic.ini ./
COPY alembic ./alembic
COPY src/music_ingest ./src/music_ingest
COPY --from=frontend /app/src/music_ingest/ui/dist ./src/music_ingest/ui/dist

HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=2)"

CMD ["python", "-m", "music_ingest", "serve"]
