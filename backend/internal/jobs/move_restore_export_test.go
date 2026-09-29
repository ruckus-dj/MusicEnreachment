package jobs

// Expose ownership checkpoints to construct exact process-stop images.
// Recovery itself is always exercised through InstallationWorker.Work.
var RecordMoveSourcesForTest = recordMoveSources

var LoadMoveRestoreRecordForTest = loadOldSourceRestoreRecord

var SaveMoveRestoreRecordForTest = saveOldSourceRestoreRecord

var MoveRestorePathsForTest = oldSourceRestorePaths

var InspectMoveRestoreIdentityForTest = inspectMoveRestoreIdentity
