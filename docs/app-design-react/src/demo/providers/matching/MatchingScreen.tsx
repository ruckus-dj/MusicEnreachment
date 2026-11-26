import { IncomingWorkspaceView } from "../../../ui/features/incoming/IncomingWorkspaceView";
import {
  type MatchingScreenProps,
  useMatchingScreenDemoProvider,
} from "./useMatchingScreenDemoProvider";

export type { MatchingScreenProps };

export function MatchingScreen(props: MatchingScreenProps) {
  const model = useMatchingScreenDemoProvider(props);
  return <IncomingWorkspaceView matching={model} />;
}
