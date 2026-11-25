import type { ComponentProps, Key, ReactNode, Ref } from "react";
import { AppButton } from "../../components/AppButton";
import { GenreCatalogView } from "./GenreCatalogView";
import { SettingsConflictDialog } from "./SettingsConflictDialog";
import { SettingsDraftConflictNotice } from "./SettingsDraftConflictNotice";
import { SettingsFoldersSection } from "./SettingsFoldersSection";
import {
  type SettingsGeneralModel,
  SettingsGeneralSection,
} from "./SettingsGeneralSection";
import { SettingsLeaveDialog } from "./SettingsLeaveDialog";
import { SettingsManagedPackageCards } from "./SettingsManagedPackageCards";
import { SettingsOutputResetDialog } from "./SettingsOutputResetDialog";
import { SettingsPageFrame } from "./SettingsPageFrame";
import {
  SettingsProvidersSection,
  type SettingsProvidersSectionProps,
} from "./SettingsProvidersSection";
import {
  type SettingsTabKey,
  SettingsTabPanel,
  SettingsTabs,
} from "./SettingsTabs";
import {
  SettingsTokenOutlet,
  type SettingsTokenSource,
} from "./SettingsTokenOutlet";
import { SettingsToolInstallDialog } from "./SettingsToolInstallDialog";
import { SettingsToolsMoveEditor } from "./SettingsToolsMoveEditor";
import { SettingsToolsMoveReviewDialog } from "./SettingsToolsMoveReviewDialog";
import {
  SettingsToolsCatalogSummary,
  SettingsToolsRootHeader,
} from "./SettingsToolsOverview";

/** A render-ready status line; the provider owns ordering and content. */
export type SettingsNoticeView = {
  key: string;
  text: string;
};

export type SettingsConflictNoticeView = ComponentProps<
  typeof SettingsDraftConflictNotice
> & {
  key: string;
};

export type SettingsHealthView = {
  title: string;
  healthy: boolean;
  healthyText: string;
  unhealthyText: string;
  problems: readonly string[];
  unavailableText?: string;
};

/** Retryable load failure; the view focuses the alert when it appears. */
export type SettingsLoadErrorView = {
  message: string;
  retryLabel: string;
  retryDisabled?: boolean;
  alertRef?: Ref<HTMLParagraphElement>;
  onRetry: () => void;
};

/** Lower action feedback; the view focuses the alert when it appears. */
export type SettingsFeedbackView = {
  message: string;
  alertRef?: Ref<HTMLParagraphElement>;
};

/** State refresh shown once a settings snapshot exists. */
export type SettingsRefreshView = {
  label: string;
  disabled?: boolean;
  onPress: () => void;
};

export type SettingsToolsTabViewProps = {
  root: ComponentProps<typeof SettingsToolsRootHeader>;
  /** Pure token model or a mounted controller that owns the token draft. */
  token?: SettingsTokenSource;
  catalog: ComponentProps<typeof SettingsToolsCatalogSummary>;
  packages: ComponentProps<typeof SettingsManagedPackageCards>;
  move: ComponentProps<typeof SettingsToolsMoveEditor>;
  /** Narrow slot for production operation followers. */
  operations?: ReactNode;
};

export type SettingsScreenTabsViewProps = {
  selectedKey: SettingsTabKey;
  onSelectionChange: (key: Key) => void;
  /** Panels kept mounted while hidden; prototype defaults to folders and genres. */
  forceMount?: readonly SettingsTabKey[];
  general: SettingsGeneralModel;
  folders: ComponentProps<typeof SettingsFoldersSection>;
  tools: SettingsToolsTabViewProps;
  providers: SettingsProvidersSectionProps;
  /** Pure genre catalog used by a provider that owns the catalog model. */
  genres?: ComponentProps<typeof GenreCatalogView>;
  /** Narrow slot for the application's existing genre controller. */
  genresSlot?: ReactNode;
};

export type SettingsScreenReadyViewProps = {
  health?: SettingsHealthView;
  tabs: SettingsScreenTabsViewProps;
};

export type SettingsScreenDialogsViewProps = {
  leave?: ComponentProps<typeof SettingsLeaveDialog>;
  conflict?: ComponentProps<typeof SettingsConflictDialog>;
  outputReset?: ComponentProps<typeof SettingsOutputResetDialog>;
  install?: ComponentProps<typeof SettingsToolInstallDialog>;
  move?: ComponentProps<typeof SettingsToolsMoveReviewDialog>;
};

export type SettingsScreenViewProps = {
  locale: "en" | "ru";
  headingRef?: Ref<HTMLHeadingElement>;
  notices: readonly SettingsNoticeView[];
  loadError?: SettingsLoadErrorView;
  conflicts: readonly SettingsConflictNoticeView[];
  /** Ready screen body; omitted while the first snapshot is loading. */
  ready?: SettingsScreenReadyViewProps;
  feedback?: SettingsFeedbackView;
  notice?: string;
  refresh?: SettingsRefreshView;
  dialogs: SettingsScreenDialogsViewProps;
};

const healthPanelClass =
  "grid min-w-0 gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3";
const healthHeadingClass = "m-0 text-sm font-semibold";
const unhealthyTextClass =
  "m-0 rounded bg-[var(--error-soft)] p-2 text-[var(--error)]";

/**
 * Settings screen composition. Providers own lifecycle, validation, drafts,
 * preflights and transport; this view renders their typed models in the
 * established order. A mounted token controller enters through its typed
 * provider boundary and stays mounted with the Tools panel; the operation
 * follower and genre controllers still use narrow render slots.
 */
export function SettingsScreenView({
  locale,
  headingRef,
  notices,
  loadError,
  conflicts,
  ready,
  feedback,
  notice,
  refresh,
  dialogs,
}: SettingsScreenViewProps) {
  const forceMount = (tab: SettingsTabKey) =>
    ready?.tabs.forceMount?.includes(tab) ??
    (tab === "folders" || tab === "genres");
  return (
    <SettingsPageFrame locale={locale} headingRef={headingRef}>
      {notices.map((item) => (
        <p
          key={item.key}
          className="mt-3 border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-[10px] text-[var(--ink)]"
          role="status"
          aria-live="polite"
        >
          {item.text}
        </p>
      ))}
      {loadError && (
        <>
          <p ref={loadError.alertRef} tabIndex={-1} role="alert">
            {loadError.message}
          </p>
          <AppButton
            isDisabled={loadError.retryDisabled}
            onPress={loadError.onRetry}
          >
            {loadError.retryLabel}
          </AppButton>
        </>
      )}
      {conflicts.map(({ key, ...conflict }) => (
        <SettingsDraftConflictNotice key={key} {...conflict} />
      ))}
      {ready && (
        <>
          {ready.health && (
            <section
              aria-labelledby="health-title"
              className={healthPanelClass}
            >
              <h2 id="health-title" className={healthHeadingClass}>
                {ready.health.title}
              </h2>
              <p
                className={ready.health.healthy ? "m-0" : unhealthyTextClass}
                role={ready.health.healthy ? "status" : "alert"}
              >
                {ready.health.healthy
                  ? ready.health.healthyText
                  : ready.health.unhealthyText}
              </p>
              {!!ready.health.problems.length && (
                <ul className="m-0 grid list-disc gap-1 pl-5 wrap-anywhere">
                  {ready.health.problems.map((problem) => (
                    <li key={problem}>{problem}</li>
                  ))}
                </ul>
              )}
              {ready.health.unavailableText && (
                <p role="alert" className={unhealthyTextClass}>
                  {ready.health.unavailableText}
                </p>
              )}
            </section>
          )}
          <SettingsTabs
            locale={locale}
            selectedKey={ready.tabs.selectedKey}
            onSelectionChange={ready.tabs.onSelectionChange}
          >
            <SettingsTabPanel id="general" forceMount={forceMount("general")}>
              <SettingsGeneralSection model={ready.tabs.general} />
            </SettingsTabPanel>
            <SettingsTabPanel id="folders" forceMount={forceMount("folders")}>
              <SettingsFoldersSection {...ready.tabs.folders} />
            </SettingsTabPanel>
            <SettingsTabPanel
              id="tools"
              className="grid gap-4"
              forceMount={forceMount("tools")}
            >
              {ready.tabs.tools.operations}
              <SettingsToolsRootHeader {...ready.tabs.tools.root} />
              <section aria-labelledby="managed-title" className="grid gap-3">
                {ready.tabs.tools.token && (
                  <SettingsTokenOutlet source={ready.tabs.tools.token} />
                )}
                <SettingsToolsCatalogSummary {...ready.tabs.tools.catalog} />
                <SettingsManagedPackageCards {...ready.tabs.tools.packages} />
                <SettingsToolsMoveEditor {...ready.tabs.tools.move} />
              </section>
            </SettingsTabPanel>
            <SettingsTabPanel
              id="providers"
              forceMount={forceMount("providers")}
            >
              <SettingsProvidersSection {...ready.tabs.providers} />
            </SettingsTabPanel>
            <SettingsTabPanel id="genres" forceMount={forceMount("genres")}>
              {ready.tabs.genresSlot ??
                (ready.tabs.genres && (
                  <GenreCatalogView {...ready.tabs.genres} />
                ))}
            </SettingsTabPanel>
          </SettingsTabs>
        </>
      )}
      {feedback && (
        <p ref={feedback.alertRef} tabIndex={-1} role="alert">
          {feedback.message}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {refresh && (
        <AppButton isDisabled={refresh.disabled} onPress={refresh.onPress}>
          {refresh.label}
        </AppButton>
      )}
      {dialogs.leave && <SettingsLeaveDialog {...dialogs.leave} />}
      {dialogs.conflict && <SettingsConflictDialog {...dialogs.conflict} />}
      {dialogs.outputReset && (
        <SettingsOutputResetDialog {...dialogs.outputReset} />
      )}
      {dialogs.install && <SettingsToolInstallDialog {...dialogs.install} />}
      {dialogs.move && <SettingsToolsMoveReviewDialog {...dialogs.move} />}
    </SettingsPageFrame>
  );
}
