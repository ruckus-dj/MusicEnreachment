import {
	LocalReleaseGenreCatalogBinder,
	type LocalReleaseGenreCatalogProviderProps,
} from "../../../ui/features/local-release/LocalReleaseGenreCatalogOutlet";

/** Stable demo data provider; binds the already-built demo catalog model. */
export function DemoReleaseGenreCatalogProvider({
	children,
	model,
	selection,
}: LocalReleaseGenreCatalogProviderProps) {
	if (!model) throw new Error("missing demo Local release genre catalog");
	return (
		<LocalReleaseGenreCatalogBinder catalog={model} selection={selection}>
			{children}
		</LocalReleaseGenreCatalogBinder>
	);
}
