// Command matching-experiment compares matching calculations on frozen data.
// It has no database, network, worker, or source-file entry point.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type fixture struct {
	Version           int                                                   `json:"version"`
	Name              string                                                `json:"name"`
	Provenance        string                                                `json:"provenance"`
	ExpectedReleaseID string                                                `json:"expected_release_id"`
	BaselineSource    string                                                `json:"baseline_source"`
	BaselineObserved  json.RawMessage                                       `json:"baseline_observed,omitempty"`
	BaselineIDs       []string                                              `json:"baseline_release_ids"`
	Captures          []persistence.IncomingGroupingCapture                 `json:"captures"`
	Headers           []service.MatchingExperimentReleaseHeader             `json:"headers"`
	Layouts           map[string]json.RawMessage                            `json:"layouts"`
	MemoryTags        map[uuid.UUID]map[string][]string                     `json:"memory_tags"`
	SearchKeys        map[uuid.UUID]service.MatchingSearchKeys              `json:"search_keys"`
	SearchScores      map[string]map[string]persistence.MatchingSearchScore `json:"search_scores"`
}

type baselineDecision struct {
	Selection  service.ReleaseSelectionResult            `json:"release_selection"`
	Decision   service.MatchingGroupDecision             `json:"decision"`
	Reason     string                                    `json:"reason"`
	Outcome    service.MatchingEvaluationDecisionOutcome `json:"outcome"`
	Assignment service.MatchingAssignmentResult          `json:"assignment"`
}

type savedDecision struct {
	SelectionReason service.ReleaseSelectionReason            `json:"release_selection_reason"`
	SelectedID      string                                    `json:"selected_release_id"`
	Outcome         service.MatchingEvaluationDecisionOutcome `json:"outcome"`
	Assignments     map[string]string                         `json:"assignments"`
}

type report struct {
	Name           string                                      `json:"name"`
	Provenance     string                                      `json:"provenance"`
	Digest         string                                      `json:"fixture_sha256"`
	Policy         string                                      `json:"scoring_policy"`
	ReadyFiles     int                                         `json:"ready_files"`
	ExpectedID     string                                      `json:"expected_release_id,omitempty"`
	ExpectedStatus string                                      `json:"expected_release_status"`
	BaselineSource string                                      `json:"baseline_source"`
	Observed       json.RawMessage                             `json:"baseline_observed,omitempty"`
	BaselineIDs    []string                                    `json:"baseline_release_ids"`
	Baseline       *baselineDecision                           `json:"baseline,omitempty"`
	BaselineMatch  *bool                                       `json:"baseline_matches_observed,omitempty"`
	Ranking        []service.MatchingExperimentReleaseRank     `json:"ranked_headers"`
	NotHydrated    []string                                    `json:"shortlist_without_frozen_tracklists,omitempty"`
	LayoutScope    string                                      `json:"layout_scope,omitempty"`
	ReleaseFirst   *baselineDecision                           `json:"release_first_on_shortlist,omitempty"`
	Compared       *service.MatchingExperimentLayoutComparison `json:"whole_layouts,omitempty"`
	MemoryControl  *service.MatchingExperimentLayoutComparison `json:"corrected_tags_control,omitempty"`
	LayoutPolicy   *layoutPolicyReport                         `json:"approved_policy_replay,omitempty"`
	ScoredPairs    int                                         `json:"scored_pairs"`
	ShortlistLimit int                                         `json:"shortlist_limit"`
	CachedVerified bool                                        `json:"reused_scores_same_result"`
	Timings        map[string]float64                          `json:"milliseconds"`
}

type options struct {
	input     string
	stage     string
	limit     int
	threshold float64
}

func main() {
	var opts options
	flag.StringVar(&opts.input, "input", "", "path to a frozen JSON fixture")
	flag.StringVar(&opts.stage, "stage", "all", "baseline, shortlist, all, or policy")
	flag.IntVar(&opts.limit, "limit", 10, "experimental shortlist limit (not a runtime setting)")
	flag.Float64Var(&opts.threshold, "threshold", 0.70, "pair threshold for the approved-policy replay")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, opts, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options, out io.Writer) error {
	if opts.input == "" || (opts.stage != "baseline" && opts.stage != "shortlist" && opts.stage != "all" && opts.stage != "policy") {
		return errors.New("use -input fixture.json and -stage baseline, shortlist, all, or policy")
	}
	if opts.limit < 1 || opts.limit > 100 {
		return errors.New("experimental shortlist limit must be between 1 and 100")
	}
	if opts.stage == "policy" && opts.limit > 10 {
		return errors.New("approved-policy shortlist limit must be at most 10")
	}
	start := time.Now()
	data, err := os.ReadFile(opts.input)
	if err != nil {
		return fmt.Errorf("read fixture: %w", err)
	}
	var input fixture
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("decode fixture: %w", err)
	}
	if input.Version != 1 || input.Name == "" || input.BaselineSource == "" || len(input.Captures) == 0 {
		return errors.New("fixture requires version 1, a name, baseline source, and saved captures")
	}
	observed, err := evaluate(ctx, input, opts)
	if err != nil {
		return err
	}
	observed.Digest = fmt.Sprintf("%x", sha256.Sum256(data))
	observed.Timings["end_to_end"] = milliseconds(start)
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(observed)
}

func milliseconds(start time.Time) float64 {
	return float64(time.Since(start)) / float64(time.Millisecond)
}
