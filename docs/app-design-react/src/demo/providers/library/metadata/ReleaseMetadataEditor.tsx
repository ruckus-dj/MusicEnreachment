import { MetadataEditorView } from "../../../../ui/features/library/MetadataEditorView";
import { ReleaseMetadataEditorView } from "../../../../ui/features/library/metadata/ReleaseMetadataEditorView";
import { useMetadataEditorProvider } from "./useMetadataEditorProvider";
import {
	type ReleaseMetadataEditorProps,
	useReleaseMetadataEditorDemoProvider,
} from "./useReleaseMetadataEditorDemoProvider";

export type ReleaseMetadataEditorDemoProps = ReleaseMetadataEditorProps & {
	/**
	 * Unmount the editor DOM and its portals while keeping the draft provider
	 * mounted. A retained release overview uses this behind an active position;
	 * draft state survives because both hooks still run on every render.
	 * @default true
	 */
	visible?: boolean;
};

export function ReleaseMetadataEditor({
	visible = true,
	...props
}: ReleaseMetadataEditorDemoProps) {
	const { editor, providerLinks } = useReleaseMetadataEditorDemoProvider(props);
	const editorView = useMetadataEditorProvider(editor);
	if (!visible) return null;
	return (
		<ReleaseMetadataEditorView
			editor={<MetadataEditorView {...editorView} />}
			providerLinks={providerLinks}
		/>
	);
}
