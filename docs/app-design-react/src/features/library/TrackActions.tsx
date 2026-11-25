import { DeletionRequest } from "../../components/DeletionRequest";
import { TrackActionsView } from "./TrackActionsView";
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
