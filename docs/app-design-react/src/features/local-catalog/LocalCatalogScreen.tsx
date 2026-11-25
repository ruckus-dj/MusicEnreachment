import type { Locale } from "../../i18n/types";
import {
	LocalCatalogArtistForm,
	LocalCatalogRecordingForm,
} from "./LocalCatalogRecordEditors";
import { LocalCatalogReleaseEditor } from "./LocalCatalogReleaseEditor";
import {
	type LocalCatalogEditorSlot,
	LocalCatalogScreenView,
} from "./LocalCatalogScreenView";
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
		return {
			kind: "releases",
			element: <LocalCatalogReleaseEditor locale={locale} {...editor.props} />,
		};
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
	return {
		kind: "artists",
		eyebrow: m("eyebrow"),
		title: editor.props.record ? m("editArtist") : m("createArtist"),
		backLabel: m("back"),
		onBack: editor.props.onBack,
		element: <LocalCatalogArtistForm locale={locale} {...editor.props} />,
	};
}

export function LocalCatalogScreen(props: LocalCatalogScreenProps) {
	const { editor, locale, ...view } = useLocalCatalogScreenDemoProvider(props);
	return (
		<LocalCatalogScreenView
			{...view}
			editor={editor ? editorSlot(editor, locale) : null}
		/>
	);
}
