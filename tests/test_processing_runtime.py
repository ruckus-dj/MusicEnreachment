from datetime import UTC, datetime

from music_ingest.processing.runtime import ProcessingRuntimeMonitor


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
