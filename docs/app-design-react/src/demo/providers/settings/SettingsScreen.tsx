import {
	type ApplicationRouteProviderProps,
	ApplicationSettingsBinder,
} from "../../../ui/app/ApplicationRouteOutlet";
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
 * Public Settings entry point. The demo provider owns state and the portable
 * route outlet owns presentation; this facade preserves existing type and
 * fixture imports.
 */
export function DemoSettingsBinder({
	children,
	...props
}: SettingsScreenProps & ApplicationRouteProviderProps) {
	const model = useSettingsScreenDemoProvider(props);
	return (
		<ApplicationSettingsBinder model={model}>
			{children}
		</ApplicationSettingsBinder>
	);
}
