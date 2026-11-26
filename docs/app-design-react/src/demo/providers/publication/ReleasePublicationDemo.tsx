import { useState } from "react";
import {
  type AggregateDisposition,
  type AggregateInspectorPresentation,
  type AggregatePositionView,
  AggregatePublicationPreviewView,
} from "../../../ui/features/library/AggregatePublicationPreviewView";
import type { PublicationPreviewPlan } from "../../../ui/features/library/PublicationPreviewDetailsView";
import { useI18n } from "../demoPreferences";
import { type OriginalRelease, trackNumberLabel } from "../library/catalogData";
import type { DemoPublicationJob } from "../operations/useOperationsDemoProvider";
import {
  type DemoPublicationCatalog,
  type DemoPublicationMode,
  type DemoPublicationRow,
  isReadyPublication,
  planDemoPublication,
} from "./libraryPublicationDemo";

export interface ReleasePublicationDemoProvider {
  catalog: DemoPublicationCatalog;
  pending: readonly DemoPublicationJob[];
  onQueue: (plans: readonly DemoPublicationRow[]) => void;
}

function inspectorPlan(row: DemoPublicationRow): PublicationPreviewPlan {
  const currentTags = row.current?.tags ?? {};
  const plannedTags = row.output?.tags ?? {};
  const tagDifferences = row.output
    ? [...new Set([...Object.keys(currentTags), ...Object.keys(plannedTags)])]
        .filter(
          (key) =>
            JSON.stringify(currentTags[key]) !==
            JSON.stringify(plannedTags[key]),
        )
        .map((key) => ({
          key,
          current: currentTags[key],
          current_present: Object.hasOwn(currentTags, key),
          desired: plannedTags[key],
          desired_present: Object.hasOwn(plannedTags, key),
        }))
    : undefined;
  const currentCover = row.current?.coverHash ?? null;
  const plannedCover = row.output?.coverHash ?? null;
  const currentLyrics = row.current?.lyrics ?? null;
  const plannedLyrics = row.output?.lyrics ?? null;
  const assets = row.output && {
    cover:
      currentCover !== plannedCover
        ? { current: currentCover, planned: plannedCover }
        : undefined,
    lyrics:
      currentLyrics !== plannedLyrics
        ? { current: currentLyrics, planned: plannedLyrics }
        : undefined,
  };
  return {
    asset_changes: assets || undefined,
    blockers: row.reason ? [row.reason] : undefined,
    confirmation_capability: row.current
      ? { status: isReadyPublication(row) ? "ready" : row.disposition }
      : undefined,
    current_comparison: row.current
      ? {
          current_components: [row.current.path],
          rendered_components: row.renderedPath
            ? [row.renderedPath]
            : undefined,
          resolved_components: row.resolved_path
            ? [row.resolved_path]
            : undefined,
          audio_status:
            row.source && row.source.id === row.current.sourceId
              ? "matches"
              : "",
          cover_status: row.output
            ? currentCover === plannedCover
              ? "matches"
              : "differs"
            : "",
          tag_differences: tagDifferences,
        }
      : undefined,
    desired_tags: row.output?.tags,
    resolved_path: row.resolved_path,
    selected_source: row.source
      ? { codec: row.source.codec, path: row.source.path }
      : undefined,
    showAudioComparisonBasis: false,
    transform: row.output
      ? {
          action: "copy",
          bitrate: 0,
          extension: "",
          output_container: row.output.container,
        }
      : undefined,
    warnings: row.downgrade ? ["output_quality_would_downgrade"] : undefined,
  };
}

export function ReleasePublicationDemo({
  release,
  provider,
}: {
  release: OriginalRelease;
  provider: ReleasePublicationDemoProvider;
}) {
  const { locale } = useI18n();
  const [mode, setMode] = useState<DemoPublicationMode>("ordinary");
  const [included, setIncluded] = useState(
    () => new Set(release.tracks.map(({ id }) => id)),
  );
  const [selectedPositionId, setSelectedPositionId] = useState(
    release.tracks[0]?.id ?? "",
  );
  const [prepared, setPrepared] = useState<
    ReadonlyMap<string, DemoPublicationRow>
  >(new Map());
  const running = new Map(
    provider.pending
      .filter((job) => job.releaseId === release.id)
      .map((job) => [job.positionId, job.id]),
  );
  const selected = release.tracks.filter(({ id }) => included.has(id));
  const canPrepare = selected.length > 0;
  const ready: DemoPublicationRow[] = [];
  const counts: Record<AggregateDisposition, number> = {
    create: 0,
    update: 0,
    unchanged: 0,
    skipped: 0,
    unprocessable: 0,
    blocked: 0,
  };
  const positions = release.tracks.map((track): AggregatePositionView => {
    const row = prepared.get(track.id);
    const job = running.get(track.id);
    const disposition = !included.has(track.id)
      ? "skipped"
      : job
        ? "blocked"
        : (row?.disposition ?? "blocked");
    counts[disposition]++;
    if (included.has(track.id) && !job && row && isReadyPublication(row))
      ready.push(row);
    return {
      id: track.id,
      label: `${trackNumberLabel(release, track)}. ${track.title}`,
      status:
        job || row
          ? { kind: "disposition", disposition }
          : { kind: "not-prepared" },
      diagnostic: row?.downgrade ? { kind: "quality-downgrade" } : undefined,
    };
  });
  const canConfirm =
    ready.length > 0 && !counts.blocked && !counts.unprocessable;
  const position =
    positions.find(({ id }) => id === selectedPositionId) ?? positions[0];
  const detail = position && prepared.get(position.id);
  const operationId = position && running.get(position.id);
  const inspector: AggregateInspectorPresentation =
    detail || operationId
      ? {
          kind: "publication",
          details: {
            error: "",
            admittedOperationID: operationId ?? "",
            admissionUncertain: false,
            loading: false,
            inspection: {
              workflow: detail?.current ? "retained_same_path" : "first",
              preview: detail ? inspectorPlan(detail) : undefined,
              sourceCandidates: [],
              selectedChoice: null,
              needsExplicitChoice: false,
              sourceChoiceDisabled: true,
            },
          },
          onRefresh: prepare,
        }
      : { kind: position ? "not-prepared" : "empty" };

  function prepare() {
    if (!canPrepare) return;
    const rows = planDemoPublication(
      provider.catalog,
      selected.map((track) => ({
        release_id: release.id,
        position_id: track.id,
      })),
      mode,
    );
    setPrepared(new Map(rows.map((row) => [row.position_id, row])));
  }

  return (
    <AggregatePublicationPreviewView
      locale={locale}
      mode={mode}
      included={included}
      positions={positions}
      counts={counts}
      selectedPositionId={selectedPositionId}
      canPrepare={canPrepare}
      canConfirm={canConfirm}
      preparing={false}
      inspector={inspector}
      onSelectPosition={setSelectedPositionId}
      onModeChange={(next) => {
        setMode(next);
        setPrepared(new Map());
      }}
      onToggleInclude={(id) =>
        setIncluded((current) => {
          const next = new Set(current);
          if (next.has(id)) next.delete(id);
          else next.add(id);
          return next;
        })
      }
      onPrepare={prepare}
      onConfirm={() => {
        if (!canConfirm) return;
        const live = planDemoPublication(provider.catalog, ready, mode);
        const retained: DemoPublicationRow[] = [];
        const stale = new Map<string, DemoPublicationRow>();
        for (const [index, row] of ready.entries()) {
          if (row.evidence && row.evidence === live[index]?.evidence) {
            retained.push(row);
          } else {
            stale.set(row.position_id, {
              ...row,
              disposition: "blocked",
              reason: "stale_authority",
            });
          }
        }
        if (retained.length) provider.onQueue(retained);
        setPrepared(stale);
      }}
    />
  );
}
