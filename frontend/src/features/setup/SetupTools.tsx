import { useCallback, useEffect, useRef, useState } from "react";
import {
  Button,
  Label,
  ListBox,
  ListBoxItem,
  Popover,
  Select,
  SelectValue,
} from "react-aria-components";
import { apiResponseError } from "../../api/client/errors";
import { listInstallations, listOperations } from "../../api/generated/client";
import type {
  CatalogBody,
  InstallationResponse,
  InstallPreflightBody,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
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
    plan: InstallPreflightBody;
  }>();
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
      const response = await listOperations();
      if (response.status !== 200) throw apiResponseError(response);
      if (!mounted.current) return;
      const active = (response.data.operations || []).filter(
        (item) =>
          item.kind === "install" &&
          (item.state === "queued" ||
            item.state === "running" ||
            item.state === "failed"),
      );
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
  }
  async function install(kind: Kind) {
    setPending(true);
    setError(null);
    setPreflight(undefined);
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
      if (plan.conflicts?.length) setPreflight({ kind, plan });
      else await start(plan, kind);
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  async function confirm() {
    if (!preflight) return;
    setPending(true);
    setError(null);
    try {
      await start(preflight.plan, preflight.kind);
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  return (
    <div>
      {!legacyConflict && (
        <AppButton isDisabled={pending} onPress={load}>
          {messages.toolCatalogRefresh}
        </AppButton>
      )}
      {kinds.map((kind) => (
        <div key={kind} className="setup-tool">
          <h3>{kind}</h3>
          {legacyConflict ? null : readyByKind(kind).length ? (
            readyByKind(kind).map((item) => (
              <p key={item.id} role="status">
                {item.version}:{" "}
                {item.active ? messages.active : messages.installedReady}
              </p>
            ))
          ) : (
            <>
              <p>{catalog[kind]?.notice}</p>
              <Select
                value={selected[kind] || null}
                placeholder={messages.selectVersion}
                isDisabled={pending || !catalog[kind]?.releases?.length}
                onChange={(key) => {
                  setSelected((previous) => ({
                    ...previous,
                    [kind]: key === null ? "" : String(key),
                  }));
                  setPreflight(undefined);
                }}
              >
                <Label>
                  {messages.toolVersion}
                  {kind}
                </Label>
                <Button className="setup-select-trigger">
                  <SelectValue />
                  <span aria-hidden="true">▾</span>
                </Button>
                <Popover className="setup-select-popover">
                  <ListBox items={catalog[kind]?.releases || []}>
                    {(release) => (
                      <ListBoxItem
                        id={release.identity}
                        textValue={`${release.identity} (${release.source})`}
                      >
                        {release.identity} ({release.source})
                      </ListBoxItem>
                    )}
                  </ListBox>
                </Popover>
              </Select>
              <AppButton
                isDisabled={pending || !selected[kind]}
                onPress={() => {
                  void install(kind);
                }}
              >
                {messages.checkToolInstall}
                {kind}
                {messages.install}
              </AppButton>
            </>
          )}
        </div>
      ))}
      {!legacyConflict && preflight?.plan.conflicts?.length ? (
        <fieldset aria-label={messages.conflictConfirmation}>
          <legend>{messages.confirmOverwrite}</legend>
          <ul>
            {preflight.plan.conflicts.map((path) => (
              <li key={path}>{path}</li>
            ))}
          </ul>
          <AppButton isDisabled={pending} onPress={confirm}>
            {messages.confirmOverwriteButton}
          </AppButton>
          <AppButton
            isDisabled={pending}
            onPress={() => setPreflight(undefined)}
          >
            {messages.cancel}
          </AppButton>
        </fieldset>
      ) : null}
      {completed.map((item) => (
        <p key={item.id} role="status" className="setup-operation">
          {operationKindLabel(item.kind, locale)} —{" "}
          {operationTargetLabel(item, locale)}:{" "}
          {operationStateLabel(item.state, locale)}.
          {operationStageLabel(item.stage, locale) &&
            ` ${operationStageLabel(item.stage, locale)}.`}
          {` ${operationProgressLabel(item, locale)}.`}
        </p>
      ))}
      {ids.map((id) => (
        <OperationProgress
          key={id}
          id={id}
          initial={snapshots[id]}
          onSuccess={loadInstallations}
          onFinished={forget}
        />
      ))}
      {(error || legacyConflict) && (
        <p ref={alert} tabIndex={-1} role="alert">
          {error
            ? setupFeedbackText(error, locale)
            : setupFeedbackText(setupMessage("multipleReadyTools"), locale)}
        </p>
      )}
      {operationsError && (
        <div>
          <p ref={operationsAlert} tabIndex={-1} role="alert">
            {setupFeedbackText(operationsError, locale)}
          </p>
          <AppButton isDisabled={discovering} onPress={discover}>
            {messages.retryOperations}
          </AppButton>
        </div>
      )}
      {discovering && <p role="status">{messages.loadingOperations}</p>}
      {pending && <p role="status">{messages.requestInProgress}</p>}
      <GitHubTokenForm
        hasToken={hasGitHubToken}
        onChanged={onGitHubTokenChanged}
      />
    </div>
  );
}
