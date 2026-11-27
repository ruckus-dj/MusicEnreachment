import { useEffect, useMemo, useRef, useState } from "react";
import type { SourceScanState } from "../../../ui/features/sources/SourceScanPanel";
import type { SourceFiles } from "../library/catalogData";
import type { SourceRoot } from "./SourcesScreen";
import { scanScopeLocations } from "./sourceInventory";
import {
  blockStagedSourceQueue,
  createSourceScan,
  processSourceQueue,
  type SourceProcessingState,
} from "./sourceProcessingModel";

type Update<T> = (update: (current: T) => T) => void;

export type DemoScanState =
  | Exclude<SourceScanState, { message: string }>
  | { kind: "fixtureFailure" };

/** One explicit scan attempt, identified before its first scheduled stage. */
export interface DemoScanAttempt {
  id: string;
  rootId: string;
  path: string;
  mode: SourceRoot["mode"];
  createdAt: number;
  state: DemoScanState;
  phase?: "traversing" | "applying";
}

export function useSourceScanDemoProvider({
  files,
  workDirectory,
  updateRoots,
  updateProcessing,
}: {
  files: SourceFiles;
  workDirectory: string;
  updateRoots: Update<SourceRoot[]>;
  updateProcessing: Update<SourceProcessingState>;
}) {
  const [attemptsByRoot, setAttemptsByRoot] = useState<
    Record<string, DemoScanAttempt>
  >({});
  const attempts = useMemo(
    () => Object.values(attemptsByRoot),
    [attemptsByRoot],
  );
  const states = useMemo(() => {
    const result: Record<string, DemoScanState> = {};
    for (const [rootId, attempt] of Object.entries(attemptsByRoot))
      result[rootId] = attempt.state;
    return result;
  }, [attemptsByRoot]);
  const attemptedRoots = useRef(new Set<string>());
  const active = useRef(new Set<string>());
  const nextAttemptId = useRef(0);
  const timers = useRef(new Set<number>());
  useEffect(() => {
    const pending = timers.current;
    const running = active.current;
    return () => {
      for (const timer of pending) window.clearTimeout(timer);
      pending.clear();
      running.clear();
    };
  }, []);
  useEffect(() => {
    if (!workDirectory) return;
    updateProcessing((current) =>
      current.files.some(
        (file) => file.blockedReason === "work_directory_required",
      )
        ? processSourceQueue(current)
        : current,
    );
  }, [workDirectory, updateProcessing]);

  function start(root: SourceRoot) {
    if (active.current.has(root.id)) return;
    // The last availability check is informational: an available root may
    // still start a scan. An unavailable fixture root deterministically fails
    // instead of replacing its retained inventory with invented results.
    const fail =
      root.available === false ||
      (root.id === "root-backup" && !attemptedRoots.current.has(root.id));
    attemptedRoots.current.add(root.id);
    active.current.add(root.id);
    const id = `scan-${++nextAttemptId.current}`;
    const createdAt = Date.now();
    const report = (state: DemoScanState) =>
      setAttemptsByRoot((current) => {
        const attempt = current[root.id];
        if (attempt?.id !== id) return current;
        return {
          ...current,
          [root.id]: {
            ...attempt,
            state,
            phase:
              state.kind === "traversing" || state.kind === "applying"
                ? state.kind
                : attempt.phase,
          },
        };
      });
    const schedule = (delay: number, callback: () => void) => {
      const timer = window.setTimeout(() => {
        timers.current.delete(timer);
        callback();
      }, delay);
      timers.current.add(timer);
    };
    setAttemptsByRoot((current) => ({
      ...current,
      [root.id]: {
        id,
        rootId: root.id,
        path: root.path,
        mode: root.mode,
        createdAt,
        state: { kind: "queued" },
      },
    }));
    schedule(350, () => report({ kind: "traversing" }));
    schedule(950, () => report({ kind: "applying" }));
    schedule(1500, () => {
      active.current.delete(root.id);
      if (fail) {
        report({ kind: "fixtureFailure" });
        return;
      }
      const at = new Date().toISOString();
      const admitted = scanScopeLocations(files, root);
      const admittedLocationIds = new Set(
        admitted.map((location) => location.id),
      );
      const count = admitted.length;
      updateRoots((current) =>
        current.map((item) =>
          item.id === root.id
            ? {
                ...item,
                available: true,
                inventoryCount: count,
                inventoryPath: root.path,
                lastSuccessfulScanAt: at,
              }
            : item,
        ),
      );
      updateProcessing((current) => {
        const scanned = createSourceScan(
          current,
          root.id,
          root.mode,
          files,
          admittedLocationIds,
        );
        return root.mode === "staged" && !workDirectory
          ? blockStagedSourceQueue(scanned, root.id)
          : processSourceQueue(
              scanned,
              root.id,
              undefined,
              new Set(admitted.map((location) => `${root.id}:${location.id}`)),
            );
      });
      report({ kind: "succeeded" });
    });
  }

  return {
    states,
    attempts,
    start,
    active: (id: string) => active.current.has(id),
  };
}

export type SourceScanDemoProvider = ReturnType<
  typeof useSourceScanDemoProvider
>;
