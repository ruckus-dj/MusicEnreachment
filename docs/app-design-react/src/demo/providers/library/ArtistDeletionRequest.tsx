import { useState } from "react";
import { catalogueMessage } from "../../../ui/features/library/catalogueMessages";
import { LibraryDeletionControlView } from "../../../ui/features/library/LibraryDeletionControlView";
import { useI18n } from "../demoPreferences";
import type { CatalogArtist, OriginalRelease } from "./catalogData";
import {
  artistCoartistWarnings,
  buildDeletionDialog,
  buildDeletionPreview,
  type DeletionOperation,
  publicationOutputs,
  trackLabel,
} from "./DeletionRequest";

export function artistDeletionPlan(
  artistId: string,
  artists: CatalogArtist[],
  releases: OriginalRelease[],
  locale: "en" | "ru" = "ru",
) {
  const artist = artists.find((item) => item.id === artistId);
  if (!artist) return null;

  const owned = releases.filter((release) => release.artistId === artistId);
  const tracks = owned.flatMap((release) => release.tracks);
  const retainedRecordingIds = new Set(
    releases
      .filter((release) => release.artistId !== artistId)
      .flatMap((release) => release.tracks.map((track) => track.recordingId)),
  );
  const removedRecordingIds = new Set(
    tracks.flatMap(({ recordingId }) =>
      recordingId && !retainedRecordingIds.has(recordingId)
        ? [recordingId]
        : [],
    ),
  );
  const paths = tracks.flatMap(({ published }) =>
    published ? [published.path] : [],
  );

  return {
    kind: "artist" as const,
    libraryArtistId: artistId,
    libraryReleaseIds: owned.map((release) => release.id),
    libraryTrackIds: tracks.map((track) => track.id),
    target: artist.name,
    scope: catalogueMessage(locale, "deleteArtistScope"),
    publications: paths.length,
    paths,
    removedRecordings: removedRecordingIds.size,
    owned,
  };
}

export function ArtistDeletionRequest({
  artistId,
  artists,
  releases,
  onQueue,
}: {
  artistId: string;
  artists: CatalogArtist[];
  releases: OriginalRelease[];
  onQueue: (operation: DeletionOperation) => void;
}) {
  const { locale } = useI18n();
  const [frozen, setFrozen] =
    useState<NonNullable<ReturnType<typeof artistDeletionPlan>>>();
  const plan = artistDeletionPlan(artistId, artists, releases, locale);
  if (!plan) return null;
  const m = (key: Parameters<typeof catalogueMessage>[1]) =>
    catalogueMessage(locale, key);
  const warnings = frozen
    ? artistCoartistWarnings(artistId, frozen.target, frozen.owned, artists)
    : [];
  return (
    <LibraryDeletionControlView
      locale={locale}
      kind="artist"
      trigger={{ label: m("deleteArtist"), onPress: () => setFrozen(plan) }}
      dialog={buildDeletionDialog({
        locale,
        targetLabel: frozen?.target ?? plan.target,
        scope: m("deleteArtistScope"),
        open: Boolean(frozen),
        onOpenChange: (open) => {
          if (!open) setFrozen(undefined);
        },
        onConfirm: () => {
          if (!frozen) return;
          const {
            owned: _owned,
            removedRecordings: _recordings,
            ...operation
          } = frozen;
          onQueue(operation);
          setFrozen(undefined);
        },
        preview: frozen
          ? buildDeletionPreview({
              locale,
              title: frozen.target,
              positions: frozen.libraryTrackIds.length,
              files: frozen.publications,
              recordings: frozen.removedRecordings,
              outputs: publicationOutputs(frozen.owned, frozen.paths),
              positionRows: frozen.owned.flatMap((release) =>
                release.tracks.map((track) => ({
                  id: track.id,
                  label: trackLabel(release, track),
                })),
              ),
              artistReleases: {
                title: m("deletionArtistReleases"),
                items: frozen.owned.map(({ id, title }) => ({
                  id,
                  label: title,
                })),
              },
              coartistWarnings: warnings.length
                ? {
                    title: m("deletionCoartistWarnings"),
                    namesLabel: m("deletionCoartistNames"),
                    items: warnings,
                  }
                : undefined,
            })
          : undefined,
      })}
    />
  );
}
