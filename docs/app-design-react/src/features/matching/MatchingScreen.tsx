import { MatchingScreenView } from "./MatchingScreenView";
import {
	type MatchingScreenProps,
	useMatchingScreenDemoProvider,
} from "./useMatchingScreenDemoProvider";

export type { MatchingScreenProps };

export function MatchingScreen(props: MatchingScreenProps) {
	const model = useMatchingScreenDemoProvider(props);
	return <MatchingScreenView model={model} />;
}
