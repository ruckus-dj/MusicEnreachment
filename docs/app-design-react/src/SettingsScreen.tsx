import { useEffect, useRef, useState, type Key } from "react";
import {
	Button,
	Dialog,
	Heading,
	Input,
	Label,
	ListBox,
	ListBoxItem,
	Modal,
	Popover,
	Select,
	SelectValue,
	Tab,
	TabList,
	TabPanel,
	Tabs,
	TextField,
} from "./ui";
import { DirectoryPicker } from "./DirectoryPicker";
import { GenreLibrary } from "./GenreLibrary";
import { formStyles as styles } from "./formStyles";
import {
	activateDemoInstallation,
	availableToolReleases,
	createDemoInstallation,
	initialDemoInstallations,
	isAbsoluteDemoServerPath,
	movePreflightDemo,
	type DemoInstallation,
	type ManagedToolPackage,
} from "./ToolLifecycleDemo";
import { setupToolExecutables } from "./setupToolsLifecycle";
import { useI18n } from "./i18n";
import { settingsMessages } from "./i18n/namespaces/settings";
import {
	isValidMusicBrainzConfiguration,
	musicBrainzConfigurationFingerprint,
} from "./providerValidation";
import { overlapsAnyDemoPath } from "./demoPathValidation";
import {
	getSettingsDraftStorage,
	mergeSettingsDraft,
	readSettingsDraft,
	rebaseSettingsDraft,
	settingsDraftConflicts,
	settingsDraftIsDirty,
	validConcurrency,
	writeSettingsDraft,
	type SettingsTextDraft,
} from "./settingsDraft";

export interface DemoSettings {
	toolsPath: string;
	workDirectory: string;
	toolsChecked: boolean;
	toolsUpdated: boolean;
	pathChecked: boolean;
	genresLoaded: boolean;
	outputCandidate: string;
	outputDemoExecution: "idle" | "queued" | "running" | "claimed";
	currentOutput?: string;
	publicationFormat?: "" | "original" | "mka";
	musicBrainzMode?: "" | "public" | "self-hosted";
	musicBrainzURL?: string;
	musicBrainzThrottle?: boolean;
	musicBrainzDelay?: string;
	musicBrainzVerified?: boolean;
	musicBrainzVerifiedFingerprint?: string;
	lrclibEnabled?: boolean;
	sha256Enabled?: boolean;
	concurrency?: string;
	logLevel?: "" | "debug" | "info" | "warn" | "error";
	platformMismatch?: boolean;
	installations?: DemoInstallation[];
	selectedRelease?: Partial<Record<ManagedToolPackage, string>>;
	setupVerified?: Partial<Record<ManagedToolPackage, string>>;
	toolCatalog?: Record<ManagedToolPackage, string[]>;
	runtimeSaved?: boolean;
	sourceCleanupEnabled?: boolean;
	setupFailures?: Partial<Record<ManagedToolPackage, string>>;
	outputChecked?: boolean;
}

export const initialDemoSettings: DemoSettings = {
	toolsPath: "/var/lib/melotrove/tools",
	workDirectory: "",
	toolsChecked: false,
	toolsUpdated: false,
	pathChecked: false,
	genresLoaded: false,
	outputCandidate: "",
	outputDemoExecution: "idle",
	currentOutput: "/srv/music",
	publicationFormat: "mka",
	musicBrainzMode: "",
	musicBrainzURL: "",
	musicBrainzThrottle: false,
	musicBrainzDelay: "",
	musicBrainzVerified: false,
	lrclibEnabled: true,
	sha256Enabled: true,
	concurrency: "4",
	logLevel: "info",
	platformMismatch: false,
	installations: initialDemoInstallations,
	selectedRelease: {},
	toolCatalog: availableToolReleases,
	runtimeSaved: false,
	sourceCleanupEnabled: false,
};

export type SettingsScreenProps = {
	settings: DemoSettings;
	onSettings: (settings: DemoSettings) => void;
	onOutputReset?: (nextPath: string) => void;
	workDirectoryBusy?: boolean;
	sourceRoots?: string[];
};

type PickerTarget = "tools" | "work" | "output";
type NoticeKey =
	| "staleOutput"
	| "staleMove"
	| "invalidPath"
	| "conflicts"
	| "invalidProvider"
	| "providerSaved"
	| "pathOverlap";

export function SettingsScreen({
	settings,
	onSettings,
	onOutputReset,
	workDirectoryBusy = false,
	sourceRoots = [],
}: SettingsScreenProps) {
	const { locale } = useI18n();
	const copy = settingsMessages[locale];
	const [picker, setPicker] = useState<PickerTarget | null>(null);
	const [notice, setNotice] = useState<NoticeKey | "" | "saved">("");
	const [confirming, setConfirming] = useState<string | null>(null);
	const [movePlan, setMovePlan] = useState<ReturnType<
		typeof movePreflightDemo
	> | null>(null);
	const [moveConflictsAcknowledged, setMoveConflictsAcknowledged] =
		useState(false);
	const [installPlan, setInstallPlan] = useState<{
		kind: ManagedToolPackage;
		release: string;
		root: string;
		token: string;
	} | null>(null);
	const baselineFromSettings = (): SettingsTextDraft => ({
		concurrency: settings.concurrency ?? "4",
		toolsPath: settings.toolsPath,
		toolsPathCandidate: "",
		musicBrainzMode: settings.musicBrainzMode ?? "",
		musicBrainzURL: settings.musicBrainzURL ?? "",
		musicBrainzDelay: settings.musicBrainzDelay ?? "",
	});
	const [draftState, setDraftState] = useState(() => {
		const baseline = baselineFromSettings();
		const restored = readSettingsDraft(getSettingsDraftStorage());
		return restored ?? { value: baseline, baseline };
	});
	const moveCandidate = draftState.value.toolsPathCandidate;
	const [replacementConfirmed, setReplacementConfirmed] = useState(false);
	const [conflictAcknowledged, setConflictAcknowledged] = useState(false);
	const [replacementPrompt, setReplacementPrompt] = useState(false);
	const [leavePrompt, setLeavePrompt] = useState(false);
	const pendingHash = useRef<string | null>(null);
	const pendingReplacement = useRef<"all" | "concurrency" | "providers">("all");
	const allowNavigation = useRef(false);
	const previousHash = useRef(
		typeof window === "undefined" ? "" : window.location.hash,
	);
	const [fieldErrors, setFieldErrors] = useState<
		Partial<Record<keyof SettingsTextDraft, string>>
	>({});
	const [draftStorageWarning, setDraftStorageWarning] = useState(false);
	const previousDraftDirty = useRef(false);
	const currentDraftValues = baselineFromSettings();
	const draftConflicts = settingsDraftConflicts(
		draftState.baseline,
		currentDraftValues,
		draftState.value,
	);
	const draftDirty = settingsDraftIsDirty(
		draftState.value,
		draftState.baseline,
	);
	const updateDraft = (values: Partial<SettingsTextDraft>) =>
		setDraftState((current) => ({
			...current,
			value: { ...current.value, ...values },
		}));
	useEffect(() => {
		const persisted = writeSettingsDraft(
			getSettingsDraftStorage(),
			draftDirty ? draftState : null,
		);
		if (!persisted && (draftDirty || previousDraftDirty.current))
			setDraftStorageWarning(true);
		else if (persisted) setDraftStorageWarning(false);
		previousDraftDirty.current = draftDirty;
	}, [draftDirty, draftState]);
	useEffect(() => {
		const current = baselineFromSettings();
		setDraftState((draft) => rebaseSettingsDraft(draft, current));
	}, [
		settings.concurrency,
		settings.toolsPath,
		settings.musicBrainzMode,
		settings.musicBrainzURL,
		settings.musicBrainzDelay,
	]);
	useEffect(() => {
		const beforeUnload = (event: BeforeUnloadEvent) => {
			if (!draftDirty) return;
			event.preventDefault();
			event.returnValue = "";
		};
		const hashChanged = () => {
			const nextHash = window.location.hash;
			if (allowNavigation.current) {
				allowNavigation.current = false;
				previousHash.current = nextHash;
				return;
			}
			if (!draftDirty || nextHash === previousHash.current) {
				previousHash.current = nextHash;
				return;
			}
			pendingHash.current = nextHash;
			window.history.replaceState(
				window.history.state,
				"",
				`${window.location.pathname}${window.location.search}${previousHash.current}`,
			);
			setLeavePrompt(true);
		};
		const anchorClicked = (event: MouseEvent) => {
			if (
				!draftDirty ||
				event.defaultPrevented ||
				event.button !== 0 ||
				event.metaKey ||
				event.ctrlKey ||
				event.shiftKey ||
				event.altKey
			)
				return;
			const target = event.target;
			if (!(target instanceof Element)) return;
			const anchor = target.closest<HTMLAnchorElement>("a[href]");
			if (!anchor || anchor.target || anchor.hasAttribute("download")) return;
			const destination = new URL(anchor.href, window.location.href);
			if (
				destination.origin !== window.location.origin ||
				destination.pathname !== window.location.pathname ||
				destination.search !== window.location.search ||
				!destination.hash ||
				destination.hash === window.location.hash ||
				destination.hash === "#content"
			)
				return;
			event.preventDefault();
			pendingHash.current = destination.hash;
			setLeavePrompt(true);
		};
		window.addEventListener("beforeunload", beforeUnload);
		window.addEventListener("hashchange", hashChanged, true);
		window.addEventListener("popstate", hashChanged, true);
		document.addEventListener("click", anchorClicked, true);
		return () => {
			window.removeEventListener("beforeunload", beforeUnload);
			window.removeEventListener("hashchange", hashChanged, true);
			window.removeEventListener("popstate", hashChanged, true);
			document.removeEventListener("click", anchorClicked, true);
		};
	}, [draftDirty]);
	const takeSavedSettings = () => {
		const baseline = baselineFromSettings();
		setDraftState({ value: baseline, baseline });
		setFieldErrors({});
		setReplacementConfirmed(false);
		setConflictAcknowledged(false);
	};
	const validateDraft = (value = draftState.value) => {
		const errors: Partial<Record<keyof SettingsTextDraft, string>> = {};
		if (!value.musicBrainzMode) errors.musicBrainzMode = copy.invalidProvider;
		if (!validConcurrency(value.concurrency))
			errors.concurrency = copy.invalidConcurrency;
		if (
			value.musicBrainzMode === "self-hosted" &&
			!isValidMusicBrainzConfiguration({
				mode: value.musicBrainzMode,
				url: value.musicBrainzURL,
				throttle: settings.musicBrainzThrottle,
				delay: value.musicBrainzDelay,
			})
		)
			errors.musicBrainzURL = copy.invalidProvider;
		if (
			value.musicBrainzDelay.trim() !== "" &&
			!/^\d+(?:\.\d+)?$/.test(value.musicBrainzDelay.trim())
		)
			errors.musicBrainzDelay = copy.invalidDelay;
		setFieldErrors(errors);
		if (Object.keys(errors).length) {
			const first = errors.concurrency
				? "concurrency"
				: errors.musicBrainzMode
					? "musicBrainzMode"
					: errors.musicBrainzURL
						? "musicBrainzURL"
						: "musicBrainzDelay";
			document.getElementById(`settings-${first}`)?.focus();
		}
		return Object.keys(errors).length === 0;
	};
	const commitDraft = (forceReplace = false) => {
		const merged = mergeSettingsDraft(draftState, currentDraftValues);
		if (!validateDraft(merged)) return;
		if (
			draftConflicts.length &&
			conflictAcknowledged &&
			!replacementConfirmed &&
			!forceReplace
		) {
			pendingReplacement.current = "all";
			setReplacementPrompt(true);
			return;
		}
		if (draftConflicts.length && !conflictAcknowledged) return;
		const { concurrency, musicBrainzMode, musicBrainzURL, musicBrainzDelay } =
			merged;
		const configuration = {
			mode: musicBrainzMode,
			url: musicBrainzURL,
			throttle: settings.musicBrainzThrottle,
			delay: musicBrainzDelay,
		};
		if (!isValidMusicBrainzConfiguration(configuration)) {
			const field = musicBrainzMode ? "musicBrainzURL" : "musicBrainzMode";
			setFieldErrors((current) => ({
				...current,
				[field]: copy.invalidProvider,
			}));
			document.getElementById(`settings-${field}`)?.focus();
			return;
		}
		const baseline = {
			...draftState.baseline,
			concurrency,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
		};
		const value = {
			...draftState.value,
			concurrency,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
		};
		onSettings({
			...settings,
			concurrency,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
			musicBrainzVerified: true,
			musicBrainzVerifiedFingerprint:
				musicBrainzConfigurationFingerprint(configuration),
			runtimeSaved: true,
		});
		setDraftState({ value, baseline });
		setFieldErrors({});
		setReplacementConfirmed(false);
		setConflictAcknowledged(false);
		setReplacementPrompt(false);
		setNotice("saved");
	};
	const commitConcurrency = () => {
		if (!validConcurrency(draftState.value.concurrency)) {
			setFieldErrors((current) => ({
				...current,
				concurrency: copy.invalidConcurrency,
			}));
			document.getElementById("settings-concurrency")?.focus();
			return;
		}
		if (draftConflicts.includes("concurrency")) {
			if (!conflictAcknowledged) return;
			pendingReplacement.current = "concurrency";
			setReplacementPrompt(true);
			return;
		}
		const merged = mergeSettingsDraft(draftState, currentDraftValues, [
			"concurrency",
		]);
		const baseline = {
			...draftState.baseline,
			concurrency: merged.concurrency,
		};
		const value = { ...draftState.value, concurrency: merged.concurrency };
		onSettings({
			...settings,
			concurrency: merged.concurrency,
			runtimeSaved: true,
		});
		setDraftState({ baseline, value });
		setFieldErrors((current) => ({ ...current, concurrency: undefined }));
		setReplacementConfirmed(false);
		setConflictAcknowledged(false);
		setNotice("saved");
	};
	const commitProviders = (forceReplace = false) => {
		const merged = mergeSettingsDraft(draftState, currentDraftValues, [
			"musicBrainzMode",
			"musicBrainzURL",
			"musicBrainzDelay",
		]);
		const { musicBrainzMode, musicBrainzURL, musicBrainzDelay } = merged;
		const errors: Partial<Record<keyof SettingsTextDraft, string>> = {};
		if (!musicBrainzMode) errors.musicBrainzMode = copy.invalidProvider;
		if (
			musicBrainzMode === "self-hosted" &&
			!isValidMusicBrainzConfiguration({
				mode: musicBrainzMode,
				url: musicBrainzURL,
				throttle: settings.musicBrainzThrottle,
				delay: musicBrainzDelay,
			})
		)
			errors.musicBrainzURL = copy.invalidProvider;
		if (
			musicBrainzDelay.trim() !== "" &&
			!/^\d+(?:\.\d+)?$/.test(musicBrainzDelay.trim())
		)
			errors.musicBrainzDelay = copy.invalidDelay;
		setFieldErrors((current) => ({
			...current,
			musicBrainzMode: errors.musicBrainzMode,
			musicBrainzURL: errors.musicBrainzURL,
			musicBrainzDelay: errors.musicBrainzDelay,
		}));
		if (Object.keys(errors).length) {
			document
				.getElementById(
					errors.musicBrainzMode
						? "settings-musicBrainzMode"
						: errors.musicBrainzURL
							? "settings-musicBrainzURL"
							: "settings-musicBrainzDelay",
				)
				?.focus();
			return;
		}
		const fields = [
			"musicBrainzMode",
			"musicBrainzURL",
			"musicBrainzDelay",
		] as const;
		const conflicts = draftConflicts.filter((key) =>
			fields.includes(key as (typeof fields)[number]),
		);
		if (conflicts.length && !conflictAcknowledged) return;
		if (conflicts.length && !replacementConfirmed && !forceReplace) {
			pendingReplacement.current = "providers";
			setReplacementPrompt(true);
			return;
		}
		const configuration = {
			mode: musicBrainzMode,
			url: musicBrainzURL,
			throttle: settings.musicBrainzThrottle,
			delay: musicBrainzDelay,
		};
		if (!isValidMusicBrainzConfiguration(configuration)) {
			const field = musicBrainzMode ? "musicBrainzURL" : "musicBrainzMode";
			setFieldErrors((current) => ({
				...current,
				[field]: copy.invalidProvider,
			}));
			document.getElementById(`settings-${field}`)?.focus();
			return;
		}
		const baseline = {
			...draftState.baseline,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
		};
		const value = {
			...draftState.value,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
		};
		onSettings({
			...settings,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
			musicBrainzVerified: true,
			musicBrainzVerifiedFingerprint:
				musicBrainzConfigurationFingerprint(configuration),
		});
		setDraftState({ baseline, value });
		setReplacementConfirmed(false);
		setConflictAcknowledged(false);
		setNotice("providerSaved");
	};
	const selectMusicBrainzMode = (key: Key | null) => {
		const musicBrainzMode = key
			? (String(key) as SettingsTextDraft["musicBrainzMode"])
			: "";
		const musicBrainzURL = settings.musicBrainzURL ?? "";
		const musicBrainzDelay = settings.musicBrainzDelay ?? "";
		const configuration = {
			mode: musicBrainzMode,
			url: musicBrainzURL,
			throttle: settings.musicBrainzThrottle,
			delay: musicBrainzDelay,
		};
		const providerConflicts = draftConflicts.includes("musicBrainzMode");
		const delayValid =
			musicBrainzDelay.trim() === "" ||
			/^\d+(?:\.\d+)?$/.test(musicBrainzDelay.trim());
		if (
			isValidMusicBrainzConfiguration(configuration) &&
			delayValid &&
			!providerConflicts
		) {
			if (musicBrainzMode !== (settings.musicBrainzMode ?? "")) {
				onSettings({
					...settings,
					musicBrainzMode,
					musicBrainzVerified: true,
					musicBrainzVerifiedFingerprint:
						musicBrainzConfigurationFingerprint(configuration),
				});
			}
			setDraftState((current) => ({
				baseline: { ...current.baseline, musicBrainzMode },
				value: { ...current.value, musicBrainzMode },
			}));
			return;
		}
		updateDraft({ musicBrainzMode });
	};
	const update = (values: Partial<DemoSettings>) =>
		onSettings({ ...settings, ...values });
	const currentOutput = settings.currentOutput ?? "/srv/music";
	const platformMismatch = settings.platformMismatch ?? false;
	const installations = settings.installations ?? initialDemoInstallations;
	const selectedRelease = settings.selectedRelease ?? {};
	const catalog = settings.toolCatalog ?? availableToolReleases;
	const blocked = ["running", "claimed"].includes(settings.outputDemoExecution);
	const candidate = settings.outputCandidate;
	const samePath = !candidate || candidate === currentOutput;
	const managedCount = installations.reduce(
		(count, item) => count + (item.package === "ffmpeg" ? 2 : 1),
		0,
	);
	const musicBrainzFingerprint = musicBrainzConfigurationFingerprint({
		mode: draftState.value.musicBrainzMode,
		url: draftState.value.musicBrainzURL,
		throttle: settings.musicBrainzThrottle,
		delay: draftState.value.musicBrainzDelay,
	});
	const musicBrainzIsVerified =
		settings.musicBrainzVerified &&
		settings.musicBrainzVerifiedFingerprint === musicBrainzFingerprint;
	const saveProviders = () => {
		commitProviders();
	};
	const leaveWithDraft = (discard: boolean) => {
		if (discard) {
			setDraftStorageWarning(
				!writeSettingsDraft(getSettingsDraftStorage(), null),
			);
			const baseline = baselineFromSettings();
			setDraftState({ value: baseline, baseline });
		}
		const target = pendingHash.current;
		setLeavePrompt(false);
		if (target) {
			allowNavigation.current = true;
			window.location.hash = target;
		}
	};

	const confirmOutputReset = () => {
		if (blocked || settings.outputCandidate !== confirming) {
			setConfirming(null);
			setNotice("staleOutput");
			return;
		}
		if (!confirming) return;
		if (
			overlapsAnyDemoPath(confirming, [
				settings.workDirectory,
				settings.toolsPath,
				...sourceRoots,
			])
		) {
			setConfirming(null);
			setNotice("pathOverlap");
			return;
		}
		update({ currentOutput: confirming });
		onOutputReset?.(confirming);
		setConfirming(null);
		setNotice("");
	};
	const startInstall = () => {
		if (!installPlan || platformMismatch) return;
		const token = [
			installPlan.kind,
			selectedRelease[installPlan.kind],
			settings.toolsPath,
		].join("|");
		if (token !== installPlan.token) {
			setInstallPlan(null);
			setNotice("staleMove");
			return;
		}
		update({
			installations: createDemoInstallation(
				installations,
				installPlan.kind,
				installPlan.release,
			),
		});
		setInstallPlan(null);
		setNotice("");
	};
	const requestMove = () => {
		if (!isAbsoluteDemoServerPath(moveCandidate.trim())) {
			setNotice("invalidPath");
			return;
		}
		if (
			overlapsAnyDemoPath(moveCandidate, [
				settings.workDirectory,
				currentOutput,
				...sourceRoots,
			])
		) {
			setMovePlan(null);
			setNotice("pathOverlap");
			return;
		}
		setMovePlan(movePreflightDemo(moveCandidate, managedCount));
		setMoveConflictsAcknowledged(false);
	};
	const confirmMove = () => {
		if (!movePlan || !isAbsoluteDemoServerPath(moveCandidate.trim())) {
			setMovePlan(null);
			setNotice("invalidPath");
			return;
		}
		const freshPlan = movePreflightDemo(moveCandidate, managedCount);
		if (
			overlapsAnyDemoPath(freshPlan.path, [
				settings.workDirectory,
				currentOutput,
				...sourceRoots,
			])
		) {
			setMovePlan(null);
			setMoveConflictsAcknowledged(false);
			setNotice("pathOverlap");
			return;
		}
		if (
			platformMismatch ||
			freshPlan.token !== movePlan.token ||
			(movePlan.conflicts.length > 0 && !moveConflictsAcknowledged)
		) {
			setMovePlan(null);
			setMoveConflictsAcknowledged(false);
			setNotice(platformMismatch ? "conflicts" : "staleMove");
			return;
		}
		update({ toolsPath: movePlan.path, pathChecked: false, setupVerified: {} });
		setMovePlan(null);
		{
			const baseline = {
				...draftState.baseline,
				toolsPath: movePlan.path,
				toolsPathCandidate: "",
			};
			setDraftState({
				baseline,
				value: {
					...draftState.value,
					toolsPath: movePlan.path,
					toolsPathCandidate: "",
				},
			});
		}
		setNotice("");
	};
	const noticeText =
		notice === "saved"
			? copy.saved
			: notice === "providerSaved"
				? copy.providerSaved
				: notice === "invalidProvider"
					? copy.invalidProvider
					: notice === "pathOverlap"
						? copy.pathOverlap
						: notice
							? copy[notice]
							: "";

	return (
		<section className={styles.page} aria-labelledby="settings-title">
			<header
				className={`${styles.pageHeader} flex flex-wrap items-center justify-between gap-3`}
			>
				<div>
					<h1 className={styles.pageTitle} id="settings-title">
						{copy.title}
					</h1>
					<p className={styles.pageDescription}>{copy.subtitle}</p>
				</div>
			</header>
			{noticeText && (
				<p className={styles.formNotice} role="status" aria-live="polite">
					{noticeText}
				</p>
			)}
			{draftStorageWarning && (
				<p className={styles.formNotice} role="status" aria-live="polite">
					{copy.draftStorageWarning}
				</p>
			)}
			{draftConflicts.length > 0 && draftDirty && (
				<div
					className="grid gap-2 rounded border border-[var(--warning)] bg-[var(--warning-soft)] p-3"
					role="alert"
				>
					<p className="m-0">{copy.settingsConflict}</p>
					<div className="flex flex-wrap gap-2">
						<Button onPress={takeSavedSettings}>{copy.takeSaved}</Button>
						<Button onPress={() => setConflictAcknowledged(true)}>
							{copy.continueEditing}
						</Button>
					</div>
				</div>
			)}
			<Tabs className="grid min-w-0 gap-3" aria-label={copy.title}>
				<TabList
					aria-label={copy.title}
					className="flex flex-wrap gap-1 border-b border-[var(--line)]"
				>
					<Tab
						id="general"
						className="min-h-9 rounded-t px-3 text-sm text-[var(--muted)] selected:border-b-2 selected:border-[var(--accent)] selected:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
					>
						{copy.tabGeneral}
					</Tab>
					<Tab
						id="folders"
						className="min-h-9 rounded-t px-3 text-sm text-[var(--muted)] selected:border-b-2 selected:border-[var(--accent)] selected:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
					>
						{copy.tabFolders}
					</Tab>
					<Tab
						id="tools"
						className="min-h-9 rounded-t px-3 text-sm text-[var(--muted)] selected:border-b-2 selected:border-[var(--accent)] selected:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
					>
						{copy.tabTools}
					</Tab>
					<Tab
						id="providers"
						className="min-h-9 rounded-t px-3 text-sm text-[var(--muted)] selected:border-b-2 selected:border-[var(--accent)] selected:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
					>
						{copy.tabProviders}
					</Tab>
					<Tab
						id="genres"
						className="min-h-9 rounded-t px-3 text-sm text-[var(--muted)] selected:border-b-2 selected:border-[var(--accent)] selected:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
					>
						{copy.tabGenres}
					</Tab>
				</TabList>
				<TabPanel id="general" className={styles.panel}>
					<h2>{copy.general}</h2>
					<div className="grid gap-3 min-[1400px]:grid-cols-2 min-[1400px]:gap-x-6">
						<div className="grid content-start gap-3 min-[600px]:grid-cols-2">
							<Select
								selectedKey={settings.publicationFormat ?? ""}
								onSelectionChange={(key) =>
									update({
										publicationFormat: key
											? (String(key) as DemoSettings["publicationFormat"])
											: "",
									})
								}
							>
								<Label>{copy.publicationFormat}</Label>
								<Button>
									<SelectValue />
								</Button>
								<Popover>
									<ListBox>
										<ListBoxItem id="mka">{copy.mka}</ListBoxItem>
										<ListBoxItem id="original">{copy.original}</ListBoxItem>
									</ListBox>
								</Popover>
							</Select>
							<div className="grid content-start gap-2">
								<TextField
									value={draftState.value.concurrency}
									onChange={(concurrency) => updateDraft({ concurrency })}
								>
									<Label>{copy.concurrency}</Label>
									<Input
										id="settings-concurrency"
										type="number"
										min="1"
										aria-invalid={Boolean(fieldErrors.concurrency)}
										aria-describedby={
											fieldErrors.concurrency
												? "settings-concurrency-error"
												: undefined
										}
									/>
								</TextField>
								{fieldErrors.concurrency && (
									<p
										id="settings-concurrency-error"
										className="m-0 text-[var(--error)]"
										role="alert"
									>
										{fieldErrors.concurrency}
									</p>
								)}
								<Button
									className="justify-self-start"
									isDisabled={
										draftConflicts.includes("concurrency") &&
										!conflictAcknowledged
									}
									onPress={commitConcurrency}
								>
									{copy.save}
								</Button>
							</div>
							<label className="flex items-center gap-2">
								<input
									type="checkbox"
									checked={settings.sha256Enabled ?? true}
									onChange={(event) =>
										update({ sha256Enabled: event.target.checked })
									}
								/>
								{copy.sha}
							</label>
							<Select
								selectedKey={settings.logLevel ?? "info"}
								onSelectionChange={(key) =>
									update({
										logLevel: key
											? (String(key) as NonNullable<DemoSettings["logLevel"]>)
											: "",
									})
								}
							>
								<Label>{copy.log}</Label>
								<Button>
									<SelectValue />
								</Button>
								<Popover>
									<ListBox>
										{["debug", "info", "warn", "error"].map((level) => (
											<ListBoxItem id={level} key={level}>
												{level}
											</ListBoxItem>
										))}
									</ListBox>
								</Popover>
							</Select>
						</div>
						<section
							aria-labelledby="source-cleanup-title"
							className="grid content-start gap-2 border-t border-[var(--line)] pt-3 min-[1400px]:border-t-0 min-[1400px]:pt-0"
						>
							<h3
								id="source-cleanup-title"
								className="m-0 text-sm font-semibold"
							>
								{copy.cleanupTitle}
							</h3>
							<label className="flex items-center gap-2">
								<input
									type="checkbox"
									aria-describedby="source-cleanup-help"
									checked={settings.sourceCleanupEnabled ?? false}
									onChange={(event) =>
										update({ sourceCleanupEnabled: event.target.checked })
									}
								/>
								{copy.cleanup}
							</label>
							<p
								id="source-cleanup-help"
								className="m-0 text-xs text-[var(--warning)]"
							>
								{copy.cleanupHelp}
							</p>
						</section>
					</div>
				</TabPanel>
				<TabPanel
					id="folders"
					className={`${styles.panel} grid max-w-3xl gap-4`}
				>
					<h2>{copy.tabFolders}</h2>
					<section className="grid gap-2">
						<h3>{copy.output}</h3>
						<TextField isReadOnly value={currentOutput}>
							<Label>{copy.currentOutput}</Label>
							<Input />
						</TextField>
						<div className={styles.objectActions}>
							<TextField value={candidate} isReadOnly>
								<Label>{copy.newOutput}</Label>
								<Input />
							</TextField>
							<Button onPress={() => setPicker("output")}>
								{copy.chooseFolder}
							</Button>
						</div>
						<p>{copy.outputImpact}</p>
						<Button
							isDisabled={blocked || samePath}
							onPress={() => setConfirming(candidate)}
						>
							{copy.changeOutput}
						</Button>
						{blocked && <p role="alert">{copy.busy}</p>}
					</section>
					<section className="grid gap-2 border-t border-[var(--line)] pt-3">
						<h3>{copy.work}</h3>
						<div className={styles.objectActions}>
							<TextField value={settings.workDirectory} isReadOnly>
								<Label>{copy.work}</Label>
								<Input />
							</TextField>
							<Button
								isDisabled={workDirectoryBusy}
								onPress={() => setPicker("work")}
							>
								{copy.chooseFolder}
							</Button>
						</div>
						<small>{copy.workHelp}</small>
					</section>
				</TabPanel>
				<TabPanel id="tools" className={`${styles.panel} grid gap-4`}>
					<h2>{copy.tools}</h2>
					{(["ffmpeg", "fpcalc"] as const).map((kind) => {
						const active = installations.find(
							(item) => item.package === kind && item.status === "active",
						);
						const versions = installations.filter(
							(item) => item.package === kind,
						);
						return (
							<section
								className="grid gap-2 border-b border-[var(--line)] pb-3"
								key={kind}
								aria-label={kind === "ffmpeg" ? copy.ffmpeg : copy.fpcalc}
							>
								<div className="flex flex-wrap items-baseline justify-between gap-2">
									<h3 className="m-0">
										{kind === "ffmpeg" ? copy.ffmpeg : copy.fpcalc}
									</h3>
									<span className="text-xs text-[var(--muted)]">
										{copy.status}:{" "}
										{active
											? `${copy.active} · ${active.release}`
											: copy.available}
									</span>
								</div>
								{versions.map((item) => (
									<div
										key={item.id}
										className="flex flex-wrap items-center justify-between gap-2 text-sm"
									>
										<span>
											{item.release} ·{" "}
											{item.status === "active" ? copy.active : copy.installed}
											{item.occupied ? ` · ${copy.occupied}` : ""}
										</span>
										{item.status === "installed" && (
											<Button
												isDisabled={platformMismatch || item.occupied}
												onPress={() =>
													update({
														installations: activateDemoInstallation(
															installations,
															item.id,
														),
													})
												}
											>
												{copy.activate}
											</Button>
										)}
									</div>
								))}
								<div className="flex flex-wrap items-end gap-2">
									<Select
										className="min-w-48 flex-1"
										selectedKey={selectedRelease[kind] ?? null}
										onSelectionChange={(key) =>
											update({
												selectedRelease: {
													...selectedRelease,
													[kind]: key ? String(key) : undefined,
												},
											})
										}
									>
										<Label>{copy.chooseVersion}</Label>
										<Button>
											<SelectValue />
										</Button>
										<Popover>
											<ListBox>
												{(catalog[kind] ?? []).map((release) => (
													<ListBoxItem id={release} key={release}>
														{release}
													</ListBoxItem>
												))}
											</ListBox>
										</Popover>
									</Select>
									<Button
										isDisabled={platformMismatch || !selectedRelease[kind]}
										onPress={() => {
											const release = selectedRelease[kind];
											if (!release) return;
											setInstallPlan({
												kind,
												release,
												root: settings.toolsPath,
												token: [kind, release, settings.toolsPath].join("|"),
											});
										}}
									>
										{copy.install}
									</Button>
								</div>
							</section>
						);
					})}
					<section className="grid max-w-2xl gap-2 border-t border-[var(--line)] pt-3">
						<h3>{copy.moveRoot}</h3>
						<p>
							{copy.toolsRoot}:{" "}
							<code className="break-all">{settings.toolsPath}</code>
						</p>
						<div className={styles.objectActions}>
							<TextField
								value={moveCandidate}
								onChange={(value) => {
									updateDraft({ toolsPathCandidate: value });
									setMovePlan(null);
								}}
							>
								<Label>{copy.newRoot}</Label>
								<Input />
							</TextField>
							<Button onPress={() => setPicker("tools")}>
								{copy.chooseFolder}
							</Button>
							<Button isDisabled={platformMismatch} onPress={requestMove}>
								{copy.preflight}
							</Button>
						</div>
					</section>
				</TabPanel>
				<TabPanel
					id="providers"
					className={`${styles.panel} grid max-w-3xl gap-4`}
				>
					<h2>{copy.providers}</h2>
					<Select
						selectedKey={draftState.value.musicBrainzMode}
						onSelectionChange={selectMusicBrainzMode}
					>
						<Label>{copy.musicbrainz}</Label>
						<Button
							id="settings-musicBrainzMode"
							aria-invalid={Boolean(fieldErrors.musicBrainzMode)}
							aria-describedby={
								fieldErrors.musicBrainzMode
									? "settings-musicBrainzMode-error"
									: undefined
							}
						>
							<SelectValue />
						</Button>
						<Popover>
							<ListBox>
								<ListBoxItem id="public">{copy.publicService}</ListBoxItem>
								<ListBoxItem id="self-hosted">{copy.selfHosted}</ListBoxItem>
							</ListBox>
						</Popover>
					</Select>
					{fieldErrors.musicBrainzMode && (
						<p
							id="settings-musicBrainzMode-error"
							className="m-0 text-[var(--error)]"
							role="alert"
						>
							{fieldErrors.musicBrainzMode}
						</p>
					)}
					{draftState.value.musicBrainzMode === "self-hosted" && (
						<TextField
							value={draftState.value.musicBrainzURL}
							onChange={(musicBrainzURL) => updateDraft({ musicBrainzURL })}
						>
							<Label>{copy.baseUrl}</Label>
							<Input
								id="settings-musicBrainzURL"
								type="url"
								aria-invalid={Boolean(fieldErrors.musicBrainzURL)}
								aria-describedby={
									fieldErrors.musicBrainzURL
										? "settings-musicBrainzURL-error"
										: undefined
								}
							/>
						</TextField>
					)}
					{fieldErrors.musicBrainzURL && (
						<p
							id="settings-musicBrainzURL-error"
							className="m-0 text-[var(--error)]"
							role="alert"
						>
							{fieldErrors.musicBrainzURL}
						</p>
					)}
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							disabled={settings.musicBrainzMode !== "self-hosted"}
							checked={settings.musicBrainzThrottle ?? false}
							onChange={(event) =>
								update({ musicBrainzThrottle: event.target.checked })
							}
						/>
						{copy.throttle}
					</label>
					<TextField
						value={draftState.value.musicBrainzDelay}
						onChange={(musicBrainzDelay) => updateDraft({ musicBrainzDelay })}
					>
						<Label>{copy.delay}</Label>
						<Input
							id="settings-musicBrainzDelay"
							type="number"
							min="0"
							aria-invalid={Boolean(fieldErrors.musicBrainzDelay)}
							aria-describedby={
								fieldErrors.musicBrainzDelay
									? "settings-musicBrainzDelay-error"
									: undefined
							}
						/>
					</TextField>
					{fieldErrors.musicBrainzDelay && (
						<p
							id="settings-musicBrainzDelay-error"
							className="m-0 text-[var(--error)]"
							role="alert"
						>
							{fieldErrors.musicBrainzDelay}
						</p>
					)}
					<p>{musicBrainzIsVerified ? copy.verified : copy.notVerified}</p>
					<Button
						isDisabled={
							draftConflicts.some((field) =>
								[
									"musicBrainzMode",
									"musicBrainzURL",
									"musicBrainzDelay",
								].includes(field),
							) && !conflictAcknowledged
						}
						onPress={saveProviders}
					>
						{copy.saveProviders}
					</Button>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={settings.lrclibEnabled ?? true}
							onChange={(event) =>
								update({ lrclibEnabled: event.target.checked })
							}
						/>
						{copy.lrclib}
					</label>
					<small>{copy.lrclibHelp}</small>
				</TabPanel>
				<TabPanel id="genres" className={`${styles.panel} grid gap-2`}>
					<h2>{copy.tabGenres}</h2>
					<GenreLibrary
						loaded={settings.genresLoaded}
						onLoadedChange={(genresLoaded) => update({ genresLoaded })}
						buttonLabel={copy.refreshGenres}
					/>
				</TabPanel>
			</Tabs>
			{leavePrompt && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setLeavePrompt(false)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">{copy.leaveSettingsTitle}</Heading>
						<p>{copy.leaveSettingsText}</p>
						{draftStorageWarning && (
							<p role="status">{copy.draftStorageWarning}</p>
						)}
						<Button onPress={() => setLeavePrompt(false)}>{copy.stay}</Button>
						<Button onPress={() => leaveWithDraft(false)}>
							{copy.leaveKeepDraft}
						</Button>
						<Button onPress={() => leaveWithDraft(true)}>
							{copy.leaveDiscard}
						</Button>
						{!draftConflicts.length && (
							<Button
								onPress={() => {
									if (!validateDraft()) return;
									commitDraft();
									if (!replacementPrompt) leaveWithDraft(false);
								}}
							>
								{copy.leaveSave}
							</Button>
						)}
					</Dialog>
				</Modal>
			)}
			{replacementPrompt && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setReplacementPrompt(false)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">{copy.replaceSettingsTitle}</Heading>
						<p>{copy.replaceSettingsText}</p>
						<Button
							onPress={() => {
								setReplacementConfirmed(true);
								setReplacementPrompt(false);
								if (pendingReplacement.current === "concurrency") {
									const baseline = {
										...draftState.baseline,
										concurrency: draftState.value.concurrency,
									};
									setDraftState({ baseline, value: { ...draftState.value } });
									onSettings({
										...settings,
										concurrency: draftState.value.concurrency,
										runtimeSaved: true,
									});
									setReplacementConfirmed(false);
									setConflictAcknowledged(false);
									setNotice("saved");
								} else if (pendingReplacement.current === "providers")
									commitProviders(true);
								else commitDraft(true);
							}}
						>
							{copy.confirmReplace}
						</Button>
						<Button onPress={() => setReplacementPrompt(false)}>
							{copy.cancel}
						</Button>
					</Dialog>
				</Modal>
			)}
			{picker && (
				<DirectoryPicker
					key={picker}
					initialPath={
						picker === "tools"
							? settings.toolsPath
							: picker === "work"
								? settings.workDirectory || "/"
								: candidate || currentOutput
					}
					onClose={() => setPicker(null)}
					onChoose={(path) => {
						if (picker === "work") {
							if (
								overlapsAnyDemoPath(path, [
									settings.toolsPath,
									currentOutput,
									...sourceRoots,
								])
							) {
								setNotice("pathOverlap");
								setPicker(null);
								return;
							}
							update({ workDirectory: path });
						} else if (picker === "tools")
							updateDraft({ toolsPathCandidate: path });
						else update({ outputCandidate: path });
						setPicker(null);
						setNotice("");
					}}
				/>
			)}
			{confirming !== null && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setConfirming(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">{copy.changeOutput}?</Heading>
						<p className="break-all">
							{currentOutput} → {confirming}
						</p>
						<p>{copy.outputImpact}</p>
						<Button onPress={confirmOutputReset}>{copy.confirmOutput}</Button>
						<Button onPress={() => setConfirming(null)}>{copy.cancel}</Button>
					</Dialog>
				</Modal>
			)}
			{installPlan && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setInstallPlan(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">
							{copy.install} · {installPlan.kind}
						</Heading>
						<p>{installPlan.release}</p>
						<p>{setupToolExecutables(installPlan.kind).join(", ")}</p>
						<Button isDisabled={platformMismatch} onPress={startInstall}>
							{copy.install}
						</Button>
						<Button onPress={() => setInstallPlan(null)}>{copy.cancel}</Button>
					</Dialog>
				</Modal>
			)}
			{movePlan && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setMovePlan(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">{copy.moveRoot}</Heading>
						<p className="break-all">{movePlan.path}</p>
						<p>
							{movePlan.managedCount} ·{" "}
							{movePlan.conflicts.length ? copy.conflicts : ""}
						</p>
						{movePlan.conflicts.length > 0 && (
							<label className="flex items-center gap-2">
								<input
									type="checkbox"
									checked={moveConflictsAcknowledged}
									onChange={(event) =>
										setMoveConflictsAcknowledged(event.target.checked)
									}
								/>
								{copy.conflicts}
							</label>
						)}
						<Button
							isDisabled={
								platformMismatch ||
								(movePlan.conflicts.length > 0 && !moveConflictsAcknowledged)
							}
							onPress={confirmMove}
						>
							{copy.confirmMove}
						</Button>
						<Button onPress={() => setMovePlan(null)}>{copy.cancel}</Button>
					</Dialog>
				</Modal>
			)}
		</section>
	);
}
