from __future__ import annotations

import json
import os
import shutil
import tempfile
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from subprocess import run
from typing import Final, override

from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from music_ingest.persistence.models import (
    AuditRecord,
    CandidateRecord,
    PublicationStateRecord,
    ReleaseFileRecord,
    ReleaseRecord,
    SourceRecord,
    TagLayerRecord,
    TrackRecord,
)

_ALLOWED_TAGS: Final = frozenset(
    {
        'TITLE',
        'ARTIST',
        'ALBUM',
        'ALBUMARTIST',
        'DATE',
        'ORIGINALDATE',
        'TRACKNUMBER',
        'TRACKTOTAL',
        'DISCNUMBER',
        'DISCTOTAL',
        'GENRE',
        'MUSICBRAINZ_TRACKID',
        'MUSICBRAINZ_ALBUMID',
        'MUSICBRAINZ_RELEASEGROUPID',
        'ISRC',
    }
)


@dataclass(frozen=True, slots=True)
class ReleaseReviewConflict(RuntimeError):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


@dataclass(frozen=True, slots=True)
class ReleaseReviewInputError(ValueError):
    reason: str

    @override
    def __str__(self) -> str:
        return self.reason


def queue(session: Session) -> list[dict[str, str]]:
    return [_queue_item(release) for release in _releases(session)]


def detail(session: Session, release_id: str) -> dict[str, object]:
    release = _release(session, release_id)
    tracks: list[dict[str, object]] = []
    candidates: list[dict[str, object]] = []
    review_states: set[str] = set()
    for track in release.tracks:
        files: list[dict[str, object]] = []
        for release_file in track.files:
            layers = _layers(release_file.tag_layers)
            files.append(
                {
                    'file_id': release_file.id,
                    'relative_path': release_file.relative_path,
                    'layers': layers,
                    'final_revision': _final_revision(release_file.tag_layers),
                }
            )
            if release_file.source is not None:
                review_states.add(release_file.source.intake_state)
                candidates.extend(_candidate_payload(candidate) for candidate in release_file.source.candidates)
        if len(files) == 1:
            tracks.append({'track_id': track.id, 'position': track.position, 'title': track.title, **files[0]})
        else:
            tracks.append({'track_id': track.id, 'position': track.position, 'title': track.title, 'files': files})
    return {
        'release_id': release.id,
        'title': release.title,
        'incoming_folder': _incoming_folder(release),
        'publication': {'state': _publication_state(release)},
        'review_state': 'needs_review' if 'needs_review' in review_states else 'published',
        'tracks': tracks,
        'candidates': candidates,
        'audit': [
            {'action': audit.action, 'actor': audit.actor, 'details': json.loads(audit.details_json)}
            for audit in release.audits
        ],
    }


def edit_track(
    session: Session, track_id: str, revision: int, tags: dict[str, str], media_root: Path | None = None
) -> int:
    track = session.scalar(
        select(TrackRecord)
        .where(TrackRecord.id == track_id)
        .options(
            selectinload(TrackRecord.release).selectinload(ReleaseRecord.publication),
            selectinload(TrackRecord.release).selectinload(ReleaseRecord.audits),
            selectinload(TrackRecord.files).selectinload(ReleaseFileRecord.tag_layers),
        )
    )
    if track is None or len(track.files) != 1:
        raise LookupError(track_id)
    release_file = track.files[0]
    current_revision = _final_revision(release_file.tag_layers)
    if revision != current_revision:
        raise ReleaseReviewConflict('stale track revision')
    _validate_tags(tags)
    new_revision = revision + 1
    current = _latest_final_tags(release_file.tag_layers)
    merged = {**current, **tags}
    session.add(
        TagLayerRecord(
            release_file_id=release_file.id,
            layer='final',
            revision=new_revision,
            tags_json=_dump(merged),
            recorded_at=datetime.now(UTC),
        )
    )
    _audit(session, track.release, 'track_edited', {'from_revision': revision, 'to_revision': new_revision})
    if media_root is not None:
        _rewrite_media_tags(media_root / release_file.relative_path, merged)
    _mark_published(session, track.release)
    session.flush()
    return new_revision


def edit(session: Session, release_id: str, revision: int, tags: dict[str, str], media_root: Path | None = None) -> int:
    release = _release(session, release_id)
    _assert_revision(release, revision)
    _validate_tags(tags)
    new_revision = revision + 1
    for release_file in _release_files(release):
        current = _latest_final_tags(release_file.tag_layers)
        session.add(
            TagLayerRecord(
                release_file_id=release_file.id,
                layer='final',
                revision=new_revision,
                tags_json=_dump({**current, **tags}),
                recorded_at=datetime.now(UTC),
            )
        )
    _audit(session, release, 'edited', {'from_revision': revision, 'to_revision': new_revision})
    _republish(session, release, new_revision, media_root)
    session.flush()
    return new_revision


def republish(session: Session, release_id: str, revision: int, media_root: Path | None = None) -> None:
    release = _release(session, release_id)
    _assert_revision(release, revision)
    _republish(session, release, revision, media_root)
    session.flush()


def rollback(session: Session, release_id: str, revision: int, media_root: Path | None = None) -> int:
    release = _release(session, release_id)
    current_revision = _release_revision(release)
    if revision < 1 or revision > current_revision:
        raise ReleaseReviewInputError('rollback revision does not exist')
    new_revision = current_revision + 1
    for release_file in _release_files(release):
        target = _tags_at_revision(release_file.tag_layers, revision)
        session.add(
            TagLayerRecord(
                release_file_id=release_file.id,
                layer='final',
                revision=new_revision,
                tags_json=_dump(target),
                recorded_at=datetime.now(UTC),
            )
        )
    _audit(session, release, 'rolled_back', {'from_revision': current_revision, 'restored_revision': revision})
    _republish(session, release, new_revision, media_root)
    session.flush()
    return new_revision


def _releases(session: Session) -> list[ReleaseRecord]:
    return list(session.scalars(_release_query().order_by(ReleaseRecord.id)).unique().all())


def _release(session: Session, release_id: str) -> ReleaseRecord:
    release = session.scalar(_release_query().where(ReleaseRecord.id == release_id))
    if release is None:
        raise LookupError(release_id)
    return release


def _release_query():
    return select(ReleaseRecord).options(
        selectinload(ReleaseRecord.publication),
        selectinload(ReleaseRecord.audits),
        selectinload(ReleaseRecord.tracks).selectinload(TrackRecord.files).selectinload(ReleaseFileRecord.tag_layers),
        selectinload(ReleaseRecord.tracks)
        .selectinload(TrackRecord.files)
        .selectinload(ReleaseFileRecord.source)
        .selectinload(SourceRecord.candidates),
    )


def _queue_item(release: ReleaseRecord) -> dict[str, str]:
    states = {
        release_file.source.intake_state for release_file in _release_files(release) if release_file.source is not None
    }
    first_file = next(iter(_release_files(release)), None)
    final_tags = _latest_final_tags(first_file.tag_layers) if first_file is not None else {}
    return {
        'release_id': release.id,
        'title': release.title,
        'artist': final_tags.get('ARTIST', ''),
        'album': final_tags.get('ALBUM', release.title),
        'incoming_folder': _incoming_folder(release),
        'publication_state': _publication_state(release),
        'review_state': 'needs_review' if 'needs_review' in states else 'published',
    }


def _incoming_folder(release: ReleaseRecord) -> str:
    paths = [
        Path(release_file.source.source_path).parent for release_file in _release_files(release) if release_file.source
    ]
    return str(sorted(paths)[0]) if paths else ''


def _release_files(release: ReleaseRecord):
    return [release_file for track in release.tracks for release_file in track.files]


def _publication_state(release: ReleaseRecord) -> str:
    return release.publication.state if release.publication is not None else 'unpublished'


def _layers(tag_layers: list[TagLayerRecord]) -> dict[str, dict[str, str]]:
    return {layer: _latest_tags(tag_layers, layer) for layer in ('original', 'analyzed', 'final')}


def _latest_tags(tag_layers: list[TagLayerRecord], layer: str) -> dict[str, str]:
    matches = [item for item in tag_layers if item.layer == layer]
    return _tags(matches[-1]) if matches else {}


def _latest_final_tags(tag_layers: list[TagLayerRecord]) -> dict[str, str]:
    return _latest_tags(tag_layers, 'final')


def _tags_at_revision(tag_layers: list[TagLayerRecord], revision: int) -> dict[str, str]:
    for item in tag_layers:
        if item.layer == 'final' and item.revision == revision:
            return _tags(item)
    raise ReleaseReviewInputError('rollback revision does not exist')


def _tags(layer: TagLayerRecord) -> dict[str, str]:
    decoded = json.loads(layer.tags_json)
    return {str(key): str(value) for key, value in decoded.items()}


def _final_revision(tag_layers: list[TagLayerRecord]) -> int:
    return max((item.revision for item in tag_layers if item.layer == 'final'), default=0)


def _release_revision(release: ReleaseRecord) -> int:
    return min(_final_revision(release_file.tag_layers) for release_file in _release_files(release))


def _assert_revision(release: ReleaseRecord, revision: int) -> None:
    if revision != _release_revision(release):
        raise ReleaseReviewConflict('stale release revision')


def _validate_tags(tags: dict[str, str]) -> None:
    if not tags or not set(tags).issubset(_ALLOWED_TAGS):
        raise ReleaseReviewInputError('tags contain unsupported canonical fields')
    if any(not value.strip() or any(ord(character) < 32 for character in value) for value in tags.values()):
        raise ReleaseReviewInputError('tags must contain non-empty printable values')
    for number_field in ('TRACKNUMBER', 'TRACKTOTAL', 'DISCNUMBER', 'DISCTOTAL'):
        value = tags.get(number_field)
        if value is not None and (not value.isdecimal() or int(value) < 1):
            raise ReleaseReviewInputError('track and disc numbers must be positive integers')


def _audit(session: Session, release: ReleaseRecord, action: str, details: dict[str, int]) -> None:
    session.add(
        AuditRecord(
            release_id=release.id,
            action=action,
            actor='local-reviewer',
            details_json=_dump(details),
            recorded_at=datetime.now(UTC),
        )
    )


def _republish(session: Session, release: ReleaseRecord, revision: int, media_root: Path | None = None) -> None:
    if media_root is not None:
        for release_file in _release_files(release):
            _rewrite_media_tags(media_root / release_file.relative_path, _latest_final_tags(release_file.tag_layers))
    _mark_published(session, release)
    _audit(session, release, 'republished', {'revision': revision})


def _mark_published(session: Session, release: ReleaseRecord) -> None:
    publication = release.publication
    if publication is None:
        publication = PublicationStateRecord(release_id=release.id, state='published', updated_at=datetime.now(UTC))
        session.add(publication)
    else:
        publication.state = 'published'
        publication.updated_at = datetime.now(UTC)


def _rewrite_media_tags(path: Path, tags: dict[str, str]) -> None:
    if path.is_dir():
        audio_paths = tuple(path.glob('*.flac'))
        if len(audio_paths) != 1:
            raise LookupError(path)
        path = audio_paths[0]
    if not path.is_file():
        raise LookupError(path)
    descriptor, temporary_name = tempfile.mkstemp(prefix='.republish-', suffix='.flac', dir=path.parent)
    os.close(descriptor)
    temporary = Path(temporary_name)
    try:
        shutil.copy2(path, temporary)
        command = (
            'metaflac',
            '--remove-all-tags',
            *(f'--set-tag={key}={value}' for key, value in tags.items()),
            str(temporary),
        )
        result = run(command, capture_output=True, check=False, text=True, timeout=30)  # noqa: S603
        if result.returncode != 0:
            raise OSError('metaflac rejected republished tags')
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def _candidate_payload(candidate: CandidateRecord) -> dict[str, object]:
    try:
        evidence = json.loads(candidate.evidence)
    except json.JSONDecodeError:
        evidence = {'raw': candidate.evidence}
    confidence = evidence.get('confidence')
    return {'key': candidate.candidate_key, 'evidence': evidence, 'confidence': confidence}


def _dump(value: dict[str, str] | dict[str, int]) -> str:
    return json.dumps(value, sort_keys=True, separators=(',', ':'))
