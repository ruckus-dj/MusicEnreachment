import type { ReactNode } from "react";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import {
  type LibraryTagComparisonProvider,
  LibraryTagEvidenceComparison,
  type LibraryTagValues,
} from "./LibraryTagComparisonTable";
import {
  releaseControlAlertClass,
  releaseControlSelectClass,
} from "./libraryReleaseControlStyles";
import { libraryTagComparisonFields } from "./libraryTagComparisonFields";

export type LibraryPositionTagEvidenceReader = {
  readonly name: string;
  readonly version: string;
  readonly contract: string;
};

export type LibraryPositionTagEvidenceSourceState =
  | { readonly state: "loading" }
  | { readonly state: "failed" }
  | { readonly state: "missing" }
  | {
      readonly state: "saved";
      readonly tags: LibraryTagValues;
      readonly reader?: LibraryPositionTagEvidenceReader;
    };

export type LibraryPositionTagEvidenceCurrentState =
  | { readonly state: "loading" | "absent" | "unavailable" }
  | { readonly state: "saved"; readonly tags: LibraryTagValues };

export type LibraryPositionTagEvidenceComparisonState =
  | { readonly state: "loading" | "unavailable" }
  | {
      readonly state: "saved";
      readonly provider: LibraryTagComparisonProvider;
      readonly overrides?: LibraryTagValues;
    };

export type LibraryPositionTagEvidenceLocation = {
  readonly id: string;
  readonly label: string;
  readonly rootId: string;
};

const copy = {
  en: {
    inspectSource: "Choose an audio source to inspect its saved tags.",
    chooseLocation: "Source location",
    selectLocation: "Choose a location",
    noLocation: "No locations are saved for this audio source.",
    loading: "Loading saved source tags…",
    loadFailed: "Could not read saved tags for this location.",
    retry: "Retry",
    reader: "Reader",
    missingSource: "No metadata inspection is saved for this location.",
    emptySource: "The saved source snapshot has no tags.",
    missingCurrent: "The current publication tag snapshot is unavailable.",
    loadingCurrent: "Reading the current publication snapshot…",
    noCurrent: "This position has no current publication.",
    emptyCurrent: "The saved current publication snapshot has no tags.",
  },
  ru: {
    inspectSource:
      "Выберите источник аудио, чтобы посмотреть сохранённые теги.",
    chooseLocation: "Расположение источника",
    selectLocation: "Выберите расположение",
    noLocation: "Для этого источника аудио нет сохранённых расположений.",
    loading: "Загрузка сохранённых тегов источника…",
    loadFailed: "Не удалось прочитать сохранённые теги этого расположения.",
    retry: "Повторить",
    reader: "Средство чтения",
    missingSource: "Для этого расположения нет сохранённой проверки тегов.",
    emptySource: "В сохранённом снимке источника нет тегов.",
    missingCurrent: "Снимок тегов текущей публикации недоступен.",
    loadingCurrent: "Чтение снимка текущей публикации…",
    noCurrent: "У этой позиции нет текущей публикации.",
    emptyCurrent: "В сохранённом снимке текущей публикации нет тегов.",
  },
} as const;

const readerClass = "m-0 text-xs text-[var(--muted)] wrap-anywhere";

export function LibraryPositionTagEvidenceFrame({
  locale,
  locationId,
  locations,
  hasSource,
  onLocationChange,
  children,
}: {
  locale: Locale;
  locationId?: string;
  locations: readonly LibraryPositionTagEvidenceLocation[];
  hasSource: boolean;
  onLocationChange: (id: string) => void;
  children: ReactNode;
}) {
  const text = copy[locale];
  return (
    <div className="mt-3 grid min-w-0 grid-cols-1 gap-3">
      {!hasSource && <p>{text.inspectSource}</p>}
      {hasSource && !locations.length && <p>{text.noLocation}</p>}
      {locations.length > 1 && (
        <label className="grid gap-1 text-sm">
          {text.chooseLocation}
          <select
            className={releaseControlSelectClass}
            value={locationId ?? ""}
            onChange={(event) => onLocationChange(event.currentTarget.value)}
          >
            <option value="">{text.selectLocation}</option>
            {locations.map((location) => (
              <option key={location.id} value={location.id}>
                {location.label} · {location.rootId}
              </option>
            ))}
          </select>
        </label>
      )}
      {children}
    </div>
  );
}

export function LibraryPositionTagEvidenceView({
  locale,
  locationPath,
  source,
  current,
  comparison,
  showAll,
  onShowAllChange,
  differencesOnly,
  onDifferencesChange,
  onRetry,
}: {
  locale: Locale;
  /** Saved source location rendered above the read state. */
  locationPath?: string;
  source?: LibraryPositionTagEvidenceSourceState;
  current?: LibraryPositionTagEvidenceCurrentState;
  comparison: LibraryPositionTagEvidenceComparisonState;
  showAll: boolean;
  onShowAllChange: (selected: boolean) => void;
  differencesOnly: boolean;
  onDifferencesChange: (selected: boolean) => void;
  onRetry?: () => void;
}) {
  const text = copy[locale];
  const sourceTags = source?.state === "saved" ? source.tags : undefined;
  const currentTags = current?.state === "saved" ? current.tags : undefined;
  const currentNotice =
    current &&
    (current.state === "saved"
      ? Object.keys(current.tags).length
        ? undefined
        : text.emptyCurrent
      : {
          absent: text.noCurrent,
          loading: text.loadingCurrent,
          unavailable: text.missingCurrent,
        }[current.state]);
  const provider: LibraryTagComparisonProvider =
    comparison.state === "saved"
      ? comparison.provider
      : { status: comparison.state };
  const overrides =
    comparison.state === "saved" ? comparison.overrides : undefined;

  return (
    <>
      {locationPath && (
        <p className="m-0 text-sm wrap-anywhere">{locationPath}</p>
      )}
      {source?.state === "loading" && <p role="status">{text.loading}</p>}
      {source?.state === "failed" && (
        <div className="grid justify-items-start gap-2">
          <p role="alert" className={releaseControlAlertClass}>
            {text.loadFailed}
          </p>
          {onRetry && (
            <AppButton type="button" onPress={onRetry}>
              {text.retry}
            </AppButton>
          )}
        </div>
      )}
      {source?.state === "missing" && <p>{text.missingSource}</p>}
      {source?.state === "saved" && (
        <>
          {!Object.keys(source.tags).length && <p>{text.emptySource}</p>}
          {source.reader && (
            <p className={readerClass}>
              {text.reader}: {source.reader.name} {source.reader.version} ·{" "}
              {source.reader.contract}
            </p>
          )}
        </>
      )}
      {currentNotice && <p className="m-0 text-sm">{currentNotice}</p>}
      <LibraryTagEvidenceComparison
        locale={locale}
        source={sourceTags}
        provider={provider}
        overrides={overrides}
        overridesUnavailable={comparison.state === "unavailable"}
        current={currentTags}
        knownFields={libraryTagComparisonFields(locale)}
        showAll={showAll}
        onShowAllChange={onShowAllChange}
        differencesOnly={differencesOnly}
        onDifferencesChange={onDifferencesChange}
        showDifferencesControl={Boolean(sourceTags && currentTags)}
      />
    </>
  );
}
