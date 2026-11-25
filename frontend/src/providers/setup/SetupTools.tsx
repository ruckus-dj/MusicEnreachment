import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import { listInstallations, listOperations } from "../../api/generated/client";
import type {
  CatalogBody,
  InstallationResponse,
  InstallPreflightBody,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { SetupToolsStepBody } from "../../ui/features/setup/SetupScreenView";
import type { SetupToolCard } from "../../ui/features/setup/SetupToolCardsView";
import {
  operationKindLabel,
  operationProgressLabel,
  operationStageLabel,
  operationStateLabel,
  operationTargetLabel,
} from "../operations/operationPresentation";
import { GitHubTokenForm } from "../tools/GitHubTokenForm";
import {
  loadManagedToolCatalogs,
  type ManagedToolKind,
  preflightManagedToolInstall,
  startManagedToolInstall,
} from "../tools/toolWorkflow";
import { OperationProgress } from "./OperationProgress";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
  setupMessages,
} from "./setupMessages";

type Kind = ManagedToolKind;
const kinds: readonly Kind[] = ["ffmpeg", "fpcalc"];
const operationKey = "melotrove.setup.operations";

function savedIds(): string[] {
  const saved =
    window.localStorage.getItem(operationKey)?.split(",").filter(Boolean) || [];
  return [...new Set(saved)];
}

function toolExecutables(kind: Kind): string[] {
  return kind === "ffmpeg" ? ["ffmpeg", "ffprobe"] : ["fpcalc"];
}

export function SetupTools({
  onBlockedChange,
  hasGitHubToken = false,
  onGitHubTokenChanged,
}: {
  onBlockedChange?: (blocked: boolean) => void;
  hasGitHubToken?: boolean;
  onGitHubTokenChanged?: () => void | Promise<void>;
}) {
  const { locale } = useI18n();
  const messages = setupMessages[locale];
  const [ids, setIds] = useState(savedIds);
  const initialIds = useRef(ids);
  const finishedIds = useRef(new Set<string>());
  const [completed, setCompleted] = useState<OperationResponse[]>([]);
  const [snapshots, setSnapshots] = useState<Record<string, OperationResponse>>(
    {},
  );
  const [installations, setInstallations] = useState<InstallationResponse[]>(
    [],
  );
  const [catalog, setCatalog] = useState<Partial<Record<Kind, CatalogBody>>>(
    {},
  );
  const [selected, setSelected] = useState<Partial<Record<Kind, string>>>({});
  const [preflight, setPreflight] = useState<{
    kind: Kind;
    release: string;
    plan: InstallPreflightBody;
  }>();
  const [confirmError, setConfirmError] = useState<SetupFeedback | null>(null);
  const installTrigger = useRef<HTMLElement | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const [operationsError, setOperationsError] = useState<SetupFeedback | null>(
    null,
  );
  const [discovering, setDiscovering] = useState(false);
  const mounted = useRef(true);
  const alert = useRef<HTMLParagraphElement>(null);
  const operationsAlert = useRef<HTMLParagraphElement>(null);
  const onBlockedChangeRef = useRef(onBlockedChange);
  onBlockedChangeRef.current = onBlockedChange;
  const readyByKind = (kind: Kind) =>
    installations.filter(
      (item) => item.package_kind === kind && item.state === "ready",
    );
  const legacyConflict = kinds.some((kind) => readyByKind(kind).length > 1);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  useEffect(() => {
    if (legacyConflict) alert.current?.focus();
    onBlockedChangeRef.current?.(legacyConflict);
  }, [legacyConflict]);
  useEffect(() => {
    if (operationsError) operationsAlert.current?.focus();
  }, [operationsError]);
  const discover = useCallback(async () => {
    setDiscovering(true);
    setOperationsError(null);
    try {
      const response = await listOperations({ tool_discovery: "install" });
      if (response.status !== 200) throw apiResponseError(response);
      if (!mounted.current) return;
      const active = response.data.operations || [];
      setSnapshots((previous) => ({
        ...previous,
        ...Object.fromEntries(active.map((item) => [item.id, item])),
      }));
      initialIds.current = [
        ...new Set([...initialIds.current, ...active.map((item) => item.id)]),
      ].filter((id) => !finishedIds.current.has(id));
      window.localStorage.setItem(operationKey, initialIds.current.join(","));
      setIds([...initialIds.current]);
    } catch (reason) {
      if (mounted.current)
        setOperationsError(setupFeedback(reason, "unknownError"));
    } finally {
      if (mounted.current) setDiscovering(false);
    }
  }, []);
  useEffect(() => {
    mounted.current = true;
    window.localStorage.setItem(operationKey, initialIds.current.join(","));
    void discover();
    return () => {
      mounted.current = false;
    };
  }, [discover]);

  const loadInstallations = useCallback(async () => {
    const response = await listInstallations();
    if (response.status !== 200) throw apiResponseError(response);
    setInstallations(response.data.installations || []);
  }, []);
  const forget = useCallback((id: string, snapshot: OperationResponse) => {
    setCompleted((previous) => [
      ...previous.filter((item) => item.id !== id),
      snapshot,
    ]);
    finishedIds.current.add(id);
    initialIds.current = initialIds.current.filter((value) => value !== id);
    window.localStorage.setItem(operationKey, initialIds.current.join(","));
    setIds([...initialIds.current]);
  }, []);
  const load = useCallback(async () => {
    setPending(true);
    setError(null);
    try {
      const [installedResult, catalogsResult] = await Promise.allSettled([
        listInstallations(),
        loadManagedToolCatalogs(setupHttpError),
      ]);
      if (installedResult.status === "rejected") throw installedResult.reason;
      const installed = installedResult.value;
      if (installed.status !== 200) throw apiResponseError(installed);
      setInstallations(installed.data.installations || []);
      if (catalogsResult.status === "rejected") throw catalogsResult.reason;
      setCatalog(catalogsResult.value);
    } catch (reason) {
      setError(setupFeedback(reason, "setupToolsInventoryError"));
    } finally {
      setPending(false);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function start(plan: InstallPreflightBody, kind: Kind) {
    if (readyByKind(kind).length) {
      throw setupMessage("readyVersionExists");
    }
    const snapshot = await startManagedToolInstall(
      plan,
      undefined,
      setupHttpError,
    );
    setSnapshots((previous) => ({ ...previous, [snapshot.id]: snapshot }));
    if (!initialIds.current.includes(snapshot.id)) {
      initialIds.current = [...initialIds.current, snapshot.id];
      window.localStorage.setItem(operationKey, initialIds.current.join(","));
      setIds([...initialIds.current]);
    }
    setPreflight(undefined);
    setConfirmError(null);
  }
  async function install(kind: Kind) {
    setPending(true);
    setError(null);
    setPreflight(undefined);
    setConfirmError(null);
    try {
      if (readyByKind(kind).length) {
        throw setupMessage("readyVersionExists");
      }
      const identity = selected[kind];
      if (!identity) throw setupMessage("chooseVersion");
      const plan = await preflightManagedToolInstall(
        kind,
        identity,
        setupHttpError,
      );
      if (plan.conflicts?.length)
        setPreflight({ kind, release: identity, plan });
      else await start(plan, kind);
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  async function confirm() {
    if (!preflight || pending) return;
    setPending(true);
    setError(null);
    setConfirmError(null);
    try {
      await start(preflight.plan, preflight.kind);
    } catch (reason) {
      setConfirmError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  const cards: SetupToolCard[] = kinds.map((kind): SetupToolCard => {
    const ready = readyByKind(kind);
    if (ready.length) {
      return {
        kind,
        state: "ready",
        versions: ready.map(({ id, version, active }) => ({
          id,
          version,
          active,
        })),
      };
    }
    return {
      kind,
      state: "install",
      notice: catalog[kind]?.notice,
      releases: catalog[kind]?.releases ?? [],
      selected: selected[kind] || null,
      pending,
      onSelect: (identity) => {
        setSelected((previous) => ({
          ...previous,
          [kind]: identity ?? "",
        }));
        setPreflight(undefined);
        setConfirmError(null);
      },
      action: {
        disabled: pending || !selected[kind],
        onPress: ({ target }) => {
          installTrigger.current =
            target instanceof HTMLElement ? target : null;
          void install(kind);
        },
      },
    };
  });
  const errorText = error
    ? setupFeedbackText(error, locale)
    : legacyConflict
      ? setupFeedbackText(setupMessage("multipleReadyTools"), locale)
      : undefined;
  return (
    <SetupToolsStepBody
      refresh={
        legacyConflict
          ? undefined
          : {
              label: messages.toolCatalogRefresh,
              disabled: pending,
              onPress: load,
            }
      }
      cards={legacyConflict ? undefined : { locale, tools: cards }}
      confirmation={
        !legacyConflict && preflight?.plan.conflicts?.length
          ? {
              title: messages.confirmOverwrite,
              packageName: messages[preflight.kind],
              release: preflight.release,
              executables: toolExecutables(preflight.kind),
              conflicts: preflight.plan.conflicts,
              confirmLabel: messages.confirmOverwriteButton,
              cancelLabel: messages.cancel,
              busy: pending,
              busyLabel: messages.startingToolDownload,
              error: confirmError
                ? setupFeedbackText(confirmError, locale)
                : undefined,
              onConfirm: () => {
                void confirm();
              },
              onCancel: () => {
                const trigger = installTrigger.current;
                setPreflight(undefined);
                setConfirmError(null);
                requestAnimationFrame(() => {
                  if (trigger?.isConnected) trigger.focus();
                });
              },
            }
          : undefined
      }
      completed={completed.map((item) => {
        const stage = operationStageLabel(item.stage, locale);
        return {
          key: item.id,
          text: `${operationKindLabel(item.kind, locale)} — ${operationTargetLabel(item, locale)}: ${operationStateLabel(item.state, locale)}.${stage ? ` ${stage}.` : ""} ${operationProgressLabel(item, locale)}.`,
        };
      })}
      operations={ids.map((id) => (
        <OperationProgress
          key={id}
          id={id}
          initial={snapshots[id]}
          onSuccess={loadInstallations}
          onFinished={forget}
        />
      ))}
      error={errorText ? { text: errorText, errorRef: alert } : undefined}
      operationError={
        operationsError
          ? {
              text: setupFeedbackText(operationsError, locale),
              errorRef: operationsAlert,
              retryLabel: messages.retryOperations,
              discovering,
              onRetry: discover,
            }
          : undefined
      }
      loadingOperations={discovering ? messages.loadingOperations : undefined}
      requestPending={pending ? messages.requestInProgress : undefined}
      tokenMounted={
        <GitHubTokenForm
          hasToken={hasGitHubToken}
          onChanged={onGitHubTokenChanged}
        />
      }
    />
  );
}
