import {
  MetadataEditorView,
  type MetadataEditorViewProps,
} from "../MetadataEditorView";
import { metadataEditorMessages } from "./metadataEditorMessages";

type ProviderLink = { provider: string; id: string };

export type ReleaseMetadataEditorViewProps = {
  editor: MetadataEditorViewProps;
  providerLinks?: readonly ProviderLink[];
};

export function ReleaseMetadataEditorView({
  editor,
  providerLinks,
}: ReleaseMetadataEditorViewProps) {
  const text = metadataEditorMessages[editor.locale];
  return (
    <div className="grid min-w-0 gap-3">
      <MetadataEditorView {...editor} />
      {!!providerLinks?.length && (
        <details className="rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
          <summary className="cursor-pointer font-semibold">
            {text.providerLinks}
            {providerLinks.length === 1
              ? ` · ${providerLinks[0].provider}`
              : ""}
          </summary>
          {providerLinks.map(({ provider, id }) => (
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
      )}
    </div>
  );
}
