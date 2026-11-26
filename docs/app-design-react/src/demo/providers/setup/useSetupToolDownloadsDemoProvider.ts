import { type SetStateAction, useCallback, useEffect, useState } from "react";
import { useI18n } from "../../../app/i18n";
import { settingsMessages } from "../../../ui/features/settings/settingsFormMessages";
import type {
	SetupScreenCompletedOperationView,
	SetupScreenToolsStepView,
} from "../../../ui/features/setup/SetupScreenView";
import {
	activateOrCreateDemoInstallation,
	type DemoInstallPreflight,
	initialDemoInstallations,
	initialDemoUnmanagedToolFiles,
	installPreflightDemo,
	type ManagedToolPackage,
} from "../settings/ToolLifecycleDemo";
import type { DemoSettings } from "../settings/useSettingsScreenDemoProvider";

/** Demo scheduling: queued work stays visible before the host advances it. */
const startDelayMs = 400;
const settleDelayMs = 800;

type SetupToolDownloadStatus = "queued" | "running" | "succeeded" | "failed";

/** Host-owned tool activity consumed by the existing Setup tools-step contract. */
export type SetupToolDownloadActivity = Pick<
	SetupScreenToolsStepView,
	"operations" | "completed" | "operationError" | "loadingOperations"
> & {
	start: (preflight: DemoInstallPreflight) => void;
	/** Packages with a queued or running download; repeated starts are disabled. */
	busyKinds: ReadonlySet<ManagedToolPackage>;
};

type SetupToolDownload = {
	id: string;
	preflight: DemoInstallPreflight;
	status: SetupToolDownloadStatus;
};

const statusText = {
	queued: { en: "Queued", ru: "В очереди" },
	running: { en: "Running", ru: "Выполняется" },
	succeeded: { en: "Completed", ru: "Завершена" },
	failed: { en: "Failed", ru: "Ошибка" },
};

function toolName(kind: ManagedToolPackage): string {
	return kind === "ffmpeg" ? "FFmpeg" : "fpcalc";
}

function downloadText(
	download: SetupToolDownload,
	locale: "en" | "ru",
): string {
	const state = statusText[download.status][locale];
	const { kind, release } = download.preflight;
	return locale === "ru"
		? `Загрузка инструмента — ${toolName(kind)}, версия ${release}: ${state}`
		: `Tool download — ${toolName(kind)}, version ${release}: ${state}`;
}

function isPending(download: SetupToolDownload) {
	return download.status === "queued" || download.status === "running";
}

function applyDownload(current: DemoSettings, plan: DemoInstallPreflight) {
	if (current.toolsPath.trim() !== plan.toolsRoot) return undefined;
	const installations = current.installations ?? initialDemoInstallations;
	const unmanaged = current.unmanagedToolFiles ?? initialDemoUnmanagedToolFiles;
	const approved = installPreflightDemo(
		current.toolsPath,
		plan.kind,
		plan.release,
		installations,
		unmanaged,
	);
	if (approved.token !== plan.token) return undefined;
	return {
		...current,
		installations: activateOrCreateDemoInstallation(
			installations,
			plan.kind,
			plan.release,
		),
		setupVerified: { ...current.setupVerified, [plan.kind]: plan.release },
		setupFailures: { ...current.setupFailures, [plan.kind]: undefined },
		...(plan.conflicts.length > 0 && {
			unmanagedToolFiles: unmanaged.filter(
				(path) => !plan.conflicts.includes(path),
			),
		}),
	};
}

/**
 * In-memory managed-tool download lifecycle for the prototype Setup host. It
 * lives above the routes so queued work keeps settling after navigation. The
 * admitted preflight is frozen evidence: settlement only applies settings while
 * the current tools root still matches that evidence, and it never performs
 * network or filesystem work, emits byte progress, or persists to storage.
 */
export function useSetupToolDownloadsDemoProvider(
	initialSettings: DemoSettings,
) {
	const { locale } = useI18n();
	const [{ settings, downloads }, setState] = useState({
		settings: initialSettings,
		downloads: [] as SetupToolDownload[],
	});
	const setSettings = useCallback(
		(update: SetStateAction<DemoSettings>) =>
			setState((current) => ({
				...current,
				settings:
					typeof update === "function" ? update(current.settings) : update,
			})),
		[],
	);

	function start(preflight: DemoInstallPreflight) {
		const frozen = {
			...preflight,
			targets: [...preflight.targets],
			conflicts: [...preflight.conflicts],
		};
		setState((current) => {
			if (
				current.downloads.some(
					(item) => item.preflight.kind === frozen.kind && isPending(item),
				)
			)
				return current;
			return {
				...current,
				downloads: [
					...current.downloads,
					{
						id: `setup-download-${current.downloads.length + 1}`,
						preflight: frozen,
						status: "queued",
					},
				],
			};
		});
	}

	// Advance one admitted download at a time so an unrelated queued package
	// cannot restart the running timer, mirroring the demo operations host.
	const active =
		downloads.find((item) => item.status === "running") ??
		downloads.find((item) => item.status === "queued");
	const activeId = active?.id;
	const activeStatus = active?.status;

	useEffect(() => {
		if (!activeId || !activeStatus) return;
		const timer = setTimeout(
			() => {
				setState((current) => {
					const download = current.downloads.find(
						(item) => item.id === activeId,
					);
					if (download?.status !== activeStatus) return current;
					const applied =
						activeStatus === "running"
							? applyDownload(current.settings, download.preflight)
							: undefined;
					const status =
						activeStatus === "queued"
							? "running"
							: applied
								? "succeeded"
								: "failed";
					return {
						settings: applied ?? current.settings,
						downloads: current.downloads.map((item) =>
							item === download ? { ...item, status } : item,
						),
					};
				});
			},
			activeStatus === "queued" ? startDelayMs : settleDelayMs,
		);
		return () => clearTimeout(timer);
	}, [activeId, activeStatus]);

	const busyKinds = new Set<ManagedToolPackage>();
	for (const item of downloads) {
		if (isPending(item)) busyKinds.add(item.preflight.kind);
	}

	const operations = downloads
		.filter((item) => item.status !== "succeeded")
		.map((item) => ({
			id: item.id,
			source: {
				locale,
				status: {
					text: downloadText(item, locale),
					stage:
						item.status === "running"
							? locale === "ru"
								? "Загрузка файлов"
								: "Downloading files"
							: undefined,
				},
				failure:
					item.status === "failed"
						? settingsMessages[locale].staleInstall
						: undefined,
			},
		}));

	const completed: SetupScreenCompletedOperationView[] = downloads
		.filter((item) => item.status === "succeeded")
		.map((item) => ({
			key: item.id,
			text: `${downloadText(item, locale)}.`,
		}));

	const activity: SetupToolDownloadActivity = {
		operations,
		completed,
		operationError: undefined,
		loadingOperations: undefined,
		start,
		busyKinds,
	};
	return { settings, setSettings, activity };
}
