import {
	ApplicationOwnCatalogBinder,
	ApplicationRouteOutlet,
	type ApplicationRouteProviderProps,
} from "../../../ui/app/ApplicationRouteOutlet";
import type { LocalCatalogEditorSlot } from "../../../ui/features/local-catalog/LocalCatalogScreenView";
import {
	type LocalCatalogEditorBinding,
	type LocalCatalogScreenProps,
	useLocalCatalogScreenDemoProvider,
} from "./useLocalCatalogScreenDemoProvider";

export type { LocalCatalogScreenProps } from "./useLocalCatalogScreenDemoProvider";

/**
 * Facade composition. The provider owns state, lookups, commits and routing;
 * this module supplies the independent editor actors as narrow slots (without
 * keys, so their lifetime follows the same conditional branches) and the view
 * owns the screen markup.
 */
function editorSlot(editor: LocalCatalogEditorBinding): LocalCatalogEditorSlot {
	return editor;
}

/** Binds demo data to the portable Local catalog route. */
export function DemoLocalCatalogBinder({
	children,
	...props
}: LocalCatalogScreenProps & ApplicationRouteProviderProps) {
	const { editor, ...view } = useLocalCatalogScreenDemoProvider(props);
	return (
		<ApplicationOwnCatalogBinder
			model={{ ...view, editor: editor ? editorSlot(editor) : null }}
		>
			{children}
		</ApplicationOwnCatalogBinder>
	);
}

export function LocalCatalogScreen(props: LocalCatalogScreenProps) {
	return (
		<ApplicationRouteOutlet
			route="own-catalog"
			provider={DemoLocalCatalogBinder}
			providerProps={props}
		/>
	);
}
