from __future__ import annotations

import json
from datetime import UTC, datetime

from music_ingest.models import LibraryMetadataRevisionRecord, LibraryRecord
from music_ingest.workers.handlers import selection


def test_final_metadata_changed_compares_latest_source_revision() -> None:
    # Given: the latest Final revision already contains canonical provider metadata.
    now = datetime(2026, 9, 23, tzinfo=UTC)
    record = LibraryRecord(
        id='record-id',
        created_at=now,
        updated_at=now,
        metadata_revisions=[
            LibraryMetadataRevisionRecord(
                library_record_id='record-id',
                source_id='source-id',
                layer='final',
                revision=1,
                tags_json=json.dumps({'ARTIST': 'Canonical Artist'}, sort_keys=True),
                actor='folder_selection',
                created_at=now,
            )
        ],
    )

    # When: refreshed tags are equal or different.
    unchanged = selection.final_metadata_changed(record, 'source-id', {'ARTIST': 'Canonical Artist'})
    changed = selection.final_metadata_changed(record, 'source-id', {'ARTIST': 'Renamed Artist'})

    # Then: only a real tag difference requires a new revision and publication.
    assert not unchanged
    assert changed
