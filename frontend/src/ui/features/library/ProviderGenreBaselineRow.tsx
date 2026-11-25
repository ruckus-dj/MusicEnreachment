export type ProviderGenreBaselineGenre = {
  id?: string;
  name: string;
};

export type ProviderGenreBaselineValue = {
  genres: readonly ProviderGenreBaselineGenre[] | null;
};

/**
 * Readonly provider genre baseline. It never reads the draft: undefined means
 * the channel is not present, null means the baseline is unavailable, an empty
 * list means the provider accepted no genres, and values keep their order.
 */
export function ProviderGenreBaselineRow({
  baseline,
  locale,
}: {
  baseline?: ProviderGenreBaselineValue | null;
  locale: "en" | "ru";
}) {
  if (baseline === undefined) return null;
  const copy = genreMessages[locale];
  return (
    <section
      aria-label={copy.providerGenres}
      data-provider-genre-baseline
      className="grid min-w-0 gap-1 border-t border-[var(--line)] pt-2 text-sm"
    >
      <h4 className="m-0 text-sm font-medium">{copy.providerGenres}</h4>
      {baseline === null || baseline.genres === null ? (
        <p className="m-0 text-[var(--muted)]">
          {copy.providerGenresUnavailable}
        </p>
      ) : baseline.genres.length ? (
        <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
          {baseline.genres.map((genre) => (
            <li
              key={genre.id ?? genre.name}
              className="rounded-full border border-[var(--line)] px-2 py-0.5"
            >
              {genre.name}
            </li>
          ))}
        </ul>
      ) : (
        <p className="m-0 text-[var(--muted)]">{copy.providerGenresEmpty}</p>
      )}
    </section>
  );
}

import { genreMessages } from "./genreMessages";
