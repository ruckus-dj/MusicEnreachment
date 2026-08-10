"""Database boundary: SQLAlchemy metadata, entities, and entity queries."""

from music_ingest.models.db import Base
from music_ingest.models.entities import (
    ArtworkRecord,
    CandidateRecord,
    FingerprintRecord,
    GenreCatalogRecord,
    JobAttemptRecord,
    JobRecord,
    ProviderAttemptRecord,
    ProviderScheduleRecord,
    ProviderSnapshotRecord,
    ReviewDecisionRecord,
    RuntimeSettingRecord,
    SourceRecord,
    SourceTagRecord,
    WebhookReceiptRecord,
)
from music_ingest.models.library import (
    LibraryEventRecord,
    LibraryMetadataRevisionRecord,
    LibraryPublicationRecord,
    LibraryRecord,
)

__all__ = [
    'ArtworkRecord',
    'Base',
    'CandidateRecord',
    'FingerprintRecord',
    'GenreCatalogRecord',
    'JobAttemptRecord',
    'JobRecord',
    'LibraryEventRecord',
    'LibraryMetadataRevisionRecord',
    'LibraryPublicationRecord',
    'LibraryRecord',
    'ProviderAttemptRecord',
    'ProviderScheduleRecord',
    'ProviderSnapshotRecord',
    'ReviewDecisionRecord',
    'RuntimeSettingRecord',
    'SourceRecord',
    'SourceTagRecord',
    'WebhookReceiptRecord',
]
