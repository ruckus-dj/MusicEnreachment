import { useEffect, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  getGenreCatalog,
  searchGenreCatalog,
} from "../../api/generated/client";
import type {
  GenreCatalogItem,
  GenreCatalogStatus,
} from "../../api/generated/client.schemas";
import type {
  GenreOwner,
  GenreReference,
} from "../library/GenreMetadataEditor";
import { GenrePicker, type GenrePickerCatalog } from "../library/GenrePicker";
import { genreMessages } from "../library/genreMessages";

/**
 * Unavailable genre search does not block saving with the current selection.
 */
export function ProviderCatalogGenreField({
  locale,
  owner,
  value,
  onChange,
  disabled,
}: {
  locale: "ru" | "en";
  owner: GenreOwner;
  value: readonly GenreReference[];
  onChange: (next: GenreReference[]) => void;
  disabled?: boolean;
}) {
  const catalog = useProviderGenreCatalog(locale);
  const copy = genreMessages[locale];
  return (
    <div className="grid gap-1">
      <GenrePicker
        {...catalog}
        value={[...value]}
        onChange={onChange}
        editing
        label={copy.label[owner]}
        copy={copy}
        disabled={Boolean(disabled)}
      />
    </div>
  );
}

export function useProviderGenreCatalog(
  locale: "ru" | "en",
): GenrePickerCatalog {
  const copy = genreMessages[locale];
  const [status, setStatus] = useState<GenreCatalogStatus>();
  const [statusLoading, setStatusLoading] = useState(false);
  const [statusError, setStatusError] = useState("");
  const [query, setQuery] = useState("");
  const [result, setResult] = useState<{
    term: string;
    options: GenreCatalogItem[];
  }>();
  const [loading, setLoading] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [retry, setRetry] = useState(0);
  const trimmed = query.trim();
  const catalogAvailable = Boolean(status?.updated_at && status.count > 0);

  async function loadStatus(force = false) {
    if (!force && (status || statusLoading)) return;
    setStatusLoading(true);
    setStatusError("");
    try {
      const response = await getGenreCatalog({ cache: "no-store" });
      if (response.status !== 200) throw apiResponseError(response);
      setStatus(response.data);
    } catch (reason) {
      setStatusError(errorMessage(reason, copy.catalogError));
    } finally {
      setStatusLoading(false);
    }
  }

  useEffect(() => {
    if (!trimmed || !catalogAvailable) {
      setResult(undefined);
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(
      () => {
        setLoading(true);
        setCatalogError("");
        void searchGenreCatalog(
          { query: trimmed, limit: 40 },
          { cache: "no-store", signal: controller.signal },
        )
          .then((response) => {
            if (response.status !== 200) throw apiResponseError(response);
            if (!controller.signal.aborted)
              setResult({ term: trimmed, options: response.data.items ?? [] });
          })
          .catch((reason: unknown) => {
            if (!controller.signal.aborted)
              setCatalogError(errorMessage(reason, copy.loadError));
          })
          .finally(() => {
            if (!controller.signal.aborted) setLoading(false);
          });
      },
      retry ? 0 : 180,
    );
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [catalogAvailable, copy.loadError, retry, trimmed]);

  return {
    options: catalogAvailable && result?.term === trimmed ? result.options : [],
    query,
    onQueryChange: setQuery,
    loading,
    catalogError,
    onClearCatalogError: () => setCatalogError(""),
    onRetryCatalog: () => setRetry((attempt) => attempt + 1),
    catalogStatus: status,
    catalogStatusLoading: statusLoading,
    catalogStatusError: statusError,
    onOpenCatalog: () => void loadStatus(),
    onRetryStatus: () => {
      setStatus(undefined);
      void loadStatus(true);
    },
  };
}
