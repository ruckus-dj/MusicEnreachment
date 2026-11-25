import { MetadataEditorView } from "../../../../ui/features/library/MetadataEditorView";
import { ReleaseMetadataEditorView } from "../../../../ui/features/library/metadata/ReleaseMetadataEditorView";
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
			editor={<MetadataEditorView {...editorView} />}
			providerLinks={providerLinks}
		/>
	);
}
