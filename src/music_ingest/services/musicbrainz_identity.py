from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol


class MusicBrainzIdentityRecord(Protocol):
    musicbrainz_recording_id: str | None
    musicbrainz_release_id: str | None


@dataclass(frozen=True, slots=True)
class ConfirmedMusicBrainzIdentity:
    recording_mbid: str
    release_mbid: str


def confirmed_musicbrainz_identity(record: MusicBrainzIdentityRecord) -> ConfirmedMusicBrainzIdentity | None:
    recording_mbid = record.musicbrainz_recording_id
    release_mbid = record.musicbrainz_release_id
    if recording_mbid is None or release_mbid is None:
        return None
    normalized_recording = recording_mbid.strip()
    normalized_release = release_mbid.strip()
    if not normalized_recording or not normalized_release:
        return None
    return ConfirmedMusicBrainzIdentity(normalized_recording, normalized_release)


def tags_match_musicbrainz_identity(tags: dict[str, str], identity: ConfirmedMusicBrainzIdentity) -> bool:
    return (
        tags.get('MUSICBRAINZ_RECORDINGID', '').strip() == identity.recording_mbid
        and tags.get('MUSICBRAINZ_ALBUMID', '').strip() == identity.release_mbid
    )


def tags_match_persisted_musicbrainz_identity(tags: dict[str, str], record: MusicBrainzIdentityRecord) -> bool:
    identity = confirmed_musicbrainz_identity(record)
    if identity is not None:
        return tags_match_musicbrainz_identity(tags, identity)
    persisted_identity_is_empty = not (
        (record.musicbrainz_recording_id or '').strip() or (record.musicbrainz_release_id or '').strip()
    )
    tagged_identity_is_empty = not (
        tags.get('MUSICBRAINZ_RECORDINGID', '').strip() or tags.get('MUSICBRAINZ_ALBUMID', '').strip()
    )
    return persisted_identity_is_empty and tagged_identity_is_empty
