FROM node:26-bookworm-slim AS frontend

WORKDIR /app/frontend

COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY frontend ./
RUN npm run build

FROM ghcr.io/astral-sh/uv:python3.14-trixie-slim

ARG TARGETARCH

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ca-certificates \
        curl \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN set -eu; \
    case "$TARGETARCH" in \
        amd64) ffmpeg_arch='64' ;; \
        arm64) ffmpeg_arch='arm64' ;; \
        *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    ffmpeg_archive="ffmpeg-master-latest-linux${ffmpeg_arch}-gpl.tar.xz"; \
    curl --fail --silent --show-error --location \
        --output "/tmp/$ffmpeg_archive" \
        "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/$ffmpeg_archive"; \
    curl --fail --silent --show-error --location \
        --output /tmp/ffmpeg-checksums.sha256 \
        'https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/checksums.sha256'; \
    (cd /tmp && grep "  $ffmpeg_archive$" ffmpeg-checksums.sha256 | sha256sum --check --status); \
    mkdir --parents /tmp/ffmpeg; \
    tar --extract --file "/tmp/$ffmpeg_archive" --directory /tmp/ffmpeg --strip-components=1; \
    install --mode=0755 /tmp/ffmpeg/bin/ffmpeg /usr/local/bin/ffmpeg; \
    install --mode=0755 /tmp/ffmpeg/bin/ffprobe /usr/local/bin/ffprobe; \
    rm -rf /tmp/$ffmpeg_archive /tmp/ffmpeg /tmp/ffmpeg-checksums.sha256; \
    case "$TARGETARCH" in \
        amd64) fpcalc_archive='chromaprint-fpcalc-1.6.1-linux-x86_64.tar.gz'; fpcalc_sha256='fc16cd37a70168040bc9ceb45f1d4d1216f5a75bc4c9cf8564bea70ac6a45733' ;; \
        arm64) fpcalc_archive='chromaprint-fpcalc-1.6.1-linux-arm64.tar.gz'; fpcalc_sha256='7eaf5d655c4aa172ab28e3c870b8bb61dd2c327ac94de145676f88842cf6215a' ;; \
        *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl --fail --silent --show-error --location \
        --output "/tmp/$fpcalc_archive" \
        "https://github.com/acoustid/chromaprint/releases/download/v1.6.1/$fpcalc_archive"; \
    printf '%s  %s\n' "$fpcalc_sha256" "/tmp/$fpcalc_archive" | sha256sum --check --status; \
    mkdir --parents /tmp/fpcalc; \
    tar --extract --file "/tmp/$fpcalc_archive" --directory /tmp/fpcalc --strip-components=1; \
    install --mode=0755 /tmp/fpcalc/fpcalc /usr/local/bin/fpcalc; \
    rm -rf /tmp/$fpcalc_archive /tmp/fpcalc

RUN set -eu; \
    test -x /usr/local/bin/fpcalc; \
    /usr/local/bin/fpcalc -version; \
    ffprobe -version; \
    ffmpeg -hide_banner -loglevel error -f lavfi -i anoisesrc=color=white:sample_rate=44100 -t 10 -c:a pcm_s16le /tmp/fingerprint-smoke.wav; \
    set +e; /usr/local/bin/fpcalc -json -length 1 /tmp/fingerprint-smoke.wav >/tmp/fpcalc-short.out 2>/tmp/fpcalc-short.err; short_status=$?; set -e; \
    test "$short_status" -eq 2; \
    test "$(tr -d '\r\n' </tmp/fpcalc-short.err)" = 'ERROR: Empty fingerprint'; \
    /usr/local/bin/fpcalc -json -length 10 /tmp/fingerprint-smoke.wav >/tmp/fpcalc.json; \
    python3 -c 'import json; from pathlib import Path; payload = json.loads(Path("/tmp/fpcalc.json").read_text()); assert payload["duration"] > 0 and payload["fingerprint"]'; \
    rm /tmp/fingerprint-smoke.wav /tmp/fpcalc-short.out /tmp/fpcalc-short.err /tmp/fpcalc.json

WORKDIR /app

ENV FPCALC=/usr/local/bin/fpcalc \
    PATH=/app/.venv/bin:$PATH \
    PYTHONPATH=/app/src \
    UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy

COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --locked --no-dev --no-install-project

COPY alembic.ini ./
COPY alembic ./alembic
COPY src/music_ingest ./src/music_ingest
COPY --from=frontend /app/src/music_ingest/static/dist ./src/music_ingest/static/dist

HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=2)"

CMD ["python", "-m", "music_ingest", "serve"]
