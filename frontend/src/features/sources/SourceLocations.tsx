import { useCallback, useEffect, useRef, useState } from "react";
import { listSourceLocations } from "../../api/generated/client";
import type {
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import {
  fileCount,
  inventoryAvailabilityNote,
  inventoryEmptyNote,
  inventoryScopeNote,
  message,
  sourceFailure,
} from "./sourcesApi";
import "./sources.css";

// The server validates ?limit between 1 and 200, so a prop that drifts out of
// that range still produces a query the API accepts.
const MAX_PAGE_LIMIT = 200;
const DEFAULT_PAGE_LIMIT = 50;

export type SourceLocationsProps = {
  sourceId: string;
  root: SourceRootResponse;
  refreshToken?: string | number;
  limit?: number;
};

function pageLimit(limit?: number): number {
  if (limit === undefined) return DEFAULT_PAGE_LIMIT;
  return Math.min(MAX_PAGE_LIMIT, Math.max(1, Math.trunc(limit)));
}

const sizeFormat = new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 });

const SIZE_UNITS = ["Б", "КБ", "МБ", "ГБ"] as const;

function sizeLabel(bytes: number): string {
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < SIZE_UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${sizeFormat.format(value)} ${SIZE_UNITS[unit]}`;
}

const PROBE_LABELS: Record<SourceLocationResponse["probe_status"], string> = {
  audio: "Аудио",
  no_audio: "Нет аудиодорожки",
  probe_error: "Ошибка проверки",
};

export function SourceLocations({
  sourceId,
  root,
  refreshToken,
  limit,
}: SourceLocationsProps) {
  const size = pageLimit(limit);
  const generation = root.scan_generation;
  // Another source, generation, page size or refresh token starts page 1 again.
  const inventoryKey = `${sourceId}\u0000${generation}\u0000${size}\u0000${String(
    refreshToken ?? "",
  )}`;
  const loadedKey = useRef<string | undefined>(undefined);
  const requestEpoch = useRef(0);
  const [locations, setLocations] = useState<SourceLocationResponse[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const alert = useRef<HTMLParagraphElement>(null);

  const loadFirstPage = useCallback(async () => {
    const epoch = ++requestEpoch.current;
    setLocations([]);
    setNextCursor(undefined);
    setLoaded(false);
    setError("");
    setBusy(true);
    try {
      const response = await listSourceLocations(
        sourceId,
        { limit: size },
        { cache: "no-store" },
      );
      if (requestEpoch.current !== epoch) return;
      if (response.status !== 200) throw sourceFailure("read", response);
      setLocations(response.data.locations || []);
      setNextCursor(response.data.next_cursor);
      setLoaded(true);
    } catch (reason) {
      if (requestEpoch.current !== epoch) return;
      setError(message(reason));
      setLoaded(true);
    } finally {
      if (requestEpoch.current === epoch) setBusy(false);
    }
  }, [sourceId, size]);

  useEffect(() => {
    if (loadedKey.current === inventoryKey) return;
    loadedKey.current = inventoryKey;
    if (generation === 0) {
      // Generation 0 holds no published inventory: only applied scans are listed.
      requestEpoch.current += 1;
      setLocations([]);
      setNextCursor(undefined);
      setLoaded(false);
      setError("");
      setBusy(false);
      return;
    }
    void loadFirstPage();
  }, [inventoryKey, generation, loadFirstPage]);

  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);

  async function loadMore() {
    const cursor = nextCursor;
    if (!cursor) return;
    const epoch = requestEpoch.current;
    setBusy(true);
    setError("");
    try {
      const response = await listSourceLocations(
        sourceId,
        { limit: size, cursor },
        { cache: "no-store" },
      );
      if (requestEpoch.current !== epoch) return;
      if (response.status !== 200) throw sourceFailure("read", response);
      setLocations((current) => [
        ...current,
        ...(response.data.locations || []),
      ]);
      setNextCursor(response.data.next_cursor);
    } catch (reason) {
      if (requestEpoch.current !== epoch) return;
      setError(message(reason));
    } finally {
      if (requestEpoch.current === epoch) setBusy(false);
    }
  }

  // A failed later page keeps its loaded rows and continues from its cursor.
  function retry() {
    if (locations.length === 0) void loadFirstPage();
    else void loadMore();
  }

  const neverScanned = generation === 0;
  const availabilityNote = inventoryAvailabilityNote(root);
  const scopeNote = inventoryScopeNote(root);

  return (
    <section aria-labelledby="source-locations-title" className="sources-panel">
      <h2 id="source-locations-title">Файлы инвентаря</h2>
      {neverScanned ? (
        <p className="sources-note">
          Инвентаря нет: каталог ещё ни разу не сканировался успешно. Список
          файлов появится после успешного сканирования.
        </p>
      ) : (
        <>
          <p className="sources-help">
            Показаны записи последнего успешного сканирования: файлы с
            поддерживаемым аудиорасширением. Пути указаны относительно{" "}
            <code>{root.inventory_path ?? root.configured_path}</code>.
          </p>
          {availabilityNote && (
            <p className="sources-note">{availabilityNote}</p>
          )}
          {scopeNote && <p className="sources-note">{scopeNote}</p>}
          {!loaded && !error && <p role="status">Загрузка списка файлов…</p>}
          {error && (
            <>
              <p ref={alert} tabIndex={-1} role="alert">
                {error}
              </p>
              <AppButton isDisabled={busy} onPress={retry}>
                Повторить загрузку
              </AppButton>
            </>
          )}
          {loaded && !error && locations.length === 0 && (
            <p role="status">
              {inventoryEmptyNote(root) ||
                "Последнее успешное сканирование не нашло ни одного файла с поддерживаемым аудиорасширением."}
            </p>
          )}
          {locations.length > 0 && (
            <>
              <div className="sources-table-wrap">
                <table className="sources-table">
                  <caption className="sources-visually-hidden">
                    Файлы последнего успешного сканирования: относительный путь,
                    размер, время изменения и проверка аудиопотока
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">Путь</th>
                      <th scope="col">Размер</th>
                      <th scope="col">Изменён</th>
                      <th scope="col">Проверка</th>
                    </tr>
                  </thead>
                  <tbody>
                    {locations.map((location) => (
                      <tr key={location.id}>
                        <th scope="row" data-label="Путь">
                          <code>{location.relative_path}</code>
                        </th>
                        <td data-label="Размер">
                          {sizeLabel(location.size_bytes)}
                        </td>
                        <td data-label="Изменён">
                          {new Date(location.mtime).toLocaleString()}
                        </td>
                        <td data-label="Проверка">
                          <span>
                            <span
                              className="sources-chip"
                              data-status={location.probe_status}
                            >
                              {PROBE_LABELS[location.probe_status]}
                            </span>
                            {location.probe_status === "no_audio" && (
                              <span className="sources-note">
                                Аудиодорожки нет: файл не может участвовать в
                                анализе.
                              </span>
                            )}
                            {location.probe_status === "probe_error" && (
                              <>
                                {location.safe_error && (
                                  <span className="sources-note">
                                    {location.safe_error}
                                  </span>
                                )}
                                <span className="sources-note">
                                  Проверка повторится при следующем
                                  сканировании.
                                </span>
                              </>
                            )}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <p className="sources-note">
                Показано {fileCount(locations.length)}.
              </p>
              {nextCursor && (
                <AppButton isDisabled={busy} onPress={() => void loadMore()}>
                  Показать ещё
                </AppButton>
              )}
              {busy && <p role="status">Загрузка следующей страницы…</p>}
            </>
          )}
        </>
      )}
    </section>
  );
}
