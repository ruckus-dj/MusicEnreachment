import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type {
  SourceArtifactCleanupCandidate,
  SourceArtifactCleanupResult,
  SourceArtifactCleanupViewProps,
} from "../../../ui/features/sources/SourceArtifactCleanupView";
import type { Locale } from "../../../ui/i18n/types";
import type { SourceFiles } from "../library/catalogData";
import { retainedCopyLabel } from "./retainedCopyPath";
import type { SourceRoot } from "./SourcesScreen";
import {
  type SourceMessageKey,
  sourceMessage,
} from "./sourceInventoryMessages";
import {
  eligibleSourceCopies,
  type SourceProcessingState,
} from "./sourceProcessingModel";

type CleanupIdentity = { id: string; createdAt: number };
export type DemoSourceCleanupOperation = CleanupIdentity &
  (
    | { state: "queued"; stage: "queued"; safe_error?: never }
    | { state: "running"; stage: "cleaning_artifacts"; safe_error?: never }
    | { state: "succeeded"; stage: "finished"; safe_error?: never }
    | { state: "failed"; stage: "finished"; safe_error: string }
  );

const candidateLimit = 1000;

/**
 * Host-owned demo execution. Kept above route branches so a cleanup started on
 * Sources keeps its timers, operation and results while the user visits
 * Operations and comes back. Selections stay in the screen provider.
 */
export type SourceArtifactCleanupDemoExecution = {
  submitted: string[];
  operation?: DemoSourceCleanupOperation;
  failedOperations: readonly DemoSourceCleanupOperation[];
  results?: SourceArtifactCleanupResult[];
  error?: { text: string };
  isPending: boolean;
  /** Increments when a started batch reaches its terminal state. */
  completedRuns: number;
  blocksRoot: (rootId: string) => boolean;
  onLoad: () => void;
  onStart: (ids: readonly string[]) => void;
};

export function useSourceArtifactCleanupDemoExecution({
  locale,
  processing,
  updateProcessing,
}: {
  locale: Locale;
  processing: SourceProcessingState;
  updateProcessing: (state: SourceProcessingState) => void;
}): SourceArtifactCleanupDemoExecution {
  const [submitted, setSubmitted] = useState<string[]>([]);
  const [operation, setOperation] = useState<DemoSourceCleanupOperation>();
  const [failedOperations, setFailedOperations] = useState<
    DemoSourceCleanupOperation[]
  >([]);
  const [results, setResults] = useState<SourceArtifactCleanupResult[]>();
  const [error, setError] = useState<{ text: string }>();
  const [completedRuns, setCompletedRuns] = useState(0);
  const pending = useRef(false);
  const sequence = useRef(0);
  const timers = useRef<Set<number>>(new Set());
  const processingRef = useRef(processing);
  processingRef.current = processing;
  const localeRef = useRef(locale);
  localeRef.current = locale;

  useEffect(() => {
    const scheduled = timers.current;
    return () => {
      for (const timer of scheduled) window.clearTimeout(timer);
      scheduled.clear();
    };
  }, []);

  const schedule = (delay: number, callback: () => void) => {
    const timer = window.setTimeout(() => {
      timers.current.delete(timer);
      callback();
    }, delay);
    timers.current.add(timer);
  };
  const message = (key: SourceMessageKey) =>
    sourceMessage(localeRef.current, key);

  function load() {
    if (pending.current) return;
    setError(undefined);
  }

  function start(ids: readonly string[]) {
    if (!ids.length || ids.length > candidateLimit || pending.current) return;
    const frozen = [...ids];
    const currentIds = new Set(
      eligibleSourceCopies(processingRef.current).map((copy) => copy.id),
    );
    if (
      new Set(frozen).size !== frozen.length ||
      frozen.some((id) => !currentIds.has(id))
    ) {
      setError({ text: message("cleanupSelectionStale") });
      return;
    }

    const identity = {
      id: `cleanup-${++sequence.current}`,
      createdAt: Date.now(),
    };
    pending.current = true;
    setError(undefined);
    setSubmitted(frozen);
    setResults(undefined);
    setOperation({ ...identity, state: "queued", stage: "queued" });

    schedule(200, () =>
      setOperation({
        ...identity,
        state: "running",
        stage: "cleaning_artifacts",
      }),
    );
    schedule(600, () => {
      const current = processingRef.current;
      const frozenEligible = new Set(
        eligibleSourceCopies(current).map((copy) => copy.id),
      );
      // A later claim can fail without undoing the earlier confirmed items.
      const firstBlocked = frozen.findIndex((id) => !frozenEligible.has(id));
      const claimed = firstBlocked < 0 ? frozen : frozen.slice(0, firstBlocked);

      const removed = new Set(claimed);
      updateProcessing({
        ...current,
        files: current.files.map((file) =>
          file.copyId && removed.has(file.copyId)
            ? { ...file, copyId: undefined }
            : file,
        ),
        copies: current.copies.filter((copy) => !removed.has(copy.id)),
      });
      setResults(
        claimed.map((id) => ({ artifact_id: id, state: "succeeded" })),
      );
      const terminal: DemoSourceCleanupOperation =
        firstBlocked < 0
          ? { ...identity, state: "succeeded", stage: "finished" }
          : {
              ...identity,
              state: "failed",
              stage: "finished",
              safe_error: message("cleanupSelectionStale"),
            };
      setOperation(terminal);
      if (terminal.state === "failed")
        setFailedOperations((current) => [...current, terminal]);
      setCompletedRuns((count) => count + 1);
      pending.current = false;
    });
  }

  const isPending =
    operation?.state === "queued" || operation?.state === "running";

  return {
    submitted,
    operation,
    failedOperations,
    results,
    error,
    isPending,
    completedRuns,
    blocksRoot: (rootId) =>
      isPending &&
      processing.copies.some(
        (copy) => copy.rootId === rootId && submitted.includes(copy.id),
      ),
    onLoad: load,
    onStart: start,
  };
}

/**
 * Screen-scoped cleanup view model. Candidate labels and checkbox selections
 * belong to the mounted Sources screen; admitted execution comes from the host.
 */
export function useSourceArtifactCleanupDemoProvider({
  locale,
  roots,
  files,
  processing,
  execution,
}: {
  locale: Locale;
  roots: SourceRoot[];
  files: SourceFiles;
  processing: SourceProcessingState;
  execution: SourceArtifactCleanupDemoExecution;
}): SourceArtifactCleanupViewProps {
  const [selected, setSelected] = useState<string[]>([]);
  const completedRuns = useRef(execution.completedRuns);

  useLayoutEffect(() => {
    if (completedRuns.current === execution.completedRuns) return;
    completedRuns.current = execution.completedRuns;
    setSelected((current) => (current.length ? [] : current));
  }, [execution.completedRuns]);

  const candidates: SourceArtifactCleanupCandidate[] = eligibleSourceCopies(
    processing,
  )
    .slice(0, candidateLimit)
    .map((copy) => ({
      artifact_id: copy.id,
      source_relative_path:
        retainedCopyLabel(copy, processing, roots, files) ??
        sourceMessage(locale, "copyPathUnavailable"),
      state: "cleanup_eligible",
    }));
  const batchCount = candidates.length;

  function toggle(id: string) {
    if (execution.isPending) return;
    setSelected((current) =>
      current.includes(id)
        ? current.filter((selectedId) => selectedId !== id)
        : [...current, id],
    );
  }

  return {
    locale,
    candidates,
    batchCount,
    selected,
    submitted: execution.submitted,
    operation: execution.operation,
    results: execution.results,
    loading: false,
    busy: false,
    isPending: execution.isPending,
    error: execution.error,
    streamError: "",
    onLoad: execution.onLoad,
    onToggle: toggle,
    onStart: execution.onStart,
  };
}
