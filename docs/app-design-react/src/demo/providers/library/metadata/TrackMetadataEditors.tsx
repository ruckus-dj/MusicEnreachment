import type { ReactNode } from "react";
import {
	TrackMetadataEditorsDataBinder,
	type TrackMetadataEditorsModel,
} from "../../../../ui/features/library/metadata/TrackMetadataEditorsOutlet";
import { MetadataEditor } from "./MetadataEditor";
import {
	type TrackMetadataEditorsProps,
	useTrackMetadataEditorsDemoProvider,
} from "./useTrackMetadataEditorsDemoProvider";

export function TrackMetadataEditors({
	children,
	...props
}: TrackMetadataEditorsProps & { children: ReactNode }) {
	const { recording, position, ...view } =
		useTrackMetadataEditorsDemoProvider(props);
	const model: TrackMetadataEditorsModel = {
		...view,
		recording: {
			bind: (children: ReactNode) => (
				<MetadataEditor {...recording}>{children}</MetadataEditor>
			),
		},
		position: {
			bind: (children: ReactNode) => (
				<MetadataEditor {...position}>{children}</MetadataEditor>
			),
		},
	};
	return (
		<TrackMetadataEditorsDataBinder model={model}>
			{children}
		</TrackMetadataEditorsDataBinder>
	);
}
