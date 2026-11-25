import type { ComponentProps, Key } from "react";
import { GitHubTokenFormView } from "../tools/GitHubTokenFormView";
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

export type SettingsToolsTabViewProps = {
	root: ComponentProps<typeof SettingsToolsRootHeader>;
	token: ComponentProps<typeof GitHubTokenFormView>;
	catalog: ComponentProps<typeof SettingsToolsCatalogSummary>;
	packages: ComponentProps<typeof SettingsManagedPackageCards>;
	move: ComponentProps<typeof SettingsToolsMoveEditor>;
};

export type SettingsScreenTabsViewProps = {
	selectedKey: SettingsTabKey;
	onSelectionChange: (key: Key) => void;
	general: SettingsGeneralModel;
	folders: ComponentProps<typeof SettingsFoldersSection>;
	tools: SettingsToolsTabViewProps;
	providers: SettingsProvidersSectionProps;
	genres: ComponentProps<typeof GenreCatalogView>;
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
	notices: readonly SettingsNoticeView[];
	conflicts: readonly SettingsConflictNoticeView[];
	tabs: SettingsScreenTabsViewProps;
	dialogs: SettingsScreenDialogsViewProps;
};

/**
 * Pure Settings screen composition. All lifecycle, validation, draft and
 * preflight decisions live in the demo provider; this view only renders the
 * supplied models in the established order and preserves the mounted panels.
 */
export function SettingsScreenView({
	locale,
	notices,
	conflicts,
	tabs,
	dialogs,
}: SettingsScreenViewProps) {
	return (
		<SettingsPageFrame locale={locale}>
			{notices.map((notice) => (
				<p
					key={notice.key}
					className="mt-3 border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-[10px] text-[var(--ink)]"
					role="status"
					aria-live="polite"
				>
					{notice.text}
				</p>
			))}
			{conflicts.map(({ key, ...conflict }) => (
				<SettingsDraftConflictNotice key={key} {...conflict} />
			))}
			<SettingsTabs
				locale={locale}
				selectedKey={tabs.selectedKey}
				onSelectionChange={tabs.onSelectionChange}
			>
				<SettingsTabPanel id="general">
					<SettingsGeneralSection model={tabs.general} />
				</SettingsTabPanel>
				<SettingsTabPanel id="folders" forceMount>
					<SettingsFoldersSection {...tabs.folders} />
				</SettingsTabPanel>
				<SettingsTabPanel id="tools" className="grid gap-4">
					<SettingsToolsRootHeader {...tabs.tools.root} />
					<section aria-labelledby="managed-title" className="grid gap-3">
						<GitHubTokenFormView {...tabs.tools.token} />
						<SettingsToolsCatalogSummary {...tabs.tools.catalog} />
						<SettingsManagedPackageCards {...tabs.tools.packages} />
						<SettingsToolsMoveEditor {...tabs.tools.move} />
					</section>
				</SettingsTabPanel>
				<SettingsTabPanel id="providers">
					<SettingsProvidersSection {...tabs.providers} />
				</SettingsTabPanel>
				<SettingsTabPanel id="genres" forceMount>
					<GenreCatalogView {...tabs.genres} />
				</SettingsTabPanel>
			</SettingsTabs>
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
