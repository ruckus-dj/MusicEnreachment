import { createThemePreferenceStore } from "../../ui/components/themePreference";
import { themeStorageKey } from "./themePreferenceConfig";

export const themePreferenceStore = createThemePreferenceStore(themeStorageKey);
