import { type SetStateAction, useCallback, useEffect, useState } from "react";
import { settingsMessages } from "../../../ui/features/settings/settingsFormMessages";
import type {
  OperationProgressBar,
  OperationProgressStatus,
} from "../../../ui/features/setup/OperationProgressView";
import type {
  SetupScreenCompletedOperationView,
  SetupScreenToolsStepView,
} from "../../../ui/features/setup/SetupScreenView";
import { useI18n } from "../demoPreferences";
import {
  activateOrCreateDemoInstallation,
  createDemoInstallation,
  type DemoInstallPreflight,
  initialDemoInstallations,
  initialDemoUnmanagedToolFiles,
  installPreflightDemo,
  isAbsoluteDemoServerPath,
  type ManagedToolPackage,
  managedDemoFileCount,
  movePreflightDemo,
} from "../settings/ToolLifecycleDemo";
import type { DemoSettings } from "../settings/useSettingsScreenDemoProvider";
import { overlapsAnyDemoPath } from "../sources/demoPathValidation";

/** Demo scheduling: queued work stays visible before the host advances it. */
const startDelayMs = 400;
const settleDelayMs = 800;

export type SetupToolDownloadStatus =
  | "queued"
  | "running"
  | "succeeded"
  | "failed";

/** Illustrative demo phases after the download starts. */
export type SetupToolDownloadStage = "download" | "extract";

/** Screens whose completion semantics differ for the same admitted download. */
export type SetupToolDownloadOrigin = "setup" | "settings";

/** Host-owned tool activity consumed by the existing Setup tools-step contract. */
export type SetupToolDownloadActivity = Pick<
  SetupScreenToolsStepView,
  "operations" | "completed" | "operationError" | "loadingOperations"
> & {
  start: (
    preflight: DemoInstallPreflight,
    origin?: SetupToolDownloadOrigin,
  ) => void;
  /** Packages with a queued or running download; repeated starts are disabled. */
  busyKinds: ReadonlySet<ManagedToolPackage>;
};

export type SetupToolDownload = {
  id: string;
  preflight: DemoInstallPreflight;
  status: SetupToolDownloadStatus;
  /** Active demo phase: download shows measured progress, extract stays indeterminate. */
  stage: SetupToolDownloadStage;
  origin: SetupToolDownloadOrigin;
  /** Epoch milliseconds, matching the operations snapshot date format. */
  createdAt: number;
};

/** Frozen evidence for a prototype managed-tools folder move. */
export type DemoToolMovePlan = {
  /** Tools root that must still be current when the move settles. */
  sourceDirectory: string;
  /** Exact destination root applied only after a successful settlement. */
  path: string;
  removeOld: boolean;
  token: string;
};

export type SetupToolMove = {
  id: string;
  plan: DemoToolMovePlan;
  status: SetupToolDownloadStatus;
  /** Epoch milliseconds, matching the operations snapshot date format. */
  createdAt: number;
};

/** Illustrative demo fraction for the active download stage. */
export const demoToolDownloadPercent = 42;

function toolName(kind: ManagedToolPackage): string {
  return kind === "ffmpeg" ? "FFmpeg" : "fpcalc";
}

/** Compact target used by wizard status and completion copy: "FFmpeg 9.0". */
function compactToolTarget(download: SetupToolDownload): string {
  return `${toolName(download.preflight.kind)} ${download.preflight.release}`;
}

function downloadStageLabel(
  stage: SetupToolDownloadStage,
  locale: "en" | "ru",
): string {
  if (stage === "download")
    return locale === "ru" ? "Загрузка файлов" : "Downloading files";
  return locale === "ru" ? "Распаковка файлов" : "Unpacking files";
}

function downloadProgress(
  download: SetupToolDownload,
  locale: "en" | "ru",
): OperationProgressBar | undefined {
  if (download.status !== "running") return undefined;
  const target = compactToolTarget(download);
  const stage = downloadStageLabel(download.stage, locale);
  const label = `${target} — ${stage}`;
  if (download.stage !== "download") return { kind: "indeterminate", label };
  return {
    kind: "determinate",
    label,
    value: demoToolDownloadPercent,
    max: 100,
    text: new Intl.NumberFormat(locale, {
      style: "percent",
      maximumFractionDigits: 0,
    }).format(demoToolDownloadPercent / 100),
  };
}

function downloadCompletedText(
  download: SetupToolDownload,
  locale: "en" | "ru",
): string {
  return locale === "ru"
    ? `${compactToolTarget(download)} — установлен.`
    : `${compactToolTarget(download)} — installed.`;
}

function downloadStatus(
  download: SetupToolDownload,
  locale: "en" | "ru",
): OperationProgressStatus {
  const text = compactToolTarget(download);
  if (download.status === "queued")
    return { text, stage: locale === "ru" ? "Ожидание" : "Waiting" };
  if (download.status === "failed")
    return { text, stage: locale === "ru" ? "Ошибка" : "Failed" };
  if (download.status === "succeeded")
    return { text: downloadCompletedText(download, locale) };
  return {
    text,
    stage: downloadStageLabel(download.stage, locale),
    progress: downloadProgress(download, locale),
  };
}

function isPending(download: SetupToolDownload) {
  return download.status === "queued" || download.status === "running";
}

function isPendingMove(move: SetupToolMove) {
  return move.status === "queued" || move.status === "running";
}

/**
 * A move applies only while its frozen evidence still matches the live root.
 * Compare the fixture's preflight again; no filesystem or byte-level work is
 * simulated here.
 */
function approvedMove(current: DemoSettings, plan: DemoToolMovePlan) {
  if (current.toolsPath.trim() !== plan.sourceDirectory.trim())
    return undefined;
  if (!isAbsoluteDemoServerPath(plan.path.trim())) return undefined;
  if (
    overlapsAnyDemoPath(plan.path, [
      current.toolsPath,
      current.workDirectory,
      current.currentOutput ?? "/srv/music",
    ])
  )
    return undefined;
  const managedCount = managedDemoFileCount(
    current.installations ?? initialDemoInstallations,
  );
  if (movePreflightDemo(plan.path, managedCount).token !== plan.token)
    return undefined;
  return plan.path.trim();
}

/** Applying a move invalidates the path check, as a confirmed Settings move did. */
function applyMove(current: DemoSettings, plan: DemoToolMovePlan) {
  const destination = approvedMove(current, plan);
  if (!destination) return undefined;
  return {
    ...current,
    toolsPath: destination,
    pathChecked: false,
    setupVerified: {},
  };
}

function approvedDownload(current: DemoSettings, plan: DemoInstallPreflight) {
  if (current.toolsPath.trim() !== plan.toolsRoot) return undefined;
  const installations = current.installations ?? initialDemoInstallations;
  const unmanaged = current.unmanagedToolFiles ?? initialDemoUnmanagedToolFiles;
  const approved = installPreflightDemo(
    current.toolsPath,
    plan.kind,
    plan.release,
    installations,
    unmanaged,
  );
  if (approved.token !== plan.token) return undefined;
  return { installations, unmanaged, approved };
}

/** Setup records a verified release and activates it as the current toolchain. */
function applySetupDownload(current: DemoSettings, plan: DemoInstallPreflight) {
  const evidence = approvedDownload(current, plan);
  if (!evidence) return undefined;
  const { installations, unmanaged } = evidence;
  return {
    ...current,
    installations: activateOrCreateDemoInstallation(
      installations,
      plan.kind,
      plan.release,
    ),
    setupVerified: { ...current.setupVerified, [plan.kind]: plan.release },
    setupFailures: { ...current.setupFailures, [plan.kind]: undefined },
    ...(plan.conflicts.length > 0 && {
      unmanagedToolFiles: unmanaged.filter(
        (path) => !plan.conflicts.includes(path),
      ),
    }),
  };
}

/** Settings installs without activating or claiming Setup verification. */
function applySettingsDownload(
  current: DemoSettings,
  plan: DemoInstallPreflight,
) {
  const evidence = approvedDownload(current, plan);
  if (!evidence) return undefined;
  const { installations, unmanaged } = evidence;
  return {
    ...current,
    installations: createDemoInstallation(
      installations,
      plan.kind,
      plan.release,
    ),
    ...(plan.conflicts.length > 0 && {
      unmanagedToolFiles: unmanaged.filter(
        (path) => !plan.conflicts.includes(path),
      ),
    }),
  };
}

/**
 * In-memory managed-tool downloads shared by Setup, Settings and Operations.
 * lives above the routes so queued work keeps settling after navigation. The
 * admitted preflight is frozen evidence: settlement only applies settings while
 * the current tools root still matches that evidence, and it never performs
 * network or filesystem work, emits byte progress, or persists to storage.
 */
export function useSetupToolDownloadsDemoProvider(
  initialSettings: DemoSettings,
) {
  const { locale } = useI18n();
  const [{ settings, downloads, moves }, setState] = useState({
    settings: initialSettings,
    downloads: [] as SetupToolDownload[],
    moves: [] as SetupToolMove[],
  });
  const setSettings = useCallback(
    (update: SetStateAction<DemoSettings>) =>
      setState((current) => ({
        ...current,
        settings:
          typeof update === "function" ? update(current.settings) : update,
      })),
    [],
  );

  function start(
    preflight: DemoInstallPreflight,
    origin: SetupToolDownloadOrigin = "setup",
  ) {
    const frozen = {
      ...preflight,
      targets: [...preflight.targets],
      conflicts: [...preflight.conflicts],
    };
    const createdAt = Date.now();
    const id = `tool-download-${crypto.randomUUID()}`;
    setState((current) => {
      if (
        current.downloads.some(
          (item) => item.preflight.kind === frozen.kind && isPending(item),
        )
      )
        return current;
      return {
        ...current,
        downloads: [
          ...current.downloads,
          {
            id,
            preflight: frozen,
            status: "queued",
            stage: "download",
            origin,
            createdAt,
          },
        ],
      };
    });
  }

  function retry(id: string) {
    setState((current) => {
      const failed = current.downloads.find(
        (item) => item.id === id && item.status === "failed",
      );
      if (
        !failed ||
        current.downloads.some(
          (item) =>
            item.preflight.kind === failed.preflight.kind && isPending(item),
        )
      )
        return current;
      return {
        ...current,
        downloads: current.downloads.map((item) =>
          item === failed
            ? { ...item, status: "queued", stage: "download" }
            : item,
        ),
      };
    });
  }
  function dismiss(id: string) {
    setState((current) => ({
      ...current,
      downloads: current.downloads.filter(
        (item) => item.id !== id || item.status !== "failed",
      ),
    }));
  }

  function startMove(plan: DemoToolMovePlan) {
    const frozen: DemoToolMovePlan = {
      sourceDirectory: plan.sourceDirectory,
      path: plan.path,
      removeOld: plan.removeOld,
      token: plan.token,
    };
    const createdAt = Date.now();
    const id = `tool-move-${crypto.randomUUID()}`;
    setState((current) => {
      if (current.moves.some(isPendingMove)) return current;
      return {
        ...current,
        moves: [
          ...current.moves,
          { id, plan: frozen, status: "queued", createdAt },
        ],
      };
    });
  }

  function retryMove(id: string) {
    setState((current) => {
      const failed = current.moves.find(
        (item) => item.id === id && item.status === "failed",
      );
      if (!failed || current.moves.some(isPendingMove)) return current;
      return {
        ...current,
        moves: current.moves.map((item) =>
          item === failed ? { ...item, status: "queued" } : item,
        ),
      };
    });
  }

  function dismissMove(id: string) {
    setState((current) => ({
      ...current,
      moves: current.moves.filter(
        (item) => item.id !== id || item.status !== "failed",
      ),
    }));
  }

  // Advance one admitted tool activity at a time so an unrelated queued item
  // cannot restart the running timer, mirroring the demo operations host.
  // Downloads and moves are distinct kinds sharing this timer.
  const activities = [
    ...downloads.map((item) => ({
      kind: "download" as const,
      id: item.id,
      status: item.status,
      stage: item.stage,
    })),
    ...moves.map((item) => ({
      kind: "move" as const,
      id: item.id,
      status: item.status,
    })),
  ];
  const active =
    activities.find((item) => item.status === "running") ??
    activities.find((item) => item.status === "queued");
  const activeId = active?.id;
  const activeStatus = active?.status;
  const activeKind = active?.kind;
  const activeStage = active?.kind === "download" ? active.stage : undefined;

  useEffect(() => {
    if (!activeId || !activeStatus || !activeKind) return;
    const timer = setTimeout(
      () => {
        setState((current) => {
          if (activeKind === "move") {
            const move = current.moves.find((item) => item.id === activeId);
            if (move?.status !== activeStatus) return current;
            const applied =
              activeStatus === "running"
                ? applyMove(current.settings, move.plan)
                : undefined;
            const status =
              activeStatus === "queued"
                ? "running"
                : applied
                  ? "succeeded"
                  : "failed";
            return {
              ...current,
              settings: applied ?? current.settings,
              moves: current.moves.map((item) =>
                item === move ? { ...item, status } : item,
              ),
            };
          }
          const download = current.downloads.find(
            (item) => item.id === activeId,
          );
          if (
            download?.status !== activeStatus ||
            download.stage !== activeStage
          )
            return current;
          // Download is measured; the following extract phase is indeterminate,
          // so the demo never leaves a stale 100% bar behind.
          if (activeStatus === "running" && activeStage === "download") {
            return {
              ...current,
              downloads: current.downloads.map((item) =>
                item === download ? { ...item, stage: "extract" } : item,
              ),
            };
          }
          const applied =
            activeStatus === "running"
              ? download.origin === "settings"
                ? applySettingsDownload(current.settings, download.preflight)
                : applySetupDownload(current.settings, download.preflight)
              : undefined;
          const status =
            activeStatus === "queued"
              ? "running"
              : applied
                ? "succeeded"
                : "failed";
          return {
            ...current,
            settings: applied ?? current.settings,
            downloads: current.downloads.map((item) =>
              item === download ? { ...item, status } : item,
            ),
          };
        });
      },
      activeStatus === "queued" ? startDelayMs : settleDelayMs,
    );
    return () => clearTimeout(timer);
  }, [activeId, activeKind, activeStage, activeStatus]);

  const busyKinds = new Set<ManagedToolPackage>();
  for (const item of downloads) {
    if (isPending(item)) busyKinds.add(item.preflight.kind);
  }

  const operations = downloads
    .filter((item) => item.status !== "succeeded")
    .map((item) => ({
      id: item.id,
      source: {
        locale,
        status: downloadStatus(item, locale),
        failure:
          item.status === "failed"
            ? settingsMessages[locale].staleInstall
            : undefined,
      },
    }));

  const completed: SetupScreenCompletedOperationView[] = downloads
    .filter((item) => item.status === "succeeded")
    .map((item) => ({
      key: item.id,
      text: downloadCompletedText(item, locale),
    }));

  const activity: SetupToolDownloadActivity = {
    operations,
    completed,
    operationError: undefined,
    loadingOperations: undefined,
    start,
    busyKinds,
  };
  return {
    settings,
    setSettings,
    activity,
    downloads,
    retry,
    dismiss,
    moves,
    startMove,
    retryMove,
    dismissMove,
  };
}
