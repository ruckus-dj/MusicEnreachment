import {
	ApplicationLibraryCatalogBinder,
	type ApplicationRouteProviderProps,
} from "../../../ui/app/ApplicationRouteOutlet";
import type { LibraryCatalogPageViewProps } from "../../../ui/features/library/LibraryCatalogPageView";

/** Binds demo library catalog props to the portable route. */
export function DemoLibraryCatalogBinder({
	children,
	...model
}: LibraryCatalogPageViewProps & ApplicationRouteProviderProps) {
	return (
		<ApplicationLibraryCatalogBinder model={model}>
			{children}
		</ApplicationLibraryCatalogBinder>
	);
}
