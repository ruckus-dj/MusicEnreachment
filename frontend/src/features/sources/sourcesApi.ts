import type { SourceRootResponse } from "../../api/generated/client.schemas";
import type { Locale } from "../../i18n/locale";
import { type SourceMessageKey, sourceMessage } from "./sourceMessages";

export type SourceError =
  | {
      type: "message";
      key: SourceMessageKey;
      values?: Record<string, string | number>;
      prefix?: SourceError;
      suffix?: SourceError;
    }
  | {
      type: "diagnostic";
      message: string;
      prefix?: SourceError;
      suffix?: SourceError;
    };

class SourceRequestError extends Error {
  constructor(readonly sourceError: SourceError) {
    super("Source request failed");
  }
}

export function sourceError(
  key: SourceMessageKey,
  values?: Record<string, string | number>,
): SourceError {
  return { type: "message", key, values };
}

export function sourceMessageFailure(
  key: SourceMessageKey,
  values?: Record<string, string | number>,
  suffix?: SourceError,
): Error {
  return new SourceRequestError({ ...sourceError(key, values), suffix });
}

export function sourceDiagnosticFailure(
  diagnostic: string,
  suffix?: SourceError,
): Error {
  return new SourceRequestError({
    type: "diagnostic",
    message: diagnostic,
    suffix,
  });
}

export function renderSourceError(error: SourceError, locale: Locale): string {
  if (error.type === "diagnostic")
    return [
      error.prefix && renderSourceError(error.prefix, locale),
      error.message,
      error.suffix && renderSourceError(error.suffix, locale),
    ]
      .filter(Boolean)
      .join(" ");
  return [
    error.prefix && renderSourceError(error.prefix, locale),
    sourceMessage(locale, error.key, error.values),
    error.suffix && renderSourceError(error.suffix, locale),
  ]
    .filter(Boolean)
    .join(" ");
}

export type SourceAction = "list" | "read" | "create" | "edit" | "delete";

export function message(reason: unknown): SourceError {
  if (reason instanceof SourceRequestError) return reason.sourceError;
  if (reason instanceof Error)
    return { type: "diagnostic", message: reason.message };
  return sourceError("requestFailed");
}

export function sourceResponseError(
  response: { status: number; data?: { detail?: string } },
  fallback: SourceError,
): Error {
  return new SourceRequestError(
    response.data?.detail
      ? { type: "diagnostic", message: response.data.detail }
      : fallback,
  );
}

export function sourceFailure(
  action: SourceAction,
  response: { status: number; data?: { detail?: string } },
  _locale?: Locale,
): Error {
  if (response.status === 404)
    return new SourceRequestError(sourceError("sourceMissing"));
  if (response.status === 409)
    return new SourceRequestError(sourceError("sourceConflict"));
  if (response.status === 400 && action === "delete")
    return new SourceRequestError(sourceError("deleteConfirmationStale"));
  if (response.status === 400 && (action === "create" || action === "edit"))
    return new SourceRequestError(sourceError("sourcePathRejected"));
  return sourceResponseError(
    response,
    sourceError("serverError", { status: response.status }),
  );
}

export function statusLabel(
  status: SourceRootResponse["status"],
  locale: Locale,
): string {
  if (status === "available") return sourceMessage(locale, "sourceAvailable");
  if (status === "unavailable")
    return sourceMessage(locale, "sourceUnavailable");
  return sourceMessage(locale, "sourceUnchecked");
}

export function fileCount(count: number, locale: Locale): string {
  const formatted = new Intl.NumberFormat(
    locale === "ru" ? "ru-RU" : "en-US",
  ).format(count);
  const plural = new Intl.PluralRules(
    locale === "ru" ? "ru-RU" : "en-US",
  ).select(count);
  const key =
    locale === "ru"
      ? plural === "one"
        ? "fileCountOne"
        : plural === "few"
          ? "fileCountFew"
          : "fileCountMany"
      : count === 1
        ? "fileCountOne"
        : "fileCountOther";
  return sourceMessage(locale, key, { count: formatted });
}

export function lastScanLabel(
  root: SourceRootResponse,
  locale: Locale,
): string {
  return root.last_successful_scan_at
    ? new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
        dateStyle: "short",
        timeStyle: "short",
      }).format(new Date(root.last_successful_scan_at))
    : sourceMessage(locale, "neverScanned");
}

export function inventoryLabel(
  root: SourceRootResponse,
  locale: Locale,
): string {
  return root.scan_generation === 0
    ? sourceMessage(locale, "neverScanned")
    : fileCount(root.location_count, locale);
}

export function inventoryAvailabilityNote(
  root: SourceRootResponse,
  locale: Locale,
): string {
  return root.status === "unavailable" && root.scan_generation > 0
    ? sourceMessage(locale, "scannedInventoryUnavailable")
    : "";
}

export function inventoryScopeNote(
  root: SourceRootResponse,
  locale: Locale,
): string {
  return root.stale && root.inventory_path
    ? sourceMessage(locale, "oldInventoryPath", { path: root.inventory_path })
    : "";
}

export function inventoryEmptyNote(
  root: SourceRootResponse,
  locale: Locale,
): string {
  return root.status === "available" &&
    root.scan_generation > 0 &&
    root.location_count === 0
    ? sourceMessage(locale, "emptyInventory")
    : "";
}

export function filesSummary(root: SourceRootResponse, locale: Locale): string {
  if (root.scan_generation === 0)
    return sourceMessage(locale, "noInventoryYet");
  if (root.location_count === 0)
    return sourceMessage(locale, "noSupportedFiles");
  return sourceMessage(locale, "inventorySummary", {
    count: fileCount(root.location_count, locale),
  });
}
