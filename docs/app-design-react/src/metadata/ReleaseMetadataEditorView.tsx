import {
	MetadataEditorView,
	type MetadataEditorViewProps,
} from "../features/library/MetadataEditorView";

type ProviderFact = { label: string; value: string };

export type ReleaseMetadataEditorViewProps = {
	editor: MetadataEditorViewProps;
	providerLinks: {
		summary: string;
		provider: ProviderFact;
		id: ProviderFact;
	};
};

export function ReleaseMetadataEditorView({
	editor,
	providerLinks,
}: ReleaseMetadataEditorViewProps) {
	return (
		<div className="grid min-w-0 gap-3">
			<MetadataEditorView {...editor} />
			<details className="rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
				<summary className="cursor-pointer font-semibold">
					{providerLinks.summary}
				</summary>
				<dl className="mt-3 grid min-w-0 gap-2 text-sm sm:grid-cols-2">
					{[providerLinks.provider, providerLinks.id].map(
						({ label, value }) => (
							<div className="min-w-0" key={label}>
								<dt className="text-[var(--muted)]">{label}</dt>
								<dd className="m-0 break-all">{value}</dd>
							</div>
						),
					)}
				</dl>
			</details>
		</div>
	);
}
