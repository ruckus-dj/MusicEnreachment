import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { createSource, listSources } from "../../api/generated/client";
import type { SourceRootResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { SourceDetailScreen } from "./SourceDetailScreen";
import { SourceInspectorScreen } from "./SourceInspectorScreen";
import {
  inventoryAvailabilityNote,
  inventoryEmptyNote,
  inventoryLabel,
  inventoryScopeNote,
  lastScanLabel,
  message,
  sourceFailure,
  statusLabel,
} from "./sourcesApi";
import "./sources.css";

// This screen owns both addresses of the source inventory: the list of
// registered roots at #/sources and one root at #/sources/{id}.
type SourcesRoute =
  | { view: "list" }
  | { view: "detail"; id: string }
  | { view: "location"; id: string; locationId: string };

const listRoute: SourcesRoute = { view: "list" };
let cachedHash: string | undefined;
let cachedRoute: SourcesRoute = listRoute;

// useSyncExternalStore requires the same reference for an unchanged hash.
function routeFor(hash: string): SourcesRoute {
  if (hash === cachedHash) return cachedRoute;
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const detail = /^\/sources\/([^/]+)$/.exec(path);
  const location = /^\/sources\/([^/]+)\/locations\/([^/]+)$/.exec(path);
  cachedHash = hash;
  cachedRoute = location
    ? {
        view: "location",
        id: decodeURIComponent(location[1]),
        locationId: decodeURIComponent(location[2]),
      }
    : detail
      ? { view: "detail", id: decodeURIComponent(detail[1]) }
      : listRoute;
  return cachedRoute;
}

function subscribe(onChange: () => void) {
  window.addEventListener("hashchange", onChange);
  return () => window.removeEventListener("hashchange", onChange);
}

export function SourcesScreen() {
  const route = useSyncExternalStore(
    subscribe,
    () => routeFor(window.location.hash),
    () => listRoute,
  );
  switch (route.view) {
    case "location":
      return (
        <SourceInspectorScreen
          key={`${route.id}/${route.locationId}`}
          sourceId={route.id}
          locationId={route.locationId}
        />
      );
    case "detail":
      return <SourceDetailScreen key={route.id} sourceId={route.id} />;
    case "list":
      return <SourceListScreen />;
  }
}

function InventoryCell({ root }: { root: SourceRootResponse }) {
  const notes = [
    inventoryAvailabilityNote(root),
    inventoryScopeNote(root),
    inventoryEmptyNote(root),
  ].filter((note) => note.length > 0);
  return (
    <>
      {inventoryLabel(root)}
      {notes.map((note) => (
        <span key={note} className="sources-note">
          {note}
        </span>
      ))}
    </>
  );
}

function SourceListScreen() {
  const [sources, setSources] = useState<SourceRootResponse[]>();
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [processingMode, setProcessingMode] = useState<
    "" | "in_place" | "staged"
  >("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [formError, setFormError] = useState("");
  const [notice, setNotice] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const listHeading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const formAlert = useRef<HTMLParagraphElement>(null);

  const load = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      const response = await listSources({ cache: "no-store" });
      if (response.status !== 200) throw sourceFailure("list", response);
      setSources(response.data.sources || []);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    heading.current?.focus();
    void load();
  }, [load]);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  useEffect(() => {
    if (formError) formAlert.current?.focus();
  }, [formError]);

  async function register() {
    if (!name.trim() || !path.trim() || !processingMode) {
      setFormError(
        "Укажите имя каталога, абсолютный путь на сервере и режим обработки.",
      );
      return;
    }
    setBusy(true);
    setFormError("");
    setNotice("");
    try {
      const response = await createSource({
        display_name: name.trim(),
        configured_path: path.trim(),
        processing_mode: processingMode,
      });
      if (response.status !== 200) throw sourceFailure("create", response);
      const registered = response.data;
      setCreating(false);
      setName("");
      setPath("");
      setProcessingMode("");
      setNotice(`Каталог «${registered.display_name}» зарегистрирован.`);
      await load();
      listHeading.current?.focus();
    } catch (reason) {
      setFormError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  function cancelCreate() {
    setCreating(false);
    setFormError("");
    setName("");
    setPath("");
    setProcessingMode("");
  }

  return (
    <section
      aria-labelledby="sources-title"
      className="sources-screen mx-auto max-w-5xl py-3"
    >
      <h1 ref={heading} tabIndex={-1} id="sources-title">
        Источники
      </h1>
      <p className="sources-help">
        Серверные каталоги, из которых MeloTrove читает файлы. Путь указывается
        на сервере; каталог читается только для чтения и файлы не загружаются
        через браузер.
      </p>
      {busy && sources && <p role="status">Выполняется запрос…</p>}
      {notice && <p role="status">{notice}</p>}
      {error && (
        <>
          <p ref={alert} tabIndex={-1} role="alert">
            {error}
          </p>
          <AppButton onPress={() => void load()}>Повторить загрузку</AppButton>
        </>
      )}
      {!sources && !error && <p role="status">Загрузка каталогов…</p>}
      {sources && (
        <>
          {creating ? (
            <form
              aria-labelledby="source-create-title"
              className="sources-panel"
              onSubmit={(event) => {
                event.preventDefault();
                void register();
              }}
            >
              <h2 id="source-create-title">Новый каталог</h2>
              <label>
                Имя каталога
                <input
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
              <label>
                Путь на сервере
                <input
                  value={path}
                  onChange={(event) => setPath(event.target.value)}
                />
              </label>
              <label>
                Режим обработки
                <select
                  required
                  value={processingMode}
                  onChange={(event) =>
                    setProcessingMode(
                      event.target.value as typeof processingMode,
                    )
                  }
                >
                  <option value="">Выберите режим</option>
                  <option value="in_place">in_place</option>
                  <option value="staged">staged</option>
                </select>
              </label>
              <p className="sources-help">
                Путь принадлежит серверу, а не этому компьютеру. MeloTrove
                ничего не создаёт и не записывает в каталог источника: браузер
                не передаёт файлы.
              </p>
              {formError && (
                <p ref={formAlert} tabIndex={-1} role="alert">
                  {formError}
                </p>
              )}
              <AppButton type="submit" isDisabled={busy}>
                Зарегистрировать каталог
              </AppButton>
              <AppButton onPress={cancelCreate}>Отмена</AppButton>
            </form>
          ) : (
            <AppButton
              onPress={() => {
                setFormError("");
                setNotice("");
                setCreating(true);
              }}
            >
              Добавить каталог
            </AppButton>
          )}
          <section
            aria-labelledby="sources-list-title"
            className="sources-panel"
          >
            <h2 ref={listHeading} tabIndex={-1} id="sources-list-title">
              Подключённые каталоги
            </h2>
            {sources.length === 0 ? (
              <p role="status">Ни один каталог не зарегистрирован.</p>
            ) : (
              <div className="sources-table-wrap">
                <table className="sources-table">
                  <caption className="sources-visually-hidden">
                    Зарегистрированные серверные каталоги: имя, путь,
                    доступность, включение, последнее сканирование и инвентарь
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">Имя</th>
                      <th scope="col">Путь</th>
                      <th scope="col">Доступность</th>
                      <th scope="col">Включён</th>
                      <th scope="col">Последнее сканирование</th>
                      <th scope="col">Инвентарь</th>
                      <th scope="col">Действия</th>
                    </tr>
                  </thead>
                  <tbody>
                    {sources.map((root) => (
                      <tr key={root.id}>
                        <th scope="row" data-label="Имя">
                          {root.display_name}
                        </th>
                        <td data-label="Путь">
                          <code>{root.configured_path}</code>
                        </td>
                        <td data-label="Доступность">
                          <span
                            className="sources-chip"
                            data-status={root.status}
                          >
                            {statusLabel(root.status)}
                          </span>
                          {root.status === "unavailable" && root.safe_error && (
                            <span className="sources-note">
                              {root.safe_error}
                            </span>
                          )}
                        </td>
                        <td data-label="Включён">
                          {root.enabled ? "Включён" : "Выключен"}
                        </td>
                        <td data-label="Последнее сканирование">
                          {lastScanLabel(root)}
                        </td>
                        <td data-label="Инвентарь">
                          <InventoryCell root={root} />
                        </td>
                        <td data-label="Действия">
                          <AppButton
                            onPress={() => {
                              window.location.hash = `/sources/${encodeURIComponent(root.id)}`;
                            }}
                          >
                            Открыть {root.display_name}
                          </AppButton>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <p className="sources-note">
              Недоступность каталога или смена пути не удаляют прежний
              инвентарь: записи остаются от последнего успешного сканирования.
            </p>
          </section>
        </>
      )}
    </section>
  );
}
