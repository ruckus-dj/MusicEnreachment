from __future__ import annotations

import os
import socket
from typing import Never

import pytest


def _network_denied(*_: object, **__: object) -> Never:
    raise RuntimeError('network access denied by the offline test suite')


@pytest.fixture(autouse=True)
def deny_socket_access_for_non_live_tests(request: pytest.FixtureRequest, monkeypatch: pytest.MonkeyPatch) -> None:
    if request.node.get_closest_marker('live') is not None:
        return
    monkeypatch.setattr(socket, 'create_connection', _network_denied)
    monkeypatch.setattr(socket.socket, 'connect', _network_denied)
    monkeypatch.setattr(socket.socket, 'connect_ex', _network_denied)


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    _ = config
    if os.environ.get('MUSIC_INGEST_ENABLE_LIVE_TESTS') == '1':
        return
    skip_live = pytest.mark.skip(reason='live tests require MUSIC_INGEST_ENABLE_LIVE_TESTS=1')
    for item in items:
        if item.get_closest_marker('live') is not None:
            item.add_marker(skip_live)
