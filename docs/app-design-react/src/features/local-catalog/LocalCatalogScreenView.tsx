import type { ComponentProps, ReactElement } from "react";
import type { Locale } from "../../i18n/types";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import { OwnCatalogDetailsView } from "./OwnCatalogDetailsView";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";
import { OwnCatalogList, type OwnCatalogListProps } from "./OwnCatalogList";

/**
 * An independent editor actor, already composed by the facade. The view keeps
 * the shell markup and the create/edit heading for the shelled actors.
 */
export type LocalCatalogEditorSlot =
	| { kind: "releases"; element: ReactElement }
	| {
			kind: "recordings" | "artists";
			mode: "create" | "edit";
			onBack: () => void;
			element: ReactElement;
	  };

export type LocalCatalogScreenViewProps = {
	locale: Locale;
	list: OwnCatalogListProps<string>;
	/** Localized commit notice; an empty value leaves the status region hidden. */
	notice: string;
	details: ComponentProps<typeof OwnCatalogDetailsView> | null;
	editor: LocalCatalogEditorSlot | null;
};

/**
 * Copy-ready Local catalog composition. The provider owns state, lookups,
 * commits, routing, the details projection and the list model; independent
 * editor actors are supplied as narrow slots. This module owns only the markup.
 */
export function LocalCatalogScreenView({
	locale,
	list,
	notice,
	details,
	editor,
}: LocalCatalogScreenViewProps) {
	const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
	if (details) {
		return <OwnCatalogDetailsView {...details} />;
	}
	if (editor?.kind === "releases") {
		return editor.element;
	}
	if (editor?.kind === "recordings") {
		return (
			<OwnCatalogEditorShell
				eyebrow={m("eyebrow")}
				title={
					editor.mode === "edit" ? m("editRecording") : m("createRecording")
				}
				backLabel={m("back")}
				onBack={editor.onBack}
			>
				{editor.element}
			</OwnCatalogEditorShell>
		);
	}
	if (editor?.kind === "artists") {
		return (
			<OwnCatalogEditorShell
				eyebrow={m("eyebrow")}
				title={editor.mode === "edit" ? m("editArtist") : m("createArtist")}
				backLabel={m("back")}
				onBack={editor.onBack}
			>
				{editor.element}
			</OwnCatalogEditorShell>
		);
	}
	return (
		<section className="mx-auto max-w-none p-3">
			<OwnCatalogList {...list} />
			{notice ? (
				<p className="mt-3 text-sm text-[var(--ink)]" role="status">
					{notice}
				</p>
			) : null}
		</section>
	);
}
