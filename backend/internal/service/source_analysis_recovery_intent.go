package service

import "github.com/ruckus/MusicEnreachment/backend/internal/persistence"

// retainedSourceAnalysisIntent is an admitted step's durable input retained
// independently of the operation that originally admitted it.
type retainedSourceAnalysisIntent struct {
	Pending  persistence.SourceAnalysisPendingWork
	Step     persistence.SourceAnalysisStep
	Snapshot persistence.SourceAnalysisOperationSnapshot
}

func retainedSourceAnalysisStepIntent(pending persistence.SourceAnalysisPendingWork, step persistence.SourceAnalysisStep) (retainedSourceAnalysisIntent, error) {
	snapshot, err := persistence.DecodeRetainedSourceAnalysisStepInput(step, pending.Work)
	if err != nil {
		return retainedSourceAnalysisIntent{}, err
	}
	return retainedSourceAnalysisIntent{Pending: pending, Step: step, Snapshot: snapshot}, nil
}
