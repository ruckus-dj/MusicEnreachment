import type { ComponentProps } from "react";
import type { Locale } from "../../i18n/types";
import {
  GenreMetadataEditorOutlet,
  type GenreMetadataEditorSource,
} from "./GenreMetadataEditorOutlet";
import {
  LibraryPrimaryArtistControl,
  type LibraryPrimaryArtistModel,
} from "./LibraryPrimaryArtistControl";
import { LibraryPositionLyricsView } from "./metadata/LibraryPositionLyricsView";
import {
  TrackMetadataEditorsOutlet,
  type TrackMetadataEditorsSource,
} from "./metadata/TrackMetadataEditorsOutlet";
import {
  ReleaseCreditListView,
  type ReleaseCreditView,
} from "./ReleaseCreditListView";

export type LibraryPositionLyricsModel = Omit<
  ComponentProps<typeof LibraryPositionLyricsView>,
  "locale"
>;

/**
 * Fixed position-editor stack. The host supplies typed presentation data and
 * named editor sources; this view owns their order and mounts nothing for an
 * inactive retained position, so an inactive editor cannot start reads.
 */
export type LibraryPositionEditorsViewProps = {
  locale: Locale;
  active?: boolean;
  positionNumber?: string;
  credits?: readonly ReleaseCreditView[];
  genre?: GenreMetadataEditorSource;
  metadata?: TrackMetadataEditorsSource;
  primaryArtist?: LibraryPrimaryArtistModel;
  lyrics?: LibraryPositionLyricsModel;
};

export function LibraryPositionEditorsView({
  locale,
  active = true,
  positionNumber,
  credits,
  genre,
  metadata,
  primaryArtist,
  lyrics,
}: LibraryPositionEditorsViewProps) {
  if (!active) return null;
  const genreEditor = genre ? (
    <GenreMetadataEditorOutlet source={genre} />
  ) : null;
  const primaryArtistEditor = primaryArtist ? (
    <LibraryPrimaryArtistControl
      locale={locale}
      positionNumber={positionNumber}
      model={primaryArtist}
    />
  ) : null;
  return (
    <div className="grid min-w-0 gap-3">
      {credits && <ReleaseCreditListView locale={locale} credits={credits} />}
      {metadata ? (
        <TrackMetadataEditorsOutlet
          source={metadata}
          locale={locale}
          recordingSupplement={genreEditor}
          positionSupplement={primaryArtistEditor}
        />
      ) : (
        <>
          {genreEditor}
          {primaryArtistEditor}
        </>
      )}
      {lyrics && <LibraryPositionLyricsView locale={locale} {...lyrics} />}
    </div>
  );
}
