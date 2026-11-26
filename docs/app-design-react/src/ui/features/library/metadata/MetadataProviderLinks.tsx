import type { Locale } from "../../../i18n/types";
import { metadataEditorMessages } from "./metadataEditorMessages";

export type MetadataProviderLink = { provider: string; id: string };

export function MetadataProviderLinks({
  locale,
  links,
}: {
  locale: Locale;
  links?: readonly MetadataProviderLink[];
}) {
  if (!links?.length) return null;
  const text = metadataEditorMessages[locale];
  return (
    <details className="min-w-0 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
      <summary className="cursor-pointer rounded font-semibold focus-visible:outline-2 focus-visible:outline-[var(--focus)]">
        {text.providerLinks}
        {links.length === 1 ? ` · ${links[0].provider}` : ""}
      </summary>
      {links.map(({ provider, id }) => (
        <dl
          key={`${provider}:${id}`}
          className="mt-3 grid min-w-0 gap-2 text-sm sm:grid-cols-2"
        >
          <div className="min-w-0">
            <dt className="text-[var(--muted)]">{text.provider}</dt>
            <dd className="m-0 break-all">{provider}</dd>
          </div>
          <div className="min-w-0">
            <dt className="text-[var(--muted)]">{text.providerId}</dt>
            <dd className="m-0 break-all">{id}</dd>
          </div>
        </dl>
      ))}
    </details>
  );
}
