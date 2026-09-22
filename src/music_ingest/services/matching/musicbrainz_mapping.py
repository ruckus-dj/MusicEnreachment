from __future__ import annotations

from dataclasses import replace

from music_ingest.contracts import LabelInfo, Release
from music_ingest.services.matching.providers import ReleaseCandidate


def candidate_for_release(
    release: Release,
    recording_mbid: str,
    artist_name: str | None = None,
    musicbrainz_score: float | None = None,
) -> ReleaseCandidate | None:
    release_tracks = tuple(track for medium in release.media for track in medium.tracks)
    track = next((track for track in release_tracks if track.recording.id == recording_mbid), None)
    data_track_recording_ids = frozenset(track.recording.id for medium in release.media for track in medium.data_tracks)
    if track is None and recording_mbid in data_track_recording_ids:
        return None
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
        recording_mbids=(recording_mbid,),
        recording_title=None if track is None else track.title or track.recording.title,
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
        musicbrainz_score=(
            None
            if musicbrainz_score is None
            else musicbrainz_score * 100
            if 0 <= musicbrainz_score <= 1
            else musicbrainz_score
        ),
    )


def merge_recording_candidate(existing: ReleaseCandidate, candidate: ReleaseCandidate) -> ReleaseCandidate:
    projections = existing.recording_candidates or (existing,)
    candidate_projections = candidate.recording_candidates or (candidate,)
    return replace(
        existing,
        recording_mbids=tuple(dict.fromkeys((*existing.recording_mbids, *candidate.recording_mbids))),
        recording_candidates=tuple(dict.fromkeys((*projections, *candidate_projections))),
    )


def select_genres(
    track_genres: tuple[str, ...], album_genres: tuple[str, ...], artist_genres: tuple[str, ...]
) -> tuple[str, ...]:
    for genres in (track_genres, album_genres, artist_genres):
        selected = tuple(dict.fromkeys(genre.strip() for genre in genres if genre.strip()))
        if selected:
            return selected
    return ()


def catalog_numbers(release: Release) -> tuple[str, ...]:
    label_info: tuple[LabelInfo, ...] = release.label_info
    return tuple(number for number in (release.barcode, *(item.catalog_number for item in label_info)) if number)
