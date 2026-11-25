/**
 * Semantic focus requests for the settings screen.
 *
 * Providers name the control that should receive focus; this module owns the
 * host DOM lookup and the focus call, so provider effects stay declarative.
 */

const settingsFocusSelectors = {
  concurrency: "#settings-concurrency",
  matchingThreshold: "#settings-matching-threshold",
  musicBrainzMode: "#settings-musicBrainzMode",
  musicBrainzURL: "#settings-musicBrainzURL",
  musicBrainzDelay: "#settings-musicBrainzDelay",
  publicationTemplate: '[aria-describedby="publication-template-help"]',
} as const;

export type SettingsFocusField = keyof typeof settingsFocusSelectors;

export type SettingsFocusTarget = SettingsFocusField | HTMLElement | null;

export function resolveSettingsFocusTarget(
  target: SettingsFocusTarget,
): HTMLElement | null {
  if (!target) return null;
  if (typeof target !== "string") return target;
  return document.querySelector<HTMLElement>(settingsFocusSelectors[target]);
}

export function requestSettingsFocus(target: SettingsFocusTarget): boolean {
  const element = resolveSettingsFocusTarget(target);
  if (!element) return false;
  element.focus();
  return true;
}

export function settingsTargetTab(element: Element | null): string | undefined {
  return element?.closest<HTMLElement>("[data-settings-tab]")?.dataset
    .settingsTab;
}

export function settingsFocusedControl(
  exclude?: Element | null,
): HTMLElement | null {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement) || active === document.body) return null;
  if (exclude?.contains(active)) return null;
  return active;
}

export function settingsDialogIsOpen(): boolean {
  return document.querySelector("dialog[open]") !== null;
}

export function settingsNavigationSaveControl(
  dialog: Element | null,
): HTMLButtonElement | null {
  return (
    dialog?.querySelector<HTMLButtonElement>(
      "button[data-settings-navigation-save]",
    ) ?? null
  );
}
