import { SettingsScreenView } from "../../../ui/features/settings/SettingsScreenView";
import {
	type SettingsScreenProps,
	useSettingsScreenDemoProvider,
} from "./useSettingsScreenDemoProvider";

export type {
	DemoSettings,
	SettingsScreenProps,
} from "./useSettingsScreenDemoProvider";
export { initialDemoSettings } from "./useSettingsScreenDemoProvider";

/**
 * Public Settings entry point. The demo provider owns state and the view owns
 * the markup; this facade preserves existing imports and prop shape.
 */
export function SettingsScreen(props: SettingsScreenProps) {
	return <SettingsScreenView {...useSettingsScreenDemoProvider(props)} />;
}
