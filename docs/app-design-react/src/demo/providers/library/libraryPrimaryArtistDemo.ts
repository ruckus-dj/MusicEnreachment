import type { LibraryPrimaryArtistModel } from "../../../ui/features/library/LibraryPrimaryArtistControl";
import type { CatalogArtist } from "./catalogData";

/** Use only local identities from the exact object's ordered credits. */
export function demoPrimaryArtist(
  credits: readonly { artistId: string; creditName: string }[],
  artists: readonly CatalogArtist[],
  selectedId: string | null | undefined,
  onChange: (id: string | null) => void,
) {
  const options = new Map<string, { id: string; name: string }>();
  let creditCount = 0;
  for (const credit of credits) {
    const artist = artists.find(({ id }) => id === credit.artistId);
    if (!artist) continue;
    creditCount++;
    if (!options.has(artist.id)) {
      options.set(artist.id, {
        id: artist.id,
        name: credit.creditName || artist.name,
      });
    }
  }
  const effectiveId = selectedId
    ? options.get(selectedId)?.id
    : options.size === 1
      ? options.values().next().value?.id
      : undefined;
  const control: LibraryPrimaryArtistModel | undefined =
    creditCount > 1
      ? {
          options: [...options.values()],
          selectedId,
          onChange: (id) => {
            if (id === null || options.has(id)) onChange(id);
          },
        }
      : undefined;
  return { effectiveId, control };
}
