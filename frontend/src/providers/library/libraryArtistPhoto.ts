import {
  getGetLibraryArtistPhotoQueryKey,
  getLibraryArtistPhoto,
} from "../../api/generated/client";
import type { LibraryArtistPhoto } from "../../ui/features/library/LibraryArtistCards";

export const libraryArtistPhotoQueryKey = getGetLibraryArtistPhotoQueryKey;

/**
 * Reads one optional artist photo through the generated client. Missing,
 * empty and failed responses resolve to `null` so the artist list always keeps
 * its monogram and never renders an error state.
 */
export async function fetchLibraryArtistPhoto(
  artistId: string,
  signal?: AbortSignal,
): Promise<LibraryArtistPhoto | null> {
  try {
    const response = await getLibraryArtistPhoto(artistId, { signal });
    if (response.status !== 200) return null;
    const photo = response.data.photo;
    if (!photo?.image_url) return null;
    return {
      src: photo.image_url,
      sourceUrl: photo.source_url || null,
      author: photo.author || null,
      license: photo.license || null,
      licenseUrl: photo.license_url || null,
    };
  } catch {
    return null;
  }
}
