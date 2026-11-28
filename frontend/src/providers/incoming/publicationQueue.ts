import { useQuery } from "@tanstack/react-query";
import { useCallback } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  getLibraryRelease,
  listLibraryPositions,
  previewLibraryPublicationSelection,
} from "../../api/generated/client";
import type {
  LibraryArtistCreditResponse,
  LibraryPositionSummary,
} from "../../api/generated/client.schemas";
import type {
  IncomingPublicationEdition,
  IncomingPublicationNextStep,
  IncomingPublicationReason,
  IncomingPublicationTrack,
  IncomingPublicationTrackAttention,
} from "../../ui/features/incoming/incomingPublicationModel";
import {
  libraryPositionHref,
  libraryReleaseHref,
} from "../../ui/features/library/libraryDetailRoute";
import { libraryReleasePrimaryArtistHref } from "../library/libraryRoute";

/**
 * Read-only Incoming publication queue.
 *
 * Matching removes assigned files from Incoming, but the library position keeps
 * the exact `track_source` links. The queue therefore starts from unpublished
 * catalogue positions with at least one linked source, then asks the existing
 * publication planner for the real per-position disposition. Availability alone
 * never claims readiness.
 */

const incomingPublicationQueuePageSize = 100;
const releaseDetailFetchConcurrency = 6;

type PublicationQueueStatus =
  | { kind: "loading" }
  | { kind: "error"; retry: () => void };

const previewKey = (releaseId: string, positionId: string) =>
  `${releaseId}:${positionId}`;

type PublicationQueuePosition = {
  id: string;
  release: {
    id: string;
    title: string;
    artists: readonly LibraryArtistCreditResponse[];
  };
  source_variant_count: number;
  selected_media_variant_id?: string | null;
  title: string;
  medium: number;
  position: number;
  displayed_number: string;
};

type PublicationQueueSourceLocation = {
  relative_path: string;
  probe_status?: string;
};

type PublicationQueueTrackSource = {
  selected: boolean;
  locations?: readonly PublicationQueueSourceLocation[] | null;
};

type PublicationQueueTrack = {
  id: string;
  title: string;
  medium: number;
  position: number;
  displayed_number: string;
  publication?: unknown | null;
  sources?: readonly PublicationQueueTrackSource[] | null;
};

type PublicationQueueRelease = {
  id: string;
  title: string;
  metadata?: unknown;
  primary_artist: {
    effective_artist_id: string | null;
    candidate_artist_ids?: readonly string[] | null;
  };
  artists?: readonly LibraryArtistCreditResponse[] | null;
  tracks?: readonly PublicationQueueTrack[] | null;
};

type PublicationQueueDisposition = {
  disposition: string;
  reason?: string;
  path_clash?: boolean;
  resolved_path?: string;
};

type PublicationQueueResult = {
  editions: IncomingPublicationEdition[];
};

function incomingPublicationQueueQueryKey() {
  return ["incoming", "publication-queue"] as const;
}

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : undefined;
}

/** Reads only the saved local release date; no extra catalogue request. */
function releaseYear(release: PublicationQueueRelease) {
  const desired = record(record(release.metadata)?.local_metadata)?.desired;
  const date = record(record(desired)?.release_date);
  const value = date?.state === "value" ? date.value : undefined;
  return typeof value === "string" ? value.slice(0, 4) : "";
}

function primaryArtistName(release: PublicationQueueRelease) {
  const effective = release.primary_artist.effective_artist_id;
  const credit = effective
    ? release.artists?.find((item) => item.artist_id === effective)
    : undefined;
  return credit ? credit.credit_name || credit.artist_name || "" : "";
}

function trackNumber(track: PublicationQueueTrack) {
  const displayed = track.displayed_number.trim();
  return displayed || String(track.position);
}

/** Only an explicitly selected variant is a publication source. */
function selectedSource(track: PublicationQueueTrack) {
  const source = (track.sources ?? []).find((item) => item.selected);
  if (!source) return undefined;
  const locations = source.locations ?? [];
  return (
    locations.find((item) => item.probe_status === "audio")?.relative_path ??
    locations[0]?.relative_path
  );
}

function trackAttention(
  item: PublicationQueueDisposition | undefined,
): IncomingPublicationTrackAttention | undefined {
  if (!item) return "publicationBlocked";
  if (item.path_clash) return "publicationBlocked";
  if (item.disposition === "create") return undefined;
  switch (item.reason) {
    case "source_selection_required":
      return "sourceNeedsSelection";
    case "missing_audio":
      return "audioUnavailable";
    default:
      return "publicationBlocked";
  }
}

function attentionReason(
  attention: IncomingPublicationTrackAttention,
  track: IncomingPublicationTrack,
): IncomingPublicationReason {
  switch (attention) {
    case "sourceNeedsSelection":
      return {
        code: "sourceNeedsSelection",
        trackId: track.id,
        trackTitle: track.title,
      };
    case "audioUnavailable":
      return {
        code: "audioUnavailable",
        trackId: track.id,
        trackTitle: track.title,
      };
    case "publicationInProgress":
      return {
        code: "publicationInProgress",
        trackId: track.id,
        trackTitle: track.title,
      };
    default:
      return {
        code: "publicationBlocked",
        trackId: track.id,
        trackTitle: track.title,
      };
  }
}

function trackHref(releaseId: string, positionId: string) {
  return libraryPositionHref(releaseId, positionId, {
    tab: "publication",
    from: "publication-queue",
  });
}

/**
 * Projects catalogue positions and planner dispositions into the portable
 * queue model. Release detail is used only to confirm selected source identity,
 * primary-artist resolution and already-current races.
 */
export function buildPublicationEditions({
  positions,
  releases,
  preview,
}: {
  positions: readonly PublicationQueuePosition[];
  releases: ReadonlyMap<string, PublicationQueueRelease>;
  preview: ReadonlyMap<string, PublicationQueueDisposition>;
}): IncomingPublicationEdition[] {
  const grouped = new Map<string, PublicationQueuePosition[]>();
  for (const position of positions) {
    if (position.source_variant_count < 1) continue;
    const releaseId = position.release.id;
    const rows = grouped.get(releaseId);
    if (rows) rows.push(position);
    else grouped.set(releaseId, [position]);
  }

  const editions: IncomingPublicationEdition[] = [];
  for (const [releaseId, rows] of grouped) {
    const release = releases.get(releaseId);
    if (!release) continue;
    const detailTracks = new Map(
      (release.tracks ?? []).map((track) => [track.id, track]),
    );
    const candidates = rows
      .flatMap((row) => {
        const track = detailTracks.get(row.id);
        const disposition = preview.get(
          previewKey(releaseId, row.id),
        )?.disposition;
        if (
          !track ||
          track.publication ||
          disposition === "update" ||
          disposition === "unchanged" ||
          (track.sources?.length ?? 0) === 0
        )
          return [];
        return [{ row, track }];
      })
      .sort(
        (left, right) =>
          left.track.medium - right.track.medium ||
          left.track.position - right.track.position ||
          left.track.id.localeCompare(right.track.id),
      );
    if (candidates.length === 0) continue;

    const tracks: IncomingPublicationTrack[] = candidates.map(({ track }) => {
      const href = trackHref(releaseId, track.id);
      const attention = trackAttention(
        preview.get(previewKey(releaseId, track.id)),
      );
      const source = selectedSource(track);
      return {
        id: track.id,
        number: trackNumber(track),
        title: track.title,
        href,
        ...(source ? { source } : {}),
        ...(attention ? { attention } : {}),
        ...(attention ? { nextStep: { kind: "track" as const, href } } : {}),
      };
    });

    /*
     * A valid output template can omit the primary artist, so an unresolved
     * artist is only a blocker when the planner reports a path/context
     * failure. Planner create/update remains authoritative for readiness.
     */
    const unresolvedArtistTrack = candidates.find(({ track }) => {
      const reason = preview.get(previewKey(releaseId, track.id))?.reason;
      return reason === "publication_path_unavailable";
    });
    const primaryArtistActionTrack =
      !release.primary_artist.effective_artist_id &&
      release.primary_artist.candidate_artist_ids?.length
        ? unresolvedArtistTrack?.track
        : undefined;
    const attentionTrack = tracks.find((track) => track.attention);
    const reason: IncomingPublicationReason = primaryArtistActionTrack
      ? { code: "missingPrimaryArtist" }
      : attentionTrack?.attention
        ? attentionReason(attentionTrack.attention, attentionTrack)
        : { code: "manualReady" };
    const firstTrack = tracks[0];
    const nextStep: IncomingPublicationNextStep = primaryArtistActionTrack
      ? {
          kind: "primaryArtist",
          href: libraryReleasePrimaryArtistHref(
            releaseId,
            primaryArtistActionTrack.id,
          ),
        }
      : attentionTrack?.href
        ? { kind: "track", href: attentionTrack.href }
        : firstTrack?.href
          ? { kind: "track", href: firstTrack.href }
          : { kind: "release", href: libraryReleaseHref(releaseId) };

    editions.push({
      id: `publication-${releaseId}`,
      releaseId,
      title: release.title || rows[0].release.title,
      artist: primaryArtistName(release),
      year: releaseYear(release),
      reason,
      nextStep,
      tracks,
    });
  }
  return editions;
}

async function readReleaseDetails(
  releaseIds: readonly string[],
  signal: AbortSignal,
) {
  const details = new Map<string, PublicationQueueRelease>();
  for (
    let index = 0;
    index < releaseIds.length;
    index += releaseDetailFetchConcurrency
  ) {
    const batch = releaseIds.slice(
      index,
      index + releaseDetailFetchConcurrency,
    );
    const responses = await Promise.all(
      batch.map((releaseId) =>
        getLibraryRelease(releaseId, { signal, cache: "no-store" }),
      ),
    );
    responses.forEach((response, offset) => {
      if (response.status !== 200)
        throw apiResponseError(
          response,
          "publication queue release unavailable",
        );
      details.set(batch[offset], response.data);
    });
  }
  return details;
}

/** Pages the whole unpublished catalogue; `source_variant_count` is exact. */
async function readPublicationPositions(
  signal: AbortSignal,
): Promise<LibraryPositionSummary[]> {
  const positions = new Map<string, LibraryPositionSummary>();
  let offset = 0;
  for (;;) {
    const response = await listLibraryPositions(
      {
        limit: incomingPublicationQueuePageSize,
        offset,
        publication: "unpublished",
        availability: "all",
      },
      { signal, cache: "no-store" },
    );
    if (response.status !== 200) {
      throw apiResponseError(
        response,
        "publication queue positions unavailable",
      );
    }
    const items = response.data.items ?? [];
    for (const item of items) {
      if (item.source_variant_count > 0) positions.set(item.id, item);
    }
    offset += items.length;
    if (items.length === 0 || offset >= (response.data.total ?? offset)) break;
  }
  return [...positions.values()];
}

/** Planner previews are sequential chunks of the API maximum. */
async function readPublicationDispositions(
  positions: readonly PublicationQueuePosition[],
  signal: AbortSignal,
): Promise<Map<string, PublicationQueueDisposition>> {
  const preview = new Map<string, PublicationQueueDisposition>();
  const firstByPath = new Map<string, PublicationQueueDisposition>();
  for (
    let index = 0;
    index < positions.length;
    index += incomingPublicationQueuePageSize
  ) {
    const chunk = positions.slice(
      index,
      index + incomingPublicationQueuePageSize,
    );
    const response = await previewLibraryPublicationSelection(
      {
        positions: chunk.map((position) => ({
          release_id: position.release.id,
          position_id: position.id,
        })),
        prepare_covers: false,
      },
      { signal, cache: "no-store" },
    );
    if (response.status !== 200) {
      throw apiResponseError(
        response,
        "publication queue readiness unavailable",
      );
    }
    for (const item of response.data.items ?? []) {
      const entry = { ...item };
      preview.set(previewKey(item.release_id, item.position_id), entry);
      if (entry.disposition !== "create" || !entry.resolved_path) continue;
      // The server checks only one preview batch at a time. Check colliding
      // targets across batches too, so neither position is advertised as ready.
      const first = firstByPath.get(entry.resolved_path);
      if (first) {
        first.path_clash = true;
        entry.path_clash = true;
      } else {
        firstByPath.set(entry.resolved_path, entry);
      }
    }
  }
  return preview;
}

async function readPublicationQueue(
  signal: AbortSignal,
): Promise<PublicationQueueResult> {
  const positions = await readPublicationPositions(signal);
  if (positions.length === 0) return { editions: [] };

  const releaseIds = [
    ...new Set(positions.map((position) => position.release.id)),
  ];
  const [details, preview] = await Promise.all([
    readReleaseDetails(releaseIds, signal),
    readPublicationDispositions(positions, signal),
  ]);

  return {
    editions: buildPublicationEditions({
      positions,
      releases: details,
      preview,
    }),
  };
}

export function useIncomingPublicationQueue(enabled: boolean): {
  editions: IncomingPublicationEdition[];
  status?: PublicationQueueStatus;
} {
  const query = useQuery({
    queryKey: incomingPublicationQueueQueryKey(),
    enabled,
    queryFn: ({ signal }) => readPublicationQueue(signal),
    retry: false,
    staleTime: 0,
  });
  const retry = useCallback(() => {
    void query.refetch();
  }, [query.refetch]);
  if (!enabled) {
    /*
     * Never claim zero merely because the tab is not active. Reuse an earlier
     * successful read when present; otherwise the tab shows loading.
     */
    return query.data
      ? { editions: query.data.editions }
      : { editions: [], status: { kind: "loading" } };
  }
  if (query.isError) return { editions: [], status: { kind: "error", retry } };
  if (query.isPending || query.isFetching)
    return { editions: [], status: { kind: "loading" } };
  return { editions: query.data?.editions ?? [] };
}
