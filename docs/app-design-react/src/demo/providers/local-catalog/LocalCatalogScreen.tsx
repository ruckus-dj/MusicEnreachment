import {
	ApplicationOwnCatalogBinder,
	ApplicationRouteOutlet,
	type ApplicationRouteProviderProps,
} from "../../../ui/app/ApplicationRouteOutlet";
import type { LocalCatalogEditorSlot } from "../../../ui/features/local-catalog/LocalCatalogScreenView";
import type { Locale } from "../../../ui/i18n/types";
import { LocalCatalogRecordingForm } from "./LocalCatalogRecordEditors";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
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
function editorSlot(
	editor: LocalCatalogEditorBinding,
	locale: Locale,
): LocalCatalogEditorSlot {
	const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
	if (editor.kind === "releases") {
		return { kind: "releases", actor: editor.actor };
	}
	if (editor.kind === "recordings") {
		return {
			kind: "recordings",
			eyebrow: m("eyebrow"),
			title: editor.props.record ? m("editRecording") : m("createRecording"),
			backLabel: m("back"),
			onBack: editor.props.onBack,
			element: <LocalCatalogRecordingForm locale={locale} {...editor.props} />,
		};
	}
	return { kind: "artists", actor: editor.actor };
}

/** Binds demo data to the portable Local catalog route. */
export function DemoLocalCatalogBinder({
	children,
	...props
}: LocalCatalogScreenProps & ApplicationRouteProviderProps) {
	const { editor, locale, ...view } = useLocalCatalogScreenDemoProvider(props);
	return (
		<ApplicationOwnCatalogBinder
			model={{ ...view, editor: editor ? editorSlot(editor, locale) : null }}
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
