import { getGetProviderReleaseFrontArtworkUrl } from "../../api/generated/client";
import { isUuid } from "./libraryRoute";

/**
 * Same-origin URL of the cached front thumbnail for one exact MusicBrainz
 * release. The API answers 404 until the artwork is cached, so an `<img>` using
 * this URL must keep its local fallback.
 */
export function providerReleaseFrontArtworkURL(releaseMBID: string): string {
  return getGetProviderReleaseFrontArtworkUrl(releaseMBID.trim().toLowerCase());
}

/**
 * The exact edition's cached provider front for links that identify exactly one
 * MusicBrainz release. `null` means the row has no single exact edition, so the
 * caller must not substitute a release-group representative image here.
 */
export function exactMusicBrainzReleaseArtworkURL(
  links: readonly { provider_code: string; provider_key: string }[] | undefined,
): string | null {
  const mbids = new Set(
    (links ?? [])
      .filter((link) => link.provider_code === "musicbrainz")
      .map((link) => link.provider_key.trim().toLowerCase())
      .filter(isUuid),
  );
  if (mbids.size !== 1) return null;
  return providerReleaseFrontArtworkURL([...mbids][0]);
}
