import { type Key, useEffect, useRef, useState } from "react";
import { DirectoryPicker } from "../../components/DirectoryPicker";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";
import { formStyles as styles } from "../../components/styles/formStyles";
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
	TextField,
} from "../../components/ui";
import { useI18n } from "../../i18n";
import { settingsMessages } from "../../i18n/namespaces/settings";
import { overlapsAnyDemoPath } from "../../infrastructure/demoPathValidation";
import {
	isValidMusicBrainzConfiguration,
	musicBrainzConfigurationFingerprint,
} from "../../infrastructure/providerValidation";
import { setupToolExecutables } from "../setup/setupToolsLifecycle";
import { GenreCatalogDemoProvider } from "./GenreCatalogDemoProvider";
import {
	defaultPublicationTemplate,
	validDemoPublicationTemplate,
} from "./publicationTemplateDemo";
import { SettingsFoldersSection } from "./SettingsFoldersSection";
import { SettingsGeneralSection } from "./SettingsGeneralSection";
import {
	type ManagedPackageCardModel,
	type ManagedPackageInstallation,
	SettingsManagedPackageCards,
} from "./SettingsManagedPackageCards";
import {
	type OutputResetFeedback,
	SettingsOutputResetDialog,
} from "./SettingsOutputResetDialog";
import {
	type SettingsTabKey,
	SettingsTabPanel,
	SettingsTabs,
	settingsTabKeys,
} from "./SettingsTabs";
import {
	getSettingsDraftStorage,
	mergeSettingsDraft,
	readSettingsDraft,
	rebaseSettingsDraft,
	type SettingsTextDraft,
	settingsDraftConflicts,
	settingsDraftIsDirty,
	validConcurrency,
	writeSettingsDraft,
} from "./settingsDraft";
import {
	activateDemoInstallation,
	availableToolReleases,
	canDeleteDemoInstallation,
	createDemoInstallation,
	type DemoInstallation,
	initialDemoInstallations,
	isAbsoluteDemoServerPath,
	type ManagedToolPackage,
	movePreflightDemo,
} from "./ToolLifecycleDemo";

const readSettingsTab = (hash: string): SettingsTabKey => {
	const [path, query = ""] = hash.replace(/^#/, "").split("?");
	if (path !== "settings") return "general";
	const tab = new URLSearchParams(query).get("tab");
	return settingsTabKeys.find((item) => item === tab) ?? "general";
};

const isSettingsTabDestination = (hash: string) =>
	hash.replace(/^#/, "").split("?")[0] === "settings";

const inactiveDirectoryBrowse: BrowseDirectory = async (path) => ({
	currentPath: path || "/",
	parentPath: "",
	directories: [],
	offset: 0,
});

export interface DemoSettings {
	toolsPath: string;
	workDirectory: string;
	publicationTemplate?: string;
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
	publicationTemplate: defaultPublicationTemplate,
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
	browseDirectory?: BrowseDirectory;
	resolveDirectoryPath?: (path: string) => string;
};

function settingsTextBaseline(settings: DemoSettings): SettingsTextDraft {
	return {
		concurrency: settings.concurrency ?? "4",
		toolsPath: settings.toolsPath,
		toolsPathCandidate: "",
		publicationTemplate:
			settings.publicationTemplate ?? defaultPublicationTemplate,
		workDirectory: settings.workDirectory,
		outputDirectory: settings.currentOutput ?? "/srv/music",
		musicBrainzMode: settings.musicBrainzMode ?? "",
		musicBrainzURL: settings.musicBrainzURL ?? "",
		musicBrainzDelay: settings.musicBrainzDelay ?? "",
	};
}

type PickerTarget = "tools";
type FolderDraftField =
	| "publicationTemplate"
	| "workDirectory"
	| "outputDirectory";
type FolderReplacement = {
	field: FolderDraftField;
	saved: string;
	draft: string;
};
type NoticeKey =
	| "staleMove"
	| "invalidPath"
	| "conflicts"
	| "invalidProvider"
	| "providerSaved"
	| "invalidTemplate"
	| "staleOutput"
	| "busy"
	| "pathOverlap";

export function SettingsScreen({
	settings,
	onSettings,
	onOutputReset,
	workDirectoryBusy = false,
	sourceRoots = [],
	browseDirectory,
	resolveDirectoryPath,
}: SettingsScreenProps) {
	const { locale } = useI18n();
	const copy = settingsMessages[locale];
	const [picker, setPicker] = useState<PickerTarget | null>(null);
	const [notice, setNotice] = useState<NoticeKey | "" | "saved">("");
	const [confirming, setConfirming] = useState<{
		path: string;
		expected: string;
	} | null>(null);
	const [confirmingFromLeave, setConfirmingFromLeave] = useState(false);
	const [outputFeedback, setOutputFeedback] = useState<
		OutputResetFeedback | undefined
	>();
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
	const [draftState, setDraftState] = useState(() => {
		const baseline = settingsTextBaseline(settings);
		const restored = readSettingsDraft(getSettingsDraftStorage());
		return restored ?? { value: baseline, baseline };
	});
	const moveCandidate = draftState.value.toolsPathCandidate;
	const [folderReplacement, setFolderReplacement] =
		useState<FolderReplacement>();
	const [conflictAcknowledged, setConflictAcknowledged] = useState(false);
	const [replacementPrompt, setReplacementPrompt] = useState(false);
	const [leavePrompt, setLeavePrompt] = useState(false);
	const pendingHash = useRef<string | null>(null);
	const pendingReplacement = useRef<"concurrency" | "providers">("concurrency");
	const allowNavigation = useRef(false);
	const previousHash = useRef(
		typeof window === "undefined" ? "" : window.location.hash,
	);
	const [activeTab, setActiveTab] = useState<SettingsTabKey>(() =>
		typeof window === "undefined"
			? "general"
			: readSettingsTab(window.location.hash),
	);
	useEffect(() => {
		const syncTab = () => setActiveTab(readSettingsTab(window.location.hash));
		syncTab();
		window.addEventListener("hashchange", syncTab);
		return () => window.removeEventListener("hashchange", syncTab);
	}, []);
	const selectTab = (key: Key) => {
		const tab = settingsTabKeys.find((item) => item === key) ?? "general";
		setActiveTab(tab);
		const nextHash = `#settings?tab=${tab}`;
		if (window.location.hash !== nextHash) window.location.hash = nextHash;
	};
	const [fieldErrors, setFieldErrors] = useState<
		Partial<Record<keyof SettingsTextDraft, string>>
	>({});
	const [draftStorageWarning, setDraftStorageWarning] = useState(false);
	const previousDraftDirty = useRef(false);
	const currentDraftValues = settingsTextBaseline(settings);
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
	const fieldDirty = (key: keyof SettingsTextDraft) =>
		draftState.value[key] !== draftState.baseline[key];
	const providerDirty =
		fieldDirty("musicBrainzMode") ||
		fieldDirty("musicBrainzURL") ||
		fieldDirty("musicBrainzDelay");
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
		const current = settingsTextBaseline(settings);
		setDraftState((draft) => rebaseSettingsDraft(draft, current));
	}, [settings]);
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
			if (isSettingsTabDestination(nextHash)) {
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
		const baseline = settingsTextBaseline(settings);
		setDraftState({ value: baseline, baseline });
		setFieldErrors({});
		setConflictAcknowledged(false);
	};
	const validateDraft = (value = draftState.value) => {
		const errors: Partial<Record<keyof SettingsTextDraft, string>> = {};
		if (providerDirty && !value.musicBrainzMode)
			errors.musicBrainzMode = copy.invalidProvider;
		if (!validConcurrency(value.concurrency)) errors.concurrency = "invalid";
		if (
			providerDirty &&
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
			providerDirty &&
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
	const commitDraft = () => {
		if (!draftDirty) return true;
		if (draftConflicts.length) return false;
		setNotice("");
		const merged = mergeSettingsDraft(draftState, currentDraftValues);
		if (!validateDraft(merged)) return false;
		const {
			concurrency,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
			publicationTemplate,
			workDirectory,
		} = merged;
		if (
			(fieldDirty("publicationTemplate") &&
				!validateTemplate(publicationTemplate)) ||
			(fieldDirty("workDirectory") && !validateWorkDirectory(workDirectory))
		)
			return false;
		const configuration = {
			mode: musicBrainzMode,
			url: musicBrainzURL,
			throttle: settings.musicBrainzThrottle,
			delay: musicBrainzDelay,
		};
		if (providerDirty && !isValidMusicBrainzConfiguration(configuration)) {
			const field = musicBrainzMode ? "musicBrainzURL" : "musicBrainzMode";
			setFieldErrors((current) => ({
				...current,
				[field]: copy.invalidProvider,
			}));
			document.getElementById(`settings-${field}`)?.focus();
			return false;
		}
		const baseline = {
			...draftState.baseline,
			concurrency,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
			publicationTemplate,
			workDirectory,
		};
		const value = {
			...draftState.value,
			concurrency,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
			publicationTemplate,
			workDirectory,
		};
		onSettings({
			...settings,
			concurrency,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
			publicationTemplate,
			workDirectory,
			...(providerDirty && {
				musicBrainzVerified: true,
				musicBrainzVerifiedFingerprint:
					musicBrainzConfigurationFingerprint(configuration),
			}),
			runtimeSaved: true,
		});
		setDraftState({ value, baseline });
		// Navigation can unmount this screen before the draft effect runs.
		setDraftStorageWarning(
			!writeSettingsDraft(
				getSettingsDraftStorage(),
				settingsDraftIsDirty(value, baseline) ? { value, baseline } : null,
			),
		);
		setFieldErrors({});
		setConflictAcknowledged(false);
		setReplacementPrompt(false);
		setNotice("saved");
		return true;
	};
	const commitConcurrency = () => {
		if (!validConcurrency(draftState.value.concurrency)) {
			setFieldErrors((current) => ({
				...current,
				concurrency: "invalid",
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
		setConflictAcknowledged(false);
		setNotice("saved");
	};
	const confirmFolderConflict = (
		field: FolderDraftField,
		approval?: FolderReplacement,
	) => {
		const saved = currentDraftValues[field];
		const draft = draftState.value[field];
		if (approval) {
			if (
				approval.field === field &&
				approval.saved === saved &&
				approval.draft === draft
			)
				return false;
			setNotice("staleOutput");
			return true;
		}
		if (!draftConflicts.includes(field)) return false;
		if (conflictAcknowledged) {
			setFolderReplacement({ field, saved, draft });
		}
		return true;
	};
	const validateTemplate = (template: string) => {
		if (validDemoPublicationTemplate(template)) return true;
		setNotice("invalidTemplate");
		return false;
	};
	const commitTemplate = (approval?: FolderReplacement) => {
		const merged = mergeSettingsDraft(draftState, currentDraftValues, [
			"publicationTemplate",
		]);
		const publicationTemplate = merged.publicationTemplate;
		if (!validateTemplate(publicationTemplate)) return;
		if (confirmFolderConflict("publicationTemplate", approval)) return;
		onSettings({ ...settings, publicationTemplate });
		setDraftState((current) => ({
			baseline: { ...current.baseline, publicationTemplate },
			value: { ...current.value, publicationTemplate },
		}));
		setConflictAcknowledged(false);
		setNotice("saved");
	};
	const validateWorkDirectory = (workDirectory: string) => {
		if (workDirectory === settings.workDirectory) return true;
		if (workDirectoryBusy) {
			setNotice("busy");
			return false;
		}
		if (workDirectory && !isAbsoluteDemoServerPath(workDirectory)) {
			setNotice("invalidPath");
			return false;
		}
		if (
			workDirectory &&
			overlapsAnyDemoPath(workDirectory, [
				settings.toolsPath,
				currentOutput,
				...sourceRoots,
			])
		) {
			setNotice("pathOverlap");
			return false;
		}
		return true;
	};
	const commitWorkDirectory = (approval?: FolderReplacement) => {
		const merged = mergeSettingsDraft(draftState, currentDraftValues, [
			"workDirectory",
		]);
		const workDirectory = merged.workDirectory.trim();
		if (!validateWorkDirectory(workDirectory)) return;
		if (confirmFolderConflict("workDirectory", approval)) return;
		onSettings({ ...settings, workDirectory });
		setDraftState((current) => ({
			baseline: { ...current.baseline, workDirectory },
			value: { ...current.value, workDirectory },
		}));
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
		if (conflicts.length && !forceReplace) {
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
	const candidate = draftState.value.outputDirectory;
	const outputDirty = candidate !== draftState.baseline.outputDirectory;
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
	const managedPackages: ManagedPackageCardModel[] = (
		["ffmpeg", "fpcalc"] as const
	).map((kind): ManagedPackageCardModel => {
		const releases = catalog[kind] ?? [];
		return {
			kind,
			installations: installations
				.filter((item) => item.package === kind)
				.map(
					(item): ManagedPackageInstallation => ({
						id: item.id,
						release: { identity: item.release },
						active: item.status === "active",
						lifeState: "ready",
						occupied: item.occupied,
						activate:
							item.status === "installed"
								? {
										disabled: platformMismatch || item.occupied,
										onPress: () =>
											update({
												installations: activateDemoInstallation(
													installations,
													item.id,
												),
											}),
									}
								: undefined,
						remove: canDeleteDemoInstallation(item)
							? {
									disabled: platformMismatch,
									onPress: () => {
										if (!canDeleteDemoInstallation(item)) return;
										update({
											installations: installations.filter(
												(candidate) => candidate.id !== item.id,
											),
										});
									},
								}
							: undefined,
					}),
				),
			catalog:
				releases.length > 0
					? {
							state: "loaded",
							releases: releases.map((identity) => ({ identity })),
						}
					: { state: "empty" },
			selectedRelease: selectedRelease[kind],
			onReleaseChange: (identity) =>
				update({
					selectedRelease: {
						...selectedRelease,
						[kind]: identity || undefined,
					},
				}),
			downloadAndVerify: {
				disabled: platformMismatch || !selectedRelease[kind],
				onPress: () => {
					const release = selectedRelease[kind];
					if (!release) return;
					setInstallPlan({
						kind,
						release,
						root: settings.toolsPath,
						token: [kind, release, settings.toolsPath].join("|"),
					});
				},
			},
		};
	});
	const saveProviders = () => {
		commitProviders();
	};
	const leaveWithDraft = (discard: boolean) => {
		if (discard) {
			setDraftStorageWarning(
				!writeSettingsDraft(getSettingsDraftStorage(), null),
			);
			const baseline = settingsTextBaseline(settings);
			setDraftState({ value: baseline, baseline });
		}
		const target = pendingHash.current;
		setLeavePrompt(false);
		if (target) {
			allowNavigation.current = true;
			window.location.hash = target;
		}
	};

	const closeOutputReset = () => {
		setConfirming(null);
		setConfirmingFromLeave(false);
		setOutputFeedback(undefined);
	};
	const openOutputReset = (fromLeave = false, approval?: FolderReplacement) => {
		if (confirmFolderConflict("outputDirectory", approval)) return;
		setOutputFeedback(undefined);
		setConfirmingFromLeave(fromLeave);
		setConfirming({ path: candidate, expected: currentOutput });
	};
	const confirmOutputReset = () => {
		if (!confirming) return;
		if (blocked) {
			setOutputFeedback({ kind: "running" });
			return;
		}
		if (
			candidate !== confirming.path ||
			confirming.expected !== currentOutput
		) {
			setOutputFeedback({ kind: "error", detail: copy.staleOutput });
			return;
		}
		const { path } = confirming;
		if (
			overlapsAnyDemoPath(path, [
				settings.workDirectory,
				settings.toolsPath,
				...sourceRoots,
			])
		) {
			setOutputFeedback({ kind: "error", detail: copy.pathOverlap });
			return;
		}
		update({ currentOutput: path });
		setDraftState((current) => ({
			baseline: { ...current.baseline, outputDirectory: path },
			value: { ...current.value, outputDirectory: path },
		}));
		onOutputReset?.(path);
		closeOutputReset();
		setNotice("");
	};
	const outputResetDialog = confirming !== null && (
		<SettingsOutputResetDialog
			locale={locale}
			busy={false}
			feedback={outputFeedback}
			onConfirm={confirmOutputReset}
			onDismiss={closeOutputReset}
		/>
	);
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
			<SettingsTabs
				locale={locale}
				selectedKey={activeTab}
				onSelectionChange={selectTab}
			>
				<SettingsTabPanel id="general">
					<SettingsGeneralSection
						model={{
							locale,
							format: {
								value:
									settings.publicationFormat === "original" ? "source" : "mka",
								choices: ["mka", "source"],
								onChange: (publicationFormat) =>
									update({
										publicationFormat:
											publicationFormat === "source" ? "original" : "mka",
									}),
							},
							concurrency: {
								value: draftState.value.concurrency,
								invalid: Boolean(fieldErrors.concurrency),
								dirty:
									draftState.value.concurrency !==
									draftState.baseline.concurrency,
								saveDisabled:
									draftConflicts.includes("concurrency") &&
									!conflictAcknowledged,
								onChange: (concurrency) => updateDraft({ concurrency }),
								onSave: commitConcurrency,
							},
							sha256: {
								enabled: settings.sha256Enabled ?? true,
								onChange: (sha256Enabled) => update({ sha256Enabled }),
							},
							logLevel: {
								value: settings.logLevel ?? "info",
								onChange: (logLevel) =>
									update({
										logLevel: logLevel as NonNullable<DemoSettings["logLevel"]>,
									}),
							},
							cleanup: {
								enabled: settings.sourceCleanupEnabled ?? false,
								dirty: false,
								onChange: (sourceCleanupEnabled) =>
									update({ sourceCleanupEnabled }),
							},
						}}
					/>
				</SettingsTabPanel>
				<SettingsTabPanel id="folders" forceMount>
					<SettingsFoldersSection
						locale={locale}
						browse={browseDirectory ?? inactiveDirectoryBrowse}
						resolveInitialPath={resolveDirectoryPath}
						busy={false}
						output={{
							value: candidate,
							dirty: outputDirty,
							matchesSaved: candidate === currentOutput,
							onChange: (outputDirectory) => updateDraft({ outputDirectory }),
							onReset: () => openOutputReset(),
						}}
						template={{
							value: draftState.value.publicationTemplate,
							dirty:
								draftState.value.publicationTemplate !==
								draftState.baseline.publicationTemplate,
							onChange: (publicationTemplate) =>
								updateDraft({ publicationTemplate }),
							onSave: () => commitTemplate(),
						}}
						work={{
							value: draftState.value.workDirectory,
							dirty:
								draftState.value.workDirectory !==
								draftState.baseline.workDirectory,
							onChange: (workDirectory) => updateDraft({ workDirectory }),
							onSave: () => commitWorkDirectory(),
							locked: workDirectoryBusy,
						}}
					/>
				</SettingsTabPanel>
				<SettingsTabPanel id="tools" className={`${styles.panel} grid gap-4`}>
					<h2>{copy.tools}</h2>
					<SettingsManagedPackageCards
						locale={locale}
						packages={managedPackages}
					/>
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
				</SettingsTabPanel>
				<SettingsTabPanel
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
				</SettingsTabPanel>
				<SettingsTabPanel id="genres" forceMount>
					<GenreCatalogDemoProvider
						loaded={settings.genresLoaded}
						onRefresh={() => update({ genresLoaded: true })}
					/>
				</SettingsTabPanel>
			</SettingsTabs>
			{leavePrompt && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setLeavePrompt(false)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">{copy.leaveSettingsTitle}</Heading>
						<p>{copy.leaveSettingsText}</p>
						{(notice === "invalidTemplate" ||
							notice === "invalidPath" ||
							notice === "busy" ||
							notice === "pathOverlap") && <p role="alert">{copy[notice]}</p>}
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
									if (outputDirty) {
										openOutputReset(true);
										return;
									}
									if (commitDraft()) leaveWithDraft(false);
								}}
							>
								{copy.leaveSave}
							</Button>
						)}
						{confirmingFromLeave && outputResetDialog}
					</Dialog>
				</Modal>
			)}
			{(replacementPrompt || folderReplacement) && (
				<Modal
					isOpen
					onOpenChange={(open) => {
						if (open) return;
						setReplacementPrompt(false);
						setFolderReplacement(undefined);
					}}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">{copy.replaceSettingsTitle}</Heading>
						<p>{copy.replaceSettingsText}</p>
						{folderReplacement && (
							<p className="wrap-anywhere">
								<code>{folderReplacement.saved}</code>
								{" → "}
								<code>{folderReplacement.draft}</code>
							</p>
						)}
						<Button
							onPress={() => {
								setReplacementPrompt(false);
								setFolderReplacement(undefined);
								if (folderReplacement) {
									if (folderReplacement.field === "publicationTemplate")
										commitTemplate(folderReplacement);
									else if (folderReplacement.field === "workDirectory")
										commitWorkDirectory(folderReplacement);
									else openOutputReset(false, folderReplacement);
								} else if (pendingReplacement.current === "concurrency") {
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
									setConflictAcknowledged(false);
									setNotice("saved");
								} else commitProviders(true);
							}}
						>
							{copy.confirmReplace}
						</Button>
						<Button
							onPress={() => {
								setReplacementPrompt(false);
								setFolderReplacement(undefined);
							}}
						>
							{copy.cancel}
						</Button>
					</Dialog>
				</Modal>
			)}
			{picker === "tools" && (
				<DirectoryPicker
					key={picker}
					initialPath={settings.toolsPath}
					onClose={() => setPicker(null)}
					onChoose={(path) => {
						updateDraft({ toolsPathCandidate: path });
						setPicker(null);
						setNotice("");
					}}
				/>
			)}
			{!confirmingFromLeave && outputResetDialog}
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
