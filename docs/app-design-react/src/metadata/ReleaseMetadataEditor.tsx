import { ReleaseMetadataEditorView } from "./ReleaseMetadataEditorView";
import { useMetadataEditorProvider } from "./useMetadataEditorProvider";
import {
	type ReleaseMetadataEditorProps,
	useReleaseMetadataEditorDemoProvider,
} from "./useReleaseMetadataEditorDemoProvider";

export function ReleaseMetadataEditor(props: ReleaseMetadataEditorProps) {
	const { editor, providerLinks } = useReleaseMetadataEditorDemoProvider(props);
	const editorView = useMetadataEditorProvider(editor);
	return (
		<ReleaseMetadataEditorView
			editor={editorView}
			providerLinks={providerLinks}
		/>
	);
}
