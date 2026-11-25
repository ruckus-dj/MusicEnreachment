import { TrackScreenView } from "../../../ui/features/library/TrackScreenView";
import {
	type TrackScreenProps,
	useTrackScreenDemoProvider,
} from "./useTrackScreenDemoProvider";

/**
 * Public track screen entry point. The demo provider owns selection state,
 * derived publication status and nested domain actors; the view owns the markup.
 */
export function TrackScreen(props: TrackScreenProps) {
	return <TrackScreenView {...useTrackScreenDemoProvider(props)} />;
}
