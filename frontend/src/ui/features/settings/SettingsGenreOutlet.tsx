import type { ComponentProps } from "react";
import { GenreCatalogView } from "./GenreCatalogView";
import {
  createSettingsPanelOutlet,
  type SettingsPanelSource,
} from "./SettingsPanelOutlet";

/** Pure genre catalog model owned by a host controller. */
export type SettingsGenreModel = ComponentProps<typeof GenreCatalogView>;

/** The genre catalog controller takes no inputs beyond its mounted lifetime. */
type SettingsGenreProviderProps = Record<never, never>;

/**
 * A pure genre model or a mounted controller that owns the catalog state. A
 * provider mounts with the Genres panel, so its lifetime matches the panel.
 */
export type SettingsGenreSource = SettingsPanelSource<
  SettingsGenreModel,
  SettingsGenreProviderProps
>;

export const {
  DataBinder: SettingsGenreDataBinder,
  Outlet: SettingsGenreOutlet,
} = createSettingsPanelOutlet<SettingsGenreModel, SettingsGenreProviderProps>(
  "settings genre catalog",
  GenreCatalogView,
);
