import type { ComponentProps } from "react";
import { GitHubTokenFormView } from "../tools/GitHubTokenFormView";
import {
  createSettingsPanelOutlet,
  type SettingsPanelSource,
} from "./SettingsPanelOutlet";

/** Pure token form model owned by a host controller. */
export type SettingsTokenModel = ComponentProps<typeof GitHubTokenFormView>;

/** Inputs a host controller needs to own the token draft. */
export type SettingsTokenProviderProps = {
  hasToken: boolean;
  onChanged?: () => void | Promise<void>;
};

/**
 * A pure token model or a mounted controller that owns the draft. A provider
 * mounts with the Tools panel, so its lifetime matches the rendered panel.
 */
export type SettingsTokenSource = SettingsPanelSource<
  SettingsTokenModel,
  SettingsTokenProviderProps
>;

const { DataBinder: SettingsTokenDataBinder, Outlet: SettingsTokenOutlet } =
  createSettingsPanelOutlet<SettingsTokenModel, SettingsTokenProviderProps>(
    "settings token",
    GitHubTokenFormView,
  );

export { SettingsTokenDataBinder, SettingsTokenOutlet };
