import type { ReactNode } from "react";
import { MetadataEditorView } from "../../../../ui/features/library/MetadataEditorView";
import { LibraryPositionMetadataEditorDataBinder } from "../../../../ui/features/library/metadata/LibraryPositionMetadataEditorOutlet";
import {
	type MetadataEditorProps,
	useMetadataEditorProvider,
} from "./useMetadataEditorProvider";

export type { MetadataEditorProps } from "./useMetadataEditorProvider";

/**
 * Public Metadata editor entry point. The provider owns state, normalisation,
 * validation, save/cancel and focus; the view owns the complete card markup.
 * With children it publishes its typed view model to the portable outlet
 * instead of rendering the view directly.
 */
export function MetadataEditor({
	children,
	...props
}: MetadataEditorProps & { children?: ReactNode }) {
	const view = useMetadataEditorProvider(props);
	if (children)
		return (
			<LibraryPositionMetadataEditorDataBinder model={view}>
				{children}
			</LibraryPositionMetadataEditorDataBinder>
		);
	return <MetadataEditorView {...view} />;
}
