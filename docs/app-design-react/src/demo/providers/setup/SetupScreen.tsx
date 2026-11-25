import { SetupScreenView } from "../../../ui/features/setup/SetupScreenView";
import {
	type SetupScreenProps,
	useSetupScreenDemoProvider,
} from "./useSetupScreenDemoProvider";

export type { SetupScreenProps } from "./useSetupScreenDemoProvider";

/**
 * Public Setup entry point. The demo provider owns state and the view owns the
 * markup; this facade preserves existing imports and prop shape.
 */
export function SetupScreen(props: SetupScreenProps) {
	return <SetupScreenView {...useSetupScreenDemoProvider(props)} />;
}
