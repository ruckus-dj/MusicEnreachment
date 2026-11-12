import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-aria-components";
import { listSourceLocations } from "../../api/generated/client";
import type {
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { numberFormat, sourceMessage } from "./sourceMessages";
import {
  fileCount,
  inventoryAvailabilityNote,
  inventoryEmptyNote,
  inventoryScopeNote,
  message,
  renderSourceError,
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

function sizeLabel(bytes: number, locale: "en" | "ru"): string {
  let value = bytes;
  let unit = 0;
  const units =
    locale === "ru" ? ["Б", "КБ", "МБ", "ГБ"] : ["B", "KB", "MB", "GB"];
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${numberFormat(locale, { maximumFractionDigits: 1 }).format(value)} ${units[unit]}`;
}

export function SourceLocations({
  sourceId,
  root,
  refreshToken,
  limit,
}: SourceLocationsProps) {
  const { locale } = useI18n();
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
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
  const [error, setError] = useState<ReturnType<typeof message> | null>(null);
  const alert = useRef<HTMLParagraphElement>(null);

  const loadFirstPage = useCallback(async () => {
    const epoch = ++requestEpoch.current;
    setLocations([]);
    setNextCursor(undefined);
    setLoaded(false);
    setError(null);
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
      setError(null);
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
    setError(null);
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
  const availabilityNote = inventoryAvailabilityNote(root, locale);
  const scopeNote = inventoryScopeNote(root, locale);

  return (
    <section aria-labelledby="source-locations-title" className="sources-panel">
      <h2 id="source-locations-title">{t("inventoryFiles")}</h2>
      {neverScanned ? (
        <p className="sources-note">{t("noInventoryYet")}</p>
      ) : (
        <>
          <p className="sources-help">
            {t("inventoryHelp")}{" "}
            <code>{root.inventory_path ?? root.configured_path}</code>.
          </p>
          {availabilityNote && (
            <p className="sources-note">{availabilityNote}</p>
          )}
          {scopeNote && <p className="sources-note">{scopeNote}</p>}
          {!loaded && !error && <p role="status">{t("loadingFiles")}</p>}
          {error && (
            <>
              <p ref={alert} tabIndex={-1} role="alert">
                {renderSourceError(error, locale)}
              </p>
              <AppButton isDisabled={busy} onPress={retry}>
                {t("retryLoad")}
              </AppButton>
            </>
          )}
          {loaded && !error && locations.length === 0 && (
            <p role="status">
              {inventoryEmptyNote(root, locale) || t("noSupportedFiles")}
            </p>
          )}
          {locations.length > 0 && (
            <>
              <div className="sources-table-wrap">
                <table className="sources-table">
                  <caption className="sources-visually-hidden">
                    {t("filesCaption")}
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">{t("pathColumn")}</th>
                      <th scope="col">{t("size")}</th>
                      <th scope="col">{t("modified")}</th>
                      <th scope="col">{t("audioCheck")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {locations.map((location) => (
                      <tr key={location.id}>
                        <th scope="row" data-label={t("pathColumn")}>
                          <Link
                            href={`#/sources/${encodeURIComponent(sourceId)}/locations/${encodeURIComponent(location.id)}`}
                          >
                            <code>{location.relative_path}</code>
                          </Link>
                        </th>
                        <td data-label={t("size")}>
                          {sizeLabel(location.size_bytes, locale)}
                        </td>
                        <td data-label={t("modified")}>
                          {new Intl.DateTimeFormat(
                            locale === "ru" ? "ru-RU" : "en-US",
                            { dateStyle: "short", timeStyle: "short" },
                          ).format(new Date(location.mtime))}
                        </td>
                        <td data-label={t("audioCheck")}>
                          <span>
                            <span
                              className="sources-chip"
                              data-status={location.probe_status}
                            >
                              {t(
                                location.probe_status === "audio"
                                  ? "streamAudio"
                                  : location.probe_status === "no_audio"
                                    ? "noAudio"
                                    : "probeError",
                              )}
                            </span>
                            {location.probe_status === "no_audio" && (
                              <span className="sources-note">
                                {t("noAudioReason")}
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
                                  {t("probeRetryNote")}
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
                {t("shownCount", {
                  count: fileCount(locations.length, locale),
                })}
              </p>
              {nextCursor && (
                <AppButton isDisabled={busy} onPress={() => void loadMore()}>
                  {t("showMore")}
                </AppButton>
              )}
              {busy && <p role="status">{t("loadingNextPage")}</p>}
            </>
          )}
        </>
      )}
    </section>
  );
}
