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
import {
  type LibraryPositionTagEvidenceComparisonState,
  type LibraryPositionTagEvidenceCurrentState,
  LibraryPositionTagEvidenceFrame,
  type LibraryPositionTagEvidenceLocation,
  type LibraryPositionTagEvidenceSourceState,
  LibraryPositionTagEvidenceView,
} from "../../ui/features/library/LibraryPositionTagEvidenceView";

type Tags = Readonly<Record<string, readonly string[] | null>>;
type Locale = "en" | "ru";
type ReadOutcome =
  | { state: "failed" | "missing" }
  | { state: "saved"; metadata: SourceMetadataResponse };
type CurrentTags =
  | { state: "loading" | "absent" | "unavailable" }
  | { state: "saved"; tags: Tags };

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
  const rootId = location?.root_id;
  const readLocationId = location?.location_id;
  const [retry, setRetry] = useState(0);
  const [settled, setSettled] = useState<{
    attempt: number;
    result: ReadOutcome;
  }>();
  const read =
    settled?.attempt === retry ? settled.result : { state: "loading" as const };
  useEffect(() => {
    if (!rootId || !readLocationId || !variantId) return;
    const controller = new AbortController();
    const finish = (result: ReadOutcome) => {
      if (!controller.signal.aborted) setSettled({ attempt: retry, result });
    };
    void getSourceLocation(rootId, readLocationId, {
      signal: controller.signal,
      cache: "no-store",
    })
      .then((response) => {
        if (controller.signal.aborted) return;
        if (
          response.status !== 200 ||
          response.data.root_id !== rootId ||
          response.data.location_id !== readLocationId ||
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
  }, [rootId, readLocationId, variantId, retry]);

  const source: LibraryPositionTagEvidenceSourceState | undefined = !location
    ? undefined
    : read.state === "saved"
      ? {
          state: "saved",
          tags: read.metadata.tags,
          reader: read.metadata.provenance,
        }
      : { state: read.state };
  const currentState: LibraryPositionTagEvidenceCurrentState =
    current.state === "saved"
      ? { state: "saved", tags: current.tags }
      : { state: current.state };
  const comparison: LibraryPositionTagEvidenceComparisonState =
    evidence.state === "saved"
      ? {
          state: "saved",
          provider: evidence.data.provider,
          overrides: evidence.data.overrides,
        }
      : { state: evidence.state };

  return (
    <LibraryPositionTagEvidenceView
      locale={locale}
      locationPath={location?.relative_path}
      source={source}
      current={currentState}
      comparison={comparison}
      showAll={showAll}
      onShowAllChange={onShowAllChange}
      differencesOnly={differencesOnly}
      onDifferencesChange={onDifferencesChange}
      onRetry={() => setRetry((value) => value + 1)}
    />
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
  const [locationId, setLocationId] = useState("");
  const locations = source?.locations ?? [];
  const location =
    locations.length === 1
      ? locations[0]
      : locations.find(({ location_id }) => location_id === locationId);
  const locationOptions: LibraryPositionTagEvidenceLocation[] = locations.map(
    (item) => ({
      id: item.location_id,
      label: item.relative_path,
      rootId: item.root_id,
    }),
  );
  return (
    <LibraryPositionTagEvidenceFrame
      locale={comparison.locale}
      hasSource={Boolean(source)}
      locations={locationOptions}
      locationId={location?.location_id}
      onLocationChange={setLocationId}
    >
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
    </LibraryPositionTagEvidenceFrame>
  );
}

export function LibraryPositionTagEvidence({
  open,
  ...props
}: LibraryPositionTagEvidenceProps) {
  return open ? <PositionEvidenceContent {...props} /> : null;
}
