package jobs

import (
	"reflect"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// periodicJobField walks River's unexported periodic job fields so a schedule
// assertion can inspect the constructed job without waiting for a real minute.
func periodicJobField(t *testing.T, value reflect.Value, names ...string) reflect.Value {
	t.Helper()
	for _, name := range names {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			t.Fatalf("inspect periodic job field %s: kind %s is not a struct", name, value.Kind())
		}
		field := value.FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("inspect periodic job field %s: field is absent", name)
		}
		value = field
	}
	return value
}

// TestPublicationReconciliationDispatchPeriodicJobSchedule pins the dispatch to
// its own minute cadence instead of the shared hourly housekeeping interval,
// and keeps insertion unique while an earlier dispatch is still active. Pending
// publication reconciliations wait for this dispatch, so the hourly cadence left
// them undispatched for up to an hour after startup.
func TestPublicationReconciliationDispatchPeriodicJobSchedule(t *testing.T) {
	periodic := NewPublicationReconciliationDispatchPeriodicJob()
	value := reflect.ValueOf(periodic)

	interval := time.Duration(periodicJobField(t, value, "scheduleFunc", "interval").Int())
	if interval != time.Minute {
		t.Fatalf("publication reconciliation dispatch interval = %s, want %s", interval, time.Minute)
	}
	if interval >= infrastructurePeriodicInterval {
		t.Fatalf("publication reconciliation dispatch interval = %s, want less than the shared interval %s", interval, infrastructurePeriodicInterval)
	}

	opts := periodicJobField(t, value, "opts", "ID")
	if id := opts.String(); id != "publication-reconciliation-dispatch" {
		t.Fatalf("publication reconciliation dispatch periodic job ID = %q, want %q", id, "publication-reconciliation-dispatch")
	}
	if !periodicJobField(t, value, "opts", "RunOnStart").Bool() {
		t.Fatalf("publication reconciliation dispatch periodic job does not run on start")
	}
	constructor := periodicJobField(t, value, "constructorFunc")
	if constructor.IsNil() {
		t.Fatalf("publication reconciliation dispatch periodic job has no constructor")
	}
	if constructor.Pointer() != reflect.ValueOf(publicationReconciliationDispatchInsert).Pointer() {
		t.Fatalf("publication reconciliation dispatch periodic job does not insert its unique dispatch args")
	}
}

// TestPublicationReconciliationDispatchInsertIsUnique proves the active-job
// uniqueness of the dispatch: duplicates collapse while one dispatch is queued
// or running, and the args stay the pinned kind.
func TestPublicationReconciliationDispatchInsertIsUnique(t *testing.T) {
	args, opts := publicationReconciliationDispatchInsert()
	if _, ok := args.(PublicationReconciliationDispatchArgs); !ok {
		t.Fatalf("publication reconciliation dispatch args = %T, want %T", args, PublicationReconciliationDispatchArgs{})
	}
	if args.Kind() != PublicationReconciliationDispatchJobKind {
		t.Fatalf("publication reconciliation dispatch kind = %q, want %q", args.Kind(), PublicationReconciliationDispatchJobKind)
	}
	if opts == nil || !opts.UniqueOpts.ByArgs {
		t.Fatalf("publication reconciliation dispatch insert is not unique by args")
	}
	want := []rivertype.JobState{
		rivertype.JobStateAvailable,
		rivertype.JobStatePending,
		rivertype.JobStateRunning,
		rivertype.JobStateRetryable,
		rivertype.JobStateScheduled,
	}
	if !reflect.DeepEqual(opts.UniqueOpts.ByState, want) {
		t.Fatalf("publication reconciliation dispatch unique states = %v, want %v", opts.UniqueOpts.ByState, want)
	}
}

var _ river.JobArgs = PublicationReconciliationDispatchArgs{}
