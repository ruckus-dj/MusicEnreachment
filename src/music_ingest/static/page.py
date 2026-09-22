from __future__ import annotations

from pathlib import Path

_BUILD_INDEX = Path(__file__).with_name('dist') / 'index.html'
_FALLBACK_PAGE = """<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Медиатека · Music Ingest</title></head><body><main><h1>Медиатека</h1>
<p>Соберите frontend командой <code>npm --prefix frontend run build</code>.</p></main></body></html>"""


def review_page() -> str:
    if _BUILD_INDEX.is_file():
        return _BUILD_INDEX.read_text(encoding='utf-8')
    return _FALLBACK_PAGE


REVIEW_PAGE = review_page()
