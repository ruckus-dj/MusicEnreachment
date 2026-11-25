import type { ReactNode } from "react";
import { AppButton } from "../../../components/AppButton";
import type { Locale } from "../../../i18n/types";
import { artistMetadataCardMessages } from "../ArtistMetadataCard";

export type ArtistMetadataCardLoaderViewProps = {
  locale: Locale;
  card?: ReactNode;
  loading: boolean;
  /** Present when the read failed; an empty detail still renders the notice. */
  readError?: string;
  onRetry: () => void;
};

/** Shared load/error shell around the artist metadata card. */
export function ArtistMetadataCardLoaderView({
  locale,
  card,
  loading,
  readError,
  onRetry,
}: ArtistMetadataCardLoaderViewProps) {
  const text = artistMetadataCardMessages[locale];
  return (
    <div className="grid gap-2">
      {card}
      {loading && <p role="status">{text.loading}</p>}
      {readError !== undefined && (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert">
            {text.readError} {readError}
          </p>
          <AppButton onPress={onRetry}>{text.retry}</AppButton>
        </div>
      )}
    </div>
  );
}
