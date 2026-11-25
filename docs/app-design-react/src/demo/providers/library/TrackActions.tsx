import { TrackActionsView } from "../../../ui/features/library/TrackActionsView";
import { DeletionRequest } from "./DeletionRequest";
import {
	type TrackActionsProps,
	useTrackActionsDemoProvider,
} from "./useTrackActionsDemoProvider";

export function TrackActions(props: TrackActionsProps) {
	const { deletion, ...view } = useTrackActionsDemoProvider(props);
	return (
		<TrackActionsView
			{...view}
			deletion={deletion ? <DeletionRequest {...deletion} /> : undefined}
		/>
	);
}
