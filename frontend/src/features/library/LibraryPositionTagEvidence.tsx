import { useEffect, useState } from "react";
import {
  getLibraryPositionTagComparison,
  getSourceLocation,
} from "../../api/generated/client";
import type {
  LibrarySourceLocationResponse,
  LibraryTagComparisonResponse,
  LibraryTrackSourceResponse,
  SourceMetadataResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { LibraryTagEvidenceComparison } from "./LibraryTagComparisonTable";
import {
  releaseControlAlertClass,
  releaseControlSelectClass,
} from "./libraryReleaseControlStyles";
import { libraryTagComparisonFields } from "./libraryTagComparisonFields";

type Tags = Readonly<Record<string, readonly string[] | null>>;
type Locale = "en" | "ru";
type ReadOutcome =
  | { state: "failed" | "missing" }
  | { state: "saved"; metadata: SourceMetadataResponse };
type CurrentTags =
  | { state: "loading" | "absent" | "unavailable" }
  | { state: "saved"; tags: Tags };

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
};

type PositionEvidence =
  | { state: "loading" | "unavailable" }
  | { state: "saved"; data: LibraryTagComparisonResponse };

function PositionTagComparison({
  locale,
  variantId,
  location,
  current,
  evidence,
  showAll,
  onShowAllChange,
  differencesOnly,
  onDifferencesChange,
}: {
  locale: Locale;
  variantId?: string;
  location?: LibrarySourceLocationResponse;
  current: CurrentTags;
  evidence: PositionEvidence;
  showAll: boolean;
  onShowAllChange: (selected: boolean) => void;
  differencesOnly: boolean;
  onDifferencesChange: (selected: boolean) => void;
}) {
  const text = copy[locale];
  const rootId = location?.root_id;
  const locationId = location?.location_id;
  const [retry, setRetry] = useState(0);
  const [settled, setSettled] = useState<{
    attempt: number;
    result: ReadOutcome;
  }>();
  const read =
    settled?.attempt === retry ? settled.result : { state: "loading" as const };
  useEffect(() => {
    if (!rootId || !locationId || !variantId) return;
    const controller = new AbortController();
    const finish = (result: ReadOutcome) => {
      if (!controller.signal.aborted) setSettled({ attempt: retry, result });
    };
    void getSourceLocation(rootId, locationId, {
      signal: controller.signal,
      cache: "no-store",
    })
      .then((response) => {
        if (controller.signal.aborted) return;
        if (
          response.status !== 200 ||
          response.data.root_id !== rootId ||
          response.data.location_id !== locationId ||
          (response.data.media_variant_id &&
            response.data.media_variant_id !== variantId)
        ) {
          finish({ state: "failed" });
          return;
        }
        const metadata = response.data.steps?.find(
          (step) => step.name === "metadata",
        )?.metadata;
        finish(
          metadata && response.data.media_variant_id === variantId
            ? { state: "saved", metadata }
            : { state: "missing" },
        );
      })
      .catch(() => finish({ state: "failed" }));
    return () => controller.abort();
  }, [rootId, locationId, variantId, retry]);

  const sourceTags = read.state === "saved" ? read.metadata.tags : undefined;
  const currentTags = current.state === "saved" ? current.tags : undefined;
  const currentNotice =
    current.state === "saved"
      ? !Object.keys(current.tags).length
        ? text.emptyCurrent
        : undefined
      : {
          absent: text.noCurrent,
          loading: text.loadingCurrent,
          unavailable: text.missingCurrent,
        }[current.state];

  return (
    <>
      {location && (
        <p className="m-0 text-sm wrap-anywhere">{location.relative_path}</p>
      )}
      {location && read.state === "loading" && (
        <p role="status">{text.loading}</p>
      )}
      {read.state === "failed" && (
        <div className="grid justify-items-start gap-2">
          <p role="alert" className={releaseControlAlertClass}>
            {text.loadFailed}
          </p>
          <AppButton
            type="button"
            onPress={() => setRetry((value) => value + 1)}
          >
            {text.retry}
          </AppButton>
        </div>
      )}
      {read.state === "missing" && <p>{text.missingSource}</p>}
      {read.state === "saved" && (
        <>
          {!Object.keys(read.metadata.tags).length && <p>{text.emptySource}</p>}
          <p className="m-0 text-xs text-[var(--muted)] wrap-anywhere">
            {text.reader}: {read.metadata.provenance.name}{" "}
            {read.metadata.provenance.version} ·{" "}
            {read.metadata.provenance.contract}
          </p>
        </>
      )}
      {currentNotice && <p className="m-0 text-sm">{currentNotice}</p>}
      <LibraryTagEvidenceComparison
        locale={locale}
        source={sourceTags}
        provider={
          evidence.state === "saved"
            ? evidence.data.provider
            : { status: evidence.state }
        }
        overrides={
          evidence.state === "saved" ? evidence.data.overrides : undefined
        }
        overridesUnavailable={evidence.state === "unavailable"}
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

type LibraryPositionTagEvidenceProps = {
  locale: Locale;
  open: boolean;
  releaseId: string;
  positionId: string;
  refreshToken?: string;
  source?: Pick<LibraryTrackSourceResponse, "media_variant_id" | "locations">;
  current: CurrentTags;
};

function PositionEvidenceContent({
  locale,
  releaseId,
  positionId,
  refreshToken,
  source,
  current,
}: Omit<LibraryPositionTagEvidenceProps, "open">) {
  const [differencesOnly, setDifferencesOnly] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const requestKey = JSON.stringify([releaseId, positionId, refreshToken]);
  const [settled, setSettled] = useState<{
    key: string;
    evidence: PositionEvidence;
  }>();
  const evidence: PositionEvidence =
    settled?.key === requestKey ? settled.evidence : { state: "loading" };
  useEffect(() => {
    const controller = new AbortController();
    const finish = (next: PositionEvidence) => {
      if (!controller.signal.aborted) {
        setSettled({ key: requestKey, evidence: next });
      }
    };
    void getLibraryPositionTagComparison(releaseId, positionId, {
      signal: controller.signal,
      cache: "no-store",
    })
      .then((response) => {
        if (response.status === 200) {
          finish({ state: "saved", data: response.data });
        } else {
          finish({ state: "unavailable" });
        }
      })
      .catch(() => finish({ state: "unavailable" }));
    return () => controller.abort();
  }, [releaseId, positionId, requestKey]);
  return (
    <PositionSourceEvidence
      key={source?.media_variant_id}
      source={source}
      locale={locale}
      current={current}
      evidence={evidence}
      showAll={showAll}
      onShowAllChange={setShowAll}
      differencesOnly={differencesOnly}
      onDifferencesChange={setDifferencesOnly}
    />
  );
}

function PositionSourceEvidence({
  source,
  ...comparison
}: Omit<Parameters<typeof PositionTagComparison>[0], "variantId" | "location"> &
  Pick<LibraryPositionTagEvidenceProps, "source">) {
  const text = copy[comparison.locale];
  const [locationId, setLocationId] = useState("");
  const locations = source?.locations ?? [];
  const location =
    locations.length === 1
      ? locations[0]
      : locations.find(({ location_id }) => location_id === locationId);
  return (
    <div className="mt-3 grid min-w-0 grid-cols-1 gap-3">
      {!source && <p>{text.inspectSource}</p>}
      {source && !locations.length && <p>{text.noLocation}</p>}
      {locations.length > 1 && (
        <label className="grid gap-1 text-sm">
          {text.chooseLocation}
          <select
            className={releaseControlSelectClass}
            value={location?.location_id ?? ""}
            onChange={(event) => setLocationId(event.currentTarget.value)}
          >
            <option value="">{text.selectLocation}</option>
            {locations.map((item) => (
              <option key={item.location_id} value={item.location_id}>
                {item.relative_path} · {item.root_id}
              </option>
            ))}
          </select>
        </label>
      )}
      <PositionTagComparison
        key={
          location
            ? `${source?.media_variant_id}:${location.root_id}:${location.location_id}:${location.mtime}`
            : "no-location"
        }
        {...comparison}
        variantId={source?.media_variant_id}
        location={location}
      />
    </div>
  );
}

export function LibraryPositionTagEvidence({
  open,
  ...props
}: LibraryPositionTagEvidenceProps) {
  return open ? <PositionEvidenceContent {...props} /> : null;
}
