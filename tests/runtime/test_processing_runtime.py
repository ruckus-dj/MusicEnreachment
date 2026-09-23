from datetime import UTC, datetime

from music_ingest.workers.runtime import ProcessingRuntimeMonitor


def test_runtime_monitor_keeps_latest_slot_state() -> None:
    monitor = ProcessingRuntimeMonitor()

    monitor.observe(2, 'processing', job_id='job-1', job_kind='filesystem_scan')
    monitor.observe(2, 'idle')

    snapshots = monitor.snapshots()

    assert len(snapshots) == 1
    assert snapshots[0].slot == 2
    assert snapshots[0].state == 'idle'
    assert snapshots[0].job_id is None
    assert snapshots[0].job_kind is None
    assert snapshots[0].observed_at <= datetime.now(UTC)


def test_worker_pools_cover_each_kind_exactly_once() -> None:
    from music_ingest.contracts.settings import WorkerPoolSettings
    from music_ingest.workers.runtime import WORKER_POOLS

    assert set(WORKER_POOLS) == set(WorkerPoolSettings.model_fields)
    kinds = [kind for allowed in WORKER_POOLS.values() for kind in allowed]
    assert len(kinds) == len(set(kinds))
    assert set(kinds) == {
        'filesystem_scan',
        'acoustid_analysis',
        'musicbrainz_analysis',
        'candidate_selection',
        'folder_release_selection',
        'final_publish',
        'selection_refresh',
        'lrclib_fetch',
        'artwork_enrichment',
        'reconciliation_scan',
    }
    for pool, allowed in WORKER_POOLS.items():
        assert allowed == frozenset({pool})
