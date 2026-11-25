import type { Locale } from "../../i18n/types";

/** One credited artist, already resolved by the release-fact projection. */
export type ReleaseCreditView = {
  /** Stable duplicate key for this credit identity. */
  key: string;
  name: string;
  canonicalName?: string | null;
  href: string;
  joinPhrase?: string | null;
  role?: string | null;
};

const copy = {
  en: { noArtists: "No artists specified" },
  ru: { noArtists: "Исполнители не указаны" },
} as const;

/**
 * Release credit line. The host resolves identities, display and canonical
 * names and keeps duplicate credits distinct; this module owns the link list and
 * its empty fallback.
 */
export function ReleaseCreditListView({
  locale,
  credits,
}: {
  locale: Locale;
  credits: readonly ReleaseCreditView[];
}) {
  if (!credits.length) return <span>{copy[locale].noArtists}</span>;
  const occurrences = new Map<string, number>();
  return (
    <ul className="my-1 flex list-none flex-wrap gap-x-4 gap-y-2 p-0">
      {credits.map((credit) => {
        const occurrence = occurrences.get(credit.key) ?? 0;
        occurrences.set(credit.key, occurrence + 1);
        const canonicalName =
          credit.canonicalName && credit.canonicalName !== credit.name
            ? credit.canonicalName
            : null;
        return (
          <li key={`${credit.key}-${occurrence}`}>
            <a href={credit.href}>{credit.name}</a>
            {canonicalName ? (
              <>
                {" ("}
                <span className="text-[.85em] text-[color-mix(in_srgb,currentColor_68%,transparent)]">
                  {canonicalName}
                </span>
                {")"}
              </>
            ) : null}
            {credit.joinPhrase}
            {credit.role ? (
              <span className="text-[color-mix(in_srgb,currentColor_68%,transparent)]">
                {" "}
                · {credit.role}
              </span>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
