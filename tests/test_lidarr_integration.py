from __future__ import annotations

from dataclasses import dataclass, field

from music_ingest.integrations.lidarr import LidarrClient, LidarrIntakeEvent, enqueue_lidarr_intake


@dataclass
class RecordedPost:
    url: str
    headers: dict[str, str]
    json: dict[str, list[str] | str]


@dataclass
class FakeLidarrTransport:
    posts: list[RecordedPost] = field(default_factory=list)

    def post(self, url: str, *, headers: dict[str, str], json: dict[str, list[str] | str], timeout: float) -> None:
        _ = timeout
        self.posts.append(RecordedPost(url, headers, json))


def test_enqueue_lidarr_intake_when_rescan_is_required_preserves_raw_path_and_requests_command() -> None:
    # Given: Lidarr's unchanged incoming pathname and a local-only command transport.
    transport = FakeLidarrTransport()
    event = LidarrIntakeEvent(source_path='/data/incoming/Artist/Release/01.flac', source_id='source-id')

    # When: the hook queues ingestion and asks Lidarr to refresh that folder.
    queued = enqueue_lidarr_intake(event, LidarrClient('http://lidarr.local', 'fixture-key', transport))

    # Then: the raw path is preserved and the only Lidarr mutation is explicit RescanFolders.
    assert queued.source_path == event.source_path
    assert transport.posts == [
        RecordedPost(
            'http://lidarr.local/api/v1/command',
            {'X-Api-Key': 'fixture-key'},
            {'name': 'RescanFolders', 'folders': ['/data/incoming/Artist/Release']},
        )
    ]
