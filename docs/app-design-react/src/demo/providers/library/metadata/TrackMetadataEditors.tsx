import { TrackMetadataEditorsView } from "../../../../ui/features/library/metadata/TrackMetadataEditorsView";
import { MetadataEditor } from "./MetadataEditor";
import {
	type TrackMetadataEditorsProps,
	useTrackMetadataEditorsDemoProvider,
} from "./useTrackMetadataEditorsDemoProvider";

export function TrackMetadataEditors(props: TrackMetadataEditorsProps) {
	const { recording, position, selectedAudio, otherAudio, ...view } =
		useTrackMetadataEditorsDemoProvider(props);
	return (
		<TrackMetadataEditorsView
			{...view}
			recording={<MetadataEditor {...recording} />}
			position={<MetadataEditor {...position} />}
			selectedAudio={
				selectedAudio ? (
					<MetadataEditor key={selectedAudio.id} {...selectedAudio.editor} />
				) : undefined
			}
			otherAudio={otherAudio.map(({ id, editor }) => (
				<MetadataEditor key={id} {...editor} />
			))}
		/>
	);
}
