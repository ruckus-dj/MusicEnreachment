import { MetadataEditorView } from "../features/library/MetadataEditorView";
import {
	type MetadataEditorProps,
	useMetadataEditorProvider,
} from "./useMetadataEditorProvider";

export type { MetadataEditorProps } from "./useMetadataEditorProvider";

/**
 * Public Metadata editor entry point. The provider owns state, normalisation,
 * validation, save/cancel and focus; the view owns the complete card markup.
 * This facade preserves the existing imports and prop shape.
 */
export function MetadataEditor(props: MetadataEditorProps) {
	const view = useMetadataEditorProvider(props);
	return <MetadataEditorView {...view} />;
}
