from __future__ import annotations

from fastapi import APIRouter, Response
from fastapi.responses import HTMLResponse

from music_ingest.static.page import REVIEW_PAGE


def create_router() -> APIRouter:
    router = APIRouter()

    @router.get('/healthz')
    def healthz() -> dict[str, str]:
        return {'status': 'ok', 'service': 'music-ingest'}

    @router.get('/', response_class=HTMLResponse)
    @router.get('/dashboard', response_class=HTMLResponse)
    @router.get('/review', response_class=HTMLResponse)
    @router.get('/settings', response_class=HTMLResponse)
    @router.get('/manual-actions', response_class=HTMLResponse)
    @router.get('/workers', response_class=HTMLResponse)
    def review_page() -> str:
        return REVIEW_PAGE

    @router.get('/library', response_class=HTMLResponse)
    @router.get('/library/{path:path}', response_class=HTMLResponse)
    def library_route(path: str = '') -> str:
        _ = path
        return REVIEW_PAGE

    @router.get('/favicon.ico')
    @router.get('/favicon.svg')
    def favicon() -> Response:
        return Response(status_code=204)

    return router
