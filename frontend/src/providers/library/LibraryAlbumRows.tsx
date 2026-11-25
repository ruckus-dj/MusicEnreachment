import { useState } from "react";
import { useGetLibraryRelease } from "../../api/generated/client";
import type {
  LibraryArtistCreditResponse,
  LibraryReleaseDetailResponse,
  LibraryReleaseItem,
  LibraryReleaseSummary,
  MissingReleaseGroupSummary,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import {
  type LibraryAlbumCredit,
  type LibraryAlbumDetail,
  LibraryAlbumListView,
  type LibraryAlbumRow,
  type LibraryAlbumSelectionControl,
} from "../../ui/features/library/LibraryAlbumListView";
import type { LibraryAlbumPosition } from "../../ui/features/library/LibraryAlbumPositionsView";
import { useMissingReleaseGroupEditions } from "./MissingReleaseGroupRow";
import type { LibrarySelectionScope } from "./useLibrarySelection";

type SelectionControl = (
  scope: LibrarySelectionScope,
) => LibraryAlbumSelectionControl;

function releaseHref(id: string) {
  return `#/library/releases/${encodeURIComponent(id)}`;
}

function creditsText(credits: LibraryArtistCreditResponse[]) {
  const names = credits
    .map(
      (credit, index) =>
        `${credit.credit_name || credit.artist_name || ""}${index < credits.length - 1 ? (credit.join_phrase ?? " ") : ""}`,
    )
    .join("");
  return names || "—";
}

function creditLinks(
  credits: LibraryArtistCreditResponse[],
): LibraryAlbumCredit[] {
  return credits.map((credit) => ({
    name: credit.credit_name || credit.artist_name || "",
    href: credit.artist_id
      ? `#/library/artists/${encodeURIComponent(credit.artist_id)}`
      : null,
    joinPhrase: credit.join_phrase,
  }));
}

interface AlbumEntry {
  row: LibraryAlbumRow;
  release?: LibraryReleaseSummary;
  group?: MissingReleaseGroupSummary;
}

function editionNote(release: LibraryReleaseSummary) {
  return (
    [
      release.edition.country,
      ...release.edition.labels,
      ...release.edition.catalog_numbers,
    ]
      .filter(Boolean)
      .join(" · ") || null
  );
}

function albumEntry(
  item: LibraryReleaseItem,
  selectionControl: SelectionControl,
): AlbumEntry | null {
  const release = item.library_release;
  const group = item.missing_release_group;
  if (!release && !group) return null;
  const musicbrainz =
    Boolean(group) ||
    Boolean(
      release?.provider_links.some(
        (link) => link.provider_code === "musicbrainz",
      ),
    );
  return {
    row: {
      id: release ? `local:${release.id}` : `provider:${group?.provider_key}`,
      title: release?.title || group?.title || "",
      href: release ? releaseHref(release.id) : null,
      missing: !release,
      artist: creditsText(release?.artists ?? group?.artists ?? []),
      date: release
        ? (release.edition.release_date ??
          release.edition.original_release_date)
        : (group?.original_release_date ?? null),
      editionNote: release ? editionNote(release) : null,
      positionCount: release?.counts.position_count ?? null,
      sourceCount: null,
      availableCount: release?.counts.available_position_count ?? null,
      providerPositionCount: release?.counts.provider_position_count ?? null,
      publishedCount: release?.counts.published_position_count ?? null,
      provider: musicbrainz ? "musicbrainz" : "local",
      selection: release
        ? selectionControl({ kind: "release", id: release.id })
        : null,
    },
    release,
    group,
  };
}

function releasePositions(
  release: LibraryReleaseSummary,
  detail: LibraryReleaseDetailResponse,
  selectionControl: SelectionControl,
): LibraryAlbumPosition[] {
  const tracks = [...(detail.tracks ?? [])].sort(
    (a, b) => a.medium - b.medium || a.position - b.position,
  );
  return tracks.map((track) => {
    const sources = track.sources ?? [];
    const paths = [
      ...new Set(
        sources.flatMap(
          (source) =>
            source.locations?.map((location) => location.relative_path) ?? [],
        ),
      ),
    ];
    const href = `${releaseHref(release.id)}/positions/${encodeURIComponent(track.id)}`;
    return {
      id: track.id,
      numberLabel: `${track.medium}.${track.displayed_number || track.position}`,
      title: track.title,
      recordingHref: href,
      tagsHref: href,
      artist: creditsText(track.artists ?? []),
      durationMs: track.duration_ms ?? undefined,
      sourceCount: sources.length,
      paths,
      publication: track.publication
        ? {
            state: "published" as const,
            container: track.publication.container,
            codec: track.publication.codec,
            matchingConfidence: track.publication.matching_confidence,
          }
        : { state: "not-published" as const },
      selection: selectionControl({
        kind: "position",
        position: {
          release_id: release.id,
          position_id: track.id,
          title: track.title,
          release_title: release.title,
        },
      }),
    };
  });
}

export function LibraryAlbumRows({
  items,
  selectionControl,
}: {
  items: LibraryReleaseItem[];
  selectionControl: SelectionControl;
}) {
  const { locale } = useI18n();
  const [expanded, setExpanded] = useState<string | null>(null);
  const entries = items.flatMap((item) => {
    const entry = albumEntry(item, selectionControl);
    return entry ? [entry] : [];
  });
  const expandedEntry =
    entries.find((entry) => entry.row.id === expanded) ?? null;
  const activeRelease = expandedEntry?.release;
  const activeGroup = expandedEntry?.group ?? null;
  const releaseQuery = useGetLibraryRelease(activeRelease?.id ?? "", {
    query: { enabled: Boolean(activeRelease) },
  });
  const missingProjection = useMissingReleaseGroupEditions(activeGroup, {
    enabled: Boolean(activeGroup),
    initialPickerOpen: true,
  });

  let detail: LibraryAlbumDetail | null = null;
  if (activeRelease) {
    if (releaseQuery.isLoading) {
      detail = { kind: "loading" };
    } else if (releaseQuery.isError || releaseQuery.data?.status !== 200) {
      detail = {
        kind: "error",
        onRetry: () => void releaseQuery.refetch(),
      };
    } else {
      detail = {
        kind: "positions",
        releaseTitle: activeRelease.title,
        positions: releasePositions(
          activeRelease,
          releaseQuery.data.data,
          selectionControl,
        ),
      };
    }
  } else if (activeGroup) {
    detail = {
      kind: "missing-edition",
      originalReleaseDate: activeGroup.original_release_date,
      credits: creditLinks(activeGroup.artists),
      ...missingProjection.viewProps,
    };
  }

  return (
    <LibraryAlbumListView
      locale={locale}
      albums={entries.map((entry) => entry.row)}
      expandedId={expanded}
      onToggleExpanded={(id) =>
        setExpanded((current) => (current === id ? null : id))
      }
      pageSelection={selectionControl({ kind: "page" })}
      detail={detail}
    />
  );
}
