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
import {
  activateToolInstallation,
  listInstallations,
  listOperations,
  listToolCatalog,
  preflightToolInstall,
  startToolInstall,
} from "../../api/generated/client";
import type {
  CatalogBody,
  InstallationResponse,
  InstallPreflightBody,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { OperationProgress } from "./OperationProgress";
import { errorMessage, setupError } from "./setupApi";

type Kind = "ffmpeg" | "fpcalc";
const kinds: readonly Kind[] = ["ffmpeg", "fpcalc"];
const operationKey = "melotrove.setup.operations";
const legacyOperationKey = "melotrove.setup.operation";

function savedIds(): string[] {
  const saved =
    window.localStorage.getItem(operationKey)?.split(",").filter(Boolean) || [];
  const legacy =
    window.localStorage
      .getItem(legacyOperationKey)
      ?.split(",")
      .filter(Boolean) || [];
  return [...new Set([...saved, ...legacy])];
}

export function SetupTools({
  onActivated,
}: {
  onActivated: () => Promise<unknown>;
}) {
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
  const [error, setError] = useState("");
  const [operationsError, setOperationsError] = useState("");
  const [discovering, setDiscovering] = useState(false);
  const mounted = useRef(true);
  const alert = useRef<HTMLParagraphElement>(null);
  const operationsAlert = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  useEffect(() => {
    if (operationsError) operationsAlert.current?.focus();
  }, [operationsError]);
  const discover = useCallback(async () => {
    setDiscovering(true);
    setOperationsError("");
    try {
      const response = await listOperations();
      if (response.status !== 200) throw setupError(response);
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
      if (mounted.current) setOperationsError(errorMessage(reason));
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
    if (response.status !== 200) throw setupError(response);
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
    const legacy = window.localStorage.getItem(legacyOperationKey);
    if (legacy) {
      const remaining = legacy
        .split(",")
        .filter((value) => value && value !== id);
      if (remaining.length)
        window.localStorage.setItem(legacyOperationKey, remaining.join(","));
      else window.localStorage.removeItem(legacyOperationKey);
    }
    setIds([...initialIds.current]);
  }, []);
  const load = useCallback(async () => {
    setPending(true);
    setError("");
    try {
      const [installed, ffmpeg, fpcalc] = await Promise.all([
        listInstallations(),
        listToolCatalog({ package_kind: "ffmpeg" }),
        listToolCatalog({ package_kind: "fpcalc" }),
      ]);
      if (installed.status !== 200) throw setupError(installed);
      if (ffmpeg.status !== 200) throw setupError(ffmpeg);
      if (fpcalc.status !== 200) throw setupError(fpcalc);
      setInstallations(installed.data.installations || []);
      setCatalog({ ffmpeg: ffmpeg.data, fpcalc: fpcalc.data });
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function start(plan: InstallPreflightBody) {
    const response = await startToolInstall({
      preflight_token: plan.preflight_token,
      confirmed_conflicts: plan.conflicts || [],
    });
    if (response.status !== 200) throw setupError(response);
    const snapshot = response.data;
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
    setError("");
    setPreflight(undefined);
    try {
      const identity = selected[kind];
      if (!identity) throw new Error("Выберите версию.");
      const response = await preflightToolInstall({
        package_kind: kind,
        release_identity: identity,
      });
      if (response.status !== 200) throw setupError(response);
      if (response.data.conflicts?.length)
        setPreflight({ kind, plan: response.data });
      else await start(response.data);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }
  async function confirm() {
    if (!preflight) return;
    setPending(true);
    setError("");
    try {
      await start(preflight.plan);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }
  async function activate(item: InstallationResponse, kind: Kind) {
    setPending(true);
    setError("");
    try {
      const response = await activateToolInstallation(item.id, {
        package_kind: kind,
      });
      if (response.status !== 204) throw setupError(response);
      await onActivated();
      await loadInstallations();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setPending(false);
    }
  }
  return (
    <div>
      <AppButton isDisabled={pending} onPress={load}>
        Обновить каталог
      </AppButton>
      {kinds.map((kind) => (
        <div key={kind} className="setup-tool">
          <h3>{kind}</h3>
          <p>{catalog[kind]?.notice}</p>
          <Select
            value={selected[kind] || null}
            placeholder="Выберите версию"
            isDisabled={pending || !catalog[kind]?.releases?.length}
            onChange={(key) => {
              setSelected((previous) => ({
                ...previous,
                [kind]: key === null ? "" : String(key),
              }));
              setPreflight(undefined);
            }}
          >
            <Label>Версия {kind}</Label>
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
            Проверить установку {kind}
          </AppButton>
          {installations
            .filter(
              (item) => item.package_kind === kind && item.state === "ready",
            )
            .map((item) => (
              <div key={item.id}>
                {item.release_identity}{" "}
                {item.active ? (
                  "Активна"
                ) : (
                  <AppButton
                    isDisabled={pending}
                    onPress={() => {
                      void activate(item, kind);
                    }}
                  >
                    Активировать {item.release_identity}
                  </AppButton>
                )}
              </div>
            ))}
        </div>
      ))}
      {preflight?.plan.conflicts?.length ? (
        <fieldset aria-label="Подтверждение конфликтов">
          <legend>Перезапись только следующих файлов:</legend>
          <ul>
            {preflight.plan.conflicts.map((path) => (
              <li key={path}>{path}</li>
            ))}
          </ul>
          <AppButton isDisabled={pending} onPress={confirm}>
            Подтвердить перезапись
          </AppButton>
          <AppButton
            isDisabled={pending}
            onPress={() => setPreflight(undefined)}
          >
            Отмена
          </AppButton>
        </fieldset>
      ) : null}
      {completed.map((item) => (
        <p key={item.id} role="status" className="setup-operation">
          Операция {item.id}: {item.state}, {item.stage}, {item.bytes_completed}{" "}
          bytes.
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
      {error && (
        <p ref={alert} tabIndex={-1} role="alert">
          {error}
        </p>
      )}
      {operationsError && (
        <div>
          <p ref={operationsAlert} tabIndex={-1} role="alert">
            {operationsError}
          </p>
          <AppButton isDisabled={discovering} onPress={discover}>
            Повторить загрузку операций
          </AppButton>
        </div>
      )}
      {discovering && <p role="status">Загрузка операций…</p>}
      {pending && <p role="status">Выполняется запрос…</p>}
    </div>
  );
}
