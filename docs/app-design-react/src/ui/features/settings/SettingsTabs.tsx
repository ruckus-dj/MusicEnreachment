import type { Key, ReactNode } from "react";
import { Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import { type SettingsTabID, settingsTabIDs } from "./settingsRoute";

export const settingsTabKeys = settingsTabIDs;
export type SettingsTabKey = SettingsTabID;

export type SettingsTabsLocale = "en" | "ru";

type SettingsTabsCopy = { label: string } & Record<SettingsTabKey, string>;

const settingsTabsCopy: Record<SettingsTabsLocale, SettingsTabsCopy> = {
	en: {
		label: "Settings",
		general: "General",
		folders: "Folders",
		tools: "Tools",
		providers: "Providers",
		genres: "Genres",
	},
	ru: {
		label: "Настройки",
		general: "Общие",
		folders: "Папки",
		tools: "Инструменты",
		providers: "Провайдеры",
		genres: "Жанры",
	},
};

const tabsClassName = "react-aria-Tabs ui-tabs grid min-w-0 gap-5";
const tabListClassName =
	"react-aria-TabList ui-tab-list inline-flex w-fit min-w-0 flex-wrap items-center gap-1 rounded-lg bg-[var(--raised)] p-1";
const tabClassName =
	"react-aria-Tab ui-tab flex h-8 pointer-coarse:min-h-10 max-[599px]:min-h-9 cursor-pointer items-center rounded-md px-3 text-sm font-medium text-[var(--muted)] transition-colors hover:text-[var(--ink)] data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";
const tabPanelClassName =
	"react-aria-TabPanel ui-tab-panel min-w-0 focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 data-[inert]:hidden";

interface SettingsTabsProps {
	locale: SettingsTabsLocale;
	selectedKey: SettingsTabKey;
	onSelectionChange: (key: Key) => void;
	children: ReactNode;
}

export function SettingsTabs({
	locale,
	selectedKey,
	onSelectionChange,
	children,
}: SettingsTabsProps) {
	const copy = settingsTabsCopy[locale];
	return (
		<Tabs
			aria-label={copy.label}
			selectedKey={selectedKey}
			onSelectionChange={onSelectionChange}
			className={tabsClassName}
		>
			<TabList aria-label={copy.label} className={tabListClassName}>
				{settingsTabKeys.map((key) => (
					<Tab key={key} id={key} className={tabClassName}>
						{copy[key]}
					</Tab>
				))}
			</TabList>
			{children}
		</Tabs>
	);
}

interface SettingsTabPanelProps {
	id: SettingsTabKey;
	forceMount?: boolean;
	className?: string;
	children: ReactNode;
}

export function SettingsTabPanel({
	id,
	forceMount = false,
	className,
	children,
}: SettingsTabPanelProps) {
	return (
		<TabPanel
			id={id}
			shouldForceMount={forceMount}
			data-settings-tab={id}
			className={
				className ? `${tabPanelClassName} ${className}` : tabPanelClassName
			}
		>
			{children}
		</TabPanel>
	);
}
