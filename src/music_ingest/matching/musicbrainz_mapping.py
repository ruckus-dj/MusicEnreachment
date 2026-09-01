from __future__ import annotations

from dataclasses import replace
from unicodedata import normalize

from rapidfuzz.fuzz import ratio

from music_ingest.dto import LabelInfo, Release
from music_ingest.dto.api import Track
from music_ingest.matching.providers import MusicBrainzLookupRequest, ReleaseCandidate


def candidate_for_release(
    release: Release,
    artist_name: str | None = None,
    recording_mbid: str | None = None,
    recording_title: str | None = None,
    duration_seconds: int | None = None,
    track_number: int | None = None,
) -> ReleaseCandidate:
    release_tracks = tuple(track for medium in release.media for track in medium.tracks)
    if recording_mbid is not None:
        track = next((track for track in release_tracks if track.recording.id == recording_mbid), None)
    elif recording_title is not None:
        track = max(
            release_tracks,
            key=lambda item: track_match_score(item, recording_title, duration_seconds, track_number),
            default=None,
        )
    else:
        track = release_tracks[0] if len(release_tracks) == 1 else None
    release_artist_name = ''.join(f'{item.name}{item.joinphrase}' for item in release.artist_credit)
    artist = artist_name or release_artist_name
    if not artist and track is not None:
        artist = ''.join(f'{item.name}{item.joinphrase}' for item in track.recording.artist_credit)
    medium = next((medium for medium in release.media if track is not None and track in medium.tracks), None)
    track_total = medium.track_count if medium is not None else None
    if medium is not None and track_total is None:
        track_total = len(medium.tracks)
    return ReleaseCandidate(
        release.id,
        release.title,
        artist,
        disambiguation=release.disambiguation,
        duration_seconds=None if track is None or track.length is None else round(track.length / 1000),
        recording_mbids=_recording_mbids(recording_mbid, recording_title, track, release_tracks),
        recording_title=None if track is None else track.recording.title,
        date=release.date,
        original_date=None if release.release_group is None else release.release_group.first_release_date,
        country=release.country,
        track_number=None if track is None else track.position,
        track_total=track_total,
        disc_number=None if medium is None else medium.position,
        disc_total=len(release.media) if release.media else None,
        genres=select_genres(
            tuple(genre.name for genre in track.recording.genres) if track is not None else (),
            tuple(genre.name for genre in release.genres),
            tuple(
                genre.name
                for credit in release.artist_credit
                for artist_credit in (credit.artist,)
                if artist_credit is not None
                for genre in artist_credit.genres
            ),
        ),
        release_group_mbid=None if release.release_group is None else release.release_group.id,
        isrcs=() if track is None else track.recording.isrcs,
        performers=()
        if track is None
        else tuple(
            relation.artist.name
            for relation in track.recording.relations
            if relation.target_type == 'artist'
            and relation.type in {'performer', 'vocal', 'instrument'}
            and relation.artist is not None
        ),
        release_artist_name=release_artist_name or None,
        recording_artist_names=() if track is None else tuple(credit.name for credit in track.recording.artist_credit),
        release_artist_names=tuple(credit.name for credit in release.artist_credit),
        recording_artist_mbids=()
        if track is None or any(credit.artist is None for credit in track.recording.artist_credit)
        else tuple(credit.artist.id for credit in track.recording.artist_credit if credit.artist is not None),
        release_artist_mbids=()
        if any(credit.artist is None for credit in release.artist_credit)
        else tuple(credit.artist.id for credit in release.artist_credit if credit.artist is not None),
        catalog_numbers=catalog_numbers(release),
    )


def without_pseudo_releases(releases: tuple[Release, ...]) -> tuple[Release, ...]:
    return tuple(release for release in releases if (release.status or '').casefold() != 'pseudo-release')


def merge_recording_candidate(
    existing: ReleaseCandidate, candidate: ReleaseCandidate, request: MusicBrainzLookupRequest
) -> ReleaseCandidate:
    if request.recording_title is not None and recording_match_score(candidate, request) > recording_match_score(
        existing, request
    ):
        return candidate
    return replace(
        existing, recording_mbids=tuple(dict.fromkeys((*existing.recording_mbids, *candidate.recording_mbids)))
    )


def select_genres(
    track_genres: tuple[str, ...], album_genres: tuple[str, ...], artist_genres: tuple[str, ...]
) -> tuple[str, ...]:
    for genres in (track_genres, album_genres, artist_genres):
        selected = tuple(dict.fromkeys(genre.strip() for genre in genres if genre.strip()))
        if selected:
            return selected
    return ()


def title_key(value: str) -> str:
    return ''.join(character for character in normalize('NFKC', value).casefold() if character.isalnum())


def track_match_score(track: Track, title: str, duration_seconds: int | None, track_number: int | None) -> float:
    title_score = ratio(title_key(title), title_key(track.recording.title)) / 100
    duration_score = (
        0.0
        if duration_seconds is None or track.length is None
        else max(0.0, 1.0 - abs(duration_seconds - round(track.length / 1000)) / 10)
    )
    number_score = 1.0 if track_number is not None and track.position == track_number else 0.0
    return 0.6 * title_score + 0.25 * duration_score + 0.15 * number_score


def recording_match_score(candidate: ReleaseCandidate, request: MusicBrainzLookupRequest) -> float:
    title_score = ratio(title_key(request.recording_title or ''), title_key(candidate.recording_title or '')) / 100
    duration_score = (
        0.0
        if request.duration_seconds is None or candidate.duration_seconds is None
        else max(0.0, 1.0 - abs(request.duration_seconds - candidate.duration_seconds) / 10)
    )
    number_score = 1.0 if request.track_number is not None and candidate.track_number == request.track_number else 0.0
    return 0.6 * title_score + 0.25 * duration_score + 0.15 * number_score


def catalog_numbers(release: Release) -> tuple[str, ...]:
    label_info: tuple[LabelInfo, ...] = release.label_info
    return tuple(number for number in (release.barcode, *(item.catalog_number for item in label_info)) if number)


def _recording_mbids(
    recording_mbid: str | None, recording_title: str | None, track: Track | None, release_tracks: tuple[Track, ...]
) -> tuple[str, ...]:
    if recording_mbid is not None:
        return (recording_mbid,)
    if recording_title is not None and track is not None:
        return (track.recording.id,)
    if len(release_tracks) == 1 and track is not None:
        return (track.recording.id,)
    return ()
