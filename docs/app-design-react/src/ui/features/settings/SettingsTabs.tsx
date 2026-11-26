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

const tabsClassName = "react-aria-Tabs ui-tabs grid min-w-0 gap-3";
const tabListClassName =
	"react-aria-TabList ui-tab-list flex min-w-0 flex-wrap items-center gap-1 border-b border-[var(--line)]";
const tabClassName =
	"react-aria-Tab ui-tab min-h-9 pointer-coarse:min-h-[40px] max-[599px]:min-h-[40px] cursor-pointer rounded-t border-0 border-b-2 border-b-transparent bg-transparent px-3 py-1.5 text-sm text-[var(--muted)] hover:bg-[var(--raised)] hover:text-[var(--ink)] data-[selected]:border-b-[var(--accent)] data-[selected]:font-semibold data-[selected]:text-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
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
