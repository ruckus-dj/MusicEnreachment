import { type Key, useEffect, useRef, useState } from "react";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";
import { formStyles as styles } from "../../components/styles/formStyles";
import { useI18n } from "../../i18n";
import { settingsMessages } from "../../i18n/namespaces/settings";
import { overlapsAnyDemoPath } from "../../infrastructure/demoPathValidation";
import {
	isValidMusicBrainzConfiguration,
	musicBrainzConfigurationFingerprint,
} from "../../infrastructure/providerValidation";
import { GitHubTokenDemo } from "../tools/GitHubTokenDemo";
import { GenreCatalogDemoProvider } from "./GenreCatalogDemoProvider";
import {
	defaultPublicationTemplate,
	validDemoPublicationTemplate,
} from "./publicationTemplateDemo";
import { SettingsConflictDialog } from "./SettingsConflictDialog";
import { SettingsDraftConflictNotice } from "./SettingsDraftConflictNotice";
import { SettingsFoldersSection } from "./SettingsFoldersSection";
import { SettingsGeneralSection } from "./SettingsGeneralSection";
import { SettingsLeaveDialog } from "./SettingsLeaveDialog";
import {
	type ManagedPackageCardModel,
	type ManagedPackageInstallation,
	SettingsManagedPackageCards,
} from "./SettingsManagedPackageCards";
import {
	type OutputResetFeedback,
	SettingsOutputResetDialog,
} from "./SettingsOutputResetDialog";
import { SettingsPageFrame } from "./SettingsPageFrame";
import { SettingsProvidersSection } from "./SettingsProvidersSection";
import {
	type SettingsTabKey,
	SettingsTabPanel,
	SettingsTabs,
	settingsTabKeys,
} from "./SettingsTabs";
import { SettingsToolInstallDialog } from "./SettingsToolInstallDialog";
import { SettingsToolsMoveEditor } from "./SettingsToolsMoveEditor";
import { SettingsToolsMoveReviewDialog } from "./SettingsToolsMoveReviewDialog";
import {
	SettingsToolsCatalogSummary,
	SettingsToolsRootHeader,
} from "./SettingsToolsOverview";
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
	initialDemoUnmanagedToolFiles,
	installPreflightDemo,
	isAbsoluteDemoServerPath,
	type ManagedToolPackage,
	movePreflightDemo,
} from "./ToolLifecycleDemo";
import { useToolCatalogDemoProvider } from "./useToolCatalogDemoProvider";

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

const validProviderDelay = (value: string) => {
	const trimmed = value.trim();
	return (
		trimmed === "" || (/^\d+(?:\.\d+)?$/.test(trimmed) && Number(trimmed) <= 60)
	);
};

const matchingThresholdNumber = (value: string) => {
	const normalized = value.trim().replace(",", ".");
	if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(normalized)) return undefined;
	const numeric = Number(normalized);
	if (!Number.isFinite(numeric) || numeric < 0 || numeric > 1) return undefined;
	return numeric;
};

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
	publicationFormat?: "" | "original" | "mka" | "m4a";
	musicBrainzMode?: "" | "public" | "self-hosted";
	musicBrainzURL?: string;
	musicBrainzThrottle?: boolean;
	musicBrainzDelay?: string;
	musicBrainzVerified?: boolean;
	musicBrainzVerifiedFingerprint?: string;
	hasAcoustIDApplicationKey?: boolean;
	hasGitHubToken?: boolean;
	lrclibEnabled?: boolean;
	sha256Enabled?: boolean;
	concurrency?: string;
	matchingPairThreshold?: string;
	logLevel?: "" | "debug" | "info" | "warn" | "error";
	platformMismatch?: boolean;
	installations?: DemoInstallation[];
	unmanagedToolFiles?: readonly string[];
	selectedRelease?: Partial<Record<ManagedToolPackage, string>>;
	setupVerified?: Partial<Record<ManagedToolPackage, string>>;
	toolCatalog?: Record<ManagedToolPackage, string[]>;
	runtimeSaved?: boolean;
	sourceCleanupEnabled?: boolean;
	publicationAutoCompleteAlbums?: boolean;
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
	hasAcoustIDApplicationKey: false,
	lrclibEnabled: true,
	sha256Enabled: true,
	concurrency: "4",
	matchingPairThreshold: "0.70",
	logLevel: "info",
	platformMismatch: false,
	installations: initialDemoInstallations,
	unmanagedToolFiles: initialDemoUnmanagedToolFiles,
	selectedRelease: {},
	toolCatalog: availableToolReleases,
	runtimeSaved: false,
	sourceCleanupEnabled: false,
	publicationAutoCompleteAlbums: false,
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
		matchingPairThreshold: settings.matchingPairThreshold ?? "0.70",
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

type FolderDraftField =
	| "publicationTemplate"
	| "workDirectory"
	| "outputDirectory";
type FolderReplacement = {
	field: FolderDraftField;
	saved: string;
	draft: string;
};

type SettingsConflictGroup =
	| "concurrency"
	| "matchingThreshold"
	| "publicationTemplate"
	| "workDirectory"
	| "outputDirectory"
	| "toolsDirectory"
	| "musicBrainz";

const conflictGroups: readonly SettingsConflictGroup[] = [
	"concurrency",
	"matchingThreshold",
	"publicationTemplate",
	"workDirectory",
	"outputDirectory",
	"toolsDirectory",
	"musicBrainz",
];

const conflictGroupFields: Record<
	SettingsConflictGroup,
	readonly (keyof SettingsTextDraft)[]
> = {
	concurrency: ["concurrency"],
	matchingThreshold: ["matchingPairThreshold"],
	publicationTemplate: ["publicationTemplate"],
	workDirectory: ["workDirectory"],
	outputDirectory: ["outputDirectory"],
	toolsDirectory: ["toolsPath"],
	musicBrainz: ["musicBrainzMode", "musicBrainzURL", "musicBrainzDelay"],
};

const conflictGroupLabelKeys = {
	concurrency: "conflictConcurrency",
	matchingThreshold: "conflictMatchingThreshold",
	publicationTemplate: "conflictTemplate",
	workDirectory: "conflictWorkDirectory",
	outputDirectory: "conflictOutputDirectory",
	toolsDirectory: "conflictToolsDirectory",
	musicBrainz: "conflictMusicBrainz",
} as const satisfies Record<
	SettingsConflictGroup,
	keyof (typeof settingsMessages)["en"]
>;

const conflictGroupLabel = (
	group: SettingsConflictGroup,
	copy: (typeof settingsMessages)[keyof typeof settingsMessages],
) => copy[conflictGroupLabelKeys[group]];

const formatMessage = (template: string, values: Record<string, string>) =>
	template.replace(
		/\{([^{}]+)\}/g,
		(match, name: string) => values[name] ?? match,
	);

const displaySettingValue = (value: string) => `«${value}»`;

function conflictGroupValue(
	group: SettingsConflictGroup,
	values: SettingsTextDraft,
) {
	switch (group) {
		case "musicBrainz": {
			const mode = values.musicBrainzMode;
			if (mode !== "self-hosted") return displaySettingValue(mode || "—");
			return [
				displaySettingValue(mode),
				displaySettingValue(values.musicBrainzURL),
				displaySettingValue(values.musicBrainzDelay),
			].join(" ");
		}
		case "concurrency":
			return displaySettingValue(values.concurrency);
		case "matchingThreshold":
			return displaySettingValue(values.matchingPairThreshold);
		case "publicationTemplate":
			return displaySettingValue(values.publicationTemplate);
		case "workDirectory":
			return displaySettingValue(values.workDirectory);
		case "outputDirectory":
			return displaySettingValue(values.outputDirectory);
		case "toolsDirectory":
			return displaySettingValue(values.toolsPath);
	}
}

function conflictGroupValuesEqual(
	group: SettingsConflictGroup,
	left: SettingsTextDraft,
	right: SettingsTextDraft,
) {
	return (
		conflictGroupFields[group].every((field) => left[field] === right[field]) &&
		(group !== "toolsDirectory" ||
			left.toolsPathCandidate === right.toolsPathCandidate)
	);
}

function conflictGroupSignature(
	group: SettingsConflictGroup,
	values: SettingsTextDraft,
) {
	return JSON.stringify(
		conflictGroupFields[group].map((field) => values[field]),
	);
}

type ValueReplacement = {
	group: "concurrency" | "matchingThreshold" | "musicBrainz" | "toolsDirectory";
	saved: SettingsTextDraft;
	draft: SettingsTextDraft;
	details: { saved: string; draft: string };
	opener?: Element;
};

type NoticeKey =
	| "staleMove"
	| "staleInstall"
	| "invalidPath"
	| "conflicts"
	| "invalidProvider"
	| "providerSaved"
	| "invalidTemplate"
	| "staleOutput"
	| "staleSettings"
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
	const toolCatalog = useToolCatalogDemoProvider();
	const [acoustIDApplicationKey, setAcoustIDApplicationKey] = useState("");
	const [notice, setNotice] = useState<NoticeKey | "" | "saved">("");
	const [confirming, setConfirming] = useState<{
		path: string;
		expected: string;
	} | null>(null);
	const [outputFeedback, setOutputFeedback] = useState<
		OutputResetFeedback | undefined
	>();
	const [movePlan, setMovePlan] = useState<
		| (ReturnType<typeof movePreflightDemo> & {
				sourceDirectory: string;
				removeOld: boolean;
		  })
		| null
	>(null);
	const [removeOld, setRemoveOld] = useState(false);
	const moveOpener = useRef<HTMLElement | null>(null);
	const [installPlan, setInstallPlan] = useState<{
		preflight: ReturnType<typeof installPreflightDemo>;
		opener: HTMLElement | null;
	} | null>(null);
	const [draftState, setDraftState] = useState(() => {
		const baseline = settingsTextBaseline(settings);
		const restored = readSettingsDraft(getSettingsDraftStorage());
		return restored ?? { value: baseline, baseline };
	});
	const moveCandidate = draftState.value.toolsPathCandidate;
	const [folderReplacement, setFolderReplacement] =
		useState<FolderReplacement>();
	const [dismissedConflicts, setDismissedConflicts] = useState<
		Partial<Record<SettingsConflictGroup, string>>
	>({});
	const [valueReplacement, setValueReplacement] = useState<ValueReplacement>();
	const [leavePrompt, setLeavePrompt] = useState(false);
	const pendingHash = useRef<string | null>(null);
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
	const conflictGroupsPresent = conflictGroups.filter((group) =>
		conflictGroupFields[group].some((field) => draftConflicts.includes(field)),
	);
	const updateDraft = (values: Partial<SettingsTextDraft>) =>
		setDraftState((current) => ({
			...current,
			value: { ...current.value, ...values },
		}));
	const fieldDirty = (key: keyof SettingsTextDraft) =>
		draftState.value[key] !== draftState.baseline[key];
	const providerFields = [
		"musicBrainzMode",
		"musicBrainzURL",
		"musicBrainzDelay",
	] as const;
	const selfHostedDraft = draftState.value.musicBrainzMode === "self-hosted";
	const isApplicableProviderField = (field: keyof SettingsTextDraft) =>
		field === "musicBrainzMode" ||
		(selfHostedDraft &&
			(field === "musicBrainzURL" || field === "musicBrainzDelay"));
	const providerDirty = providerFields.some(
		(field) => isApplicableProviderField(field) && fieldDirty(field),
	);
	const groupHasConflict = (group: SettingsConflictGroup) =>
		conflictGroupFields[group].some((field) => draftConflicts.includes(field));
	const conflictSignatureNow = (group: SettingsConflictGroup) =>
		conflictGroupSignature(group, currentDraftValues);
	const dismissConflict = (group: SettingsConflictGroup) =>
		setDismissedConflicts((current) => ({
			...current,
			[group]: conflictSignatureNow(group),
		}));
	const clearConflictDismissal = (group: SettingsConflictGroup) =>
		setDismissedConflicts((current) => {
			if (!(group in current)) return current;
			const next = { ...current };
			delete next[group];
			return next;
		});
	const takeSavedGroup = (group: SettingsConflictGroup) => {
		const saved = currentDraftValues;
		const fields = conflictGroupFields[group];
		setDraftState((current) => {
			const value = { ...current.value };
			const baseline = { ...current.baseline };
			for (const field of fields) {
				Object.assign(value, { [field]: saved[field] });
				Object.assign(baseline, { [field]: saved[field] });
			}
			if (group === "toolsDirectory") {
				value.toolsPathCandidate = "";
				baseline.toolsPathCandidate = "";
			}
			return { value, baseline };
		});
		setFieldErrors((current) => {
			const next = { ...current };
			for (const field of fields) delete next[field];
			return next;
		});
		clearConflictDismissal(group);
		if (group === "toolsDirectory") {
			setMovePlan(null);
			setNotice("");
		}
	};
	const confirmValueConflict = (
		group: ValueReplacement["group"],
		approval?: ValueReplacement,
		options?: {
			details?: ValueReplacement["details"];
			opener?: Element;
		},
	) => {
		if (approval) {
			if (
				approval.group === group &&
				groupHasConflict(group) &&
				conflictGroupValuesEqual(group, currentDraftValues, approval.saved) &&
				conflictGroupValuesEqual(group, draftState.value, approval.draft)
			)
				return false;
			clearConflictDismissal(group);
			setNotice("staleSettings");
			return true;
		}
		if (!groupHasConflict(group)) return false;
		const saved = { ...currentDraftValues };
		const draft = { ...draftState.value };
		setValueReplacement({
			group,
			saved,
			draft,
			opener: options?.opener,
			details: options?.details ?? {
				saved: conflictGroupValue(group, saved),
				draft: conflictGroupValue(group, draft),
			},
		});
		return true;
	};
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
	const validateDraft = (value = draftState.value) => {
		const errors: Partial<Record<keyof SettingsTextDraft, string>> = {};
		if (providerDirty && !value.musicBrainzMode)
			errors.musicBrainzMode = copy.invalidProvider;
		if (!validConcurrency(value.concurrency)) errors.concurrency = "invalid";
		if (matchingThresholdNumber(value.matchingPairThreshold) === undefined)
			errors.matchingPairThreshold = "invalid";
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
			value.musicBrainzMode === "self-hosted" &&
			!validProviderDelay(value.musicBrainzDelay)
		)
			errors.musicBrainzDelay = copy.invalidDelay;
		setFieldErrors(errors);
		if (Object.keys(errors).length) {
			const first = errors.concurrency
				? "settings-concurrency"
				: errors.matchingPairThreshold
					? "settings-matching-threshold"
					: errors.musicBrainzMode
						? "settings-musicBrainzMode"
						: errors.musicBrainzURL
							? "settings-musicBrainzURL"
							: "settings-musicBrainzDelay";
			document.getElementById(first)?.focus();
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
			matchingPairThreshold,
			musicBrainzMode,
			publicationTemplate,
			workDirectory,
		} = merged;
		const musicBrainzURL = selfHostedDraft
			? merged.musicBrainzURL
			: currentDraftValues.musicBrainzURL;
		const musicBrainzDelay = selfHostedDraft
			? merged.musicBrainzDelay
			: currentDraftValues.musicBrainzDelay;
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
			matchingPairThreshold,
			musicBrainzMode,
			musicBrainzURL,
			musicBrainzDelay,
			publicationTemplate,
			workDirectory,
		};
		const value = {
			...draftState.value,
			concurrency,
			matchingPairThreshold,
			musicBrainzMode,
			...(selfHostedDraft && { musicBrainzURL, musicBrainzDelay }),
			publicationTemplate,
			workDirectory,
		};
		onSettings({
			...settings,
			concurrency,
			matchingPairThreshold,
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
		setDismissedConflicts({});
		setNotice("saved");
		return true;
	};
	const commitConcurrency = (approval?: ValueReplacement) => {
		if (!validConcurrency(draftState.value.concurrency)) {
			setFieldErrors((current) => ({
				...current,
				concurrency: "invalid",
			}));
			document.getElementById("settings-concurrency")?.focus();
			return;
		}
		const merged = mergeSettingsDraft(draftState, currentDraftValues, [
			"concurrency",
		]);
		const concurrency = merged.concurrency;
		if (confirmValueConflict("concurrency", approval)) return;
		onSettings({ ...settings, concurrency, runtimeSaved: true });
		setDraftState((current) => ({
			baseline: { ...current.baseline, concurrency },
			value: { ...current.value, concurrency },
		}));
		setFieldErrors((current) => ({ ...current, concurrency: undefined }));
		clearConflictDismissal("concurrency");
		setNotice("saved");
	};
	const commitMatchingThreshold = (approval?: ValueReplacement) => {
		if (
			matchingThresholdNumber(draftState.value.matchingPairThreshold) ===
			undefined
		) {
			setFieldErrors((current) => ({
				...current,
				matchingPairThreshold: "invalid",
			}));
			document.getElementById("settings-matching-threshold")?.focus();
			return;
		}
		const merged = mergeSettingsDraft(draftState, currentDraftValues, [
			"matchingPairThreshold",
		]);
		const matchingPairThreshold = merged.matchingPairThreshold;
		if (confirmValueConflict("matchingThreshold", approval)) return;
		onSettings({
			...settings,
			matchingPairThreshold,
			runtimeSaved: true,
		});
		setDraftState((current) => ({
			baseline: { ...current.baseline, matchingPairThreshold },
			value: { ...current.value, matchingPairThreshold },
		}));
		setFieldErrors((current) => ({
			...current,
			matchingPairThreshold: undefined,
		}));
		clearConflictDismissal("matchingThreshold");
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
				groupHasConflict(field) &&
				approval.saved === saved &&
				approval.draft === draft
			)
				return false;
			clearConflictDismissal(field);
			setNotice("staleSettings");
			return true;
		}
		if (!draftConflicts.includes(field)) return false;
		setFolderReplacement({ field, saved, draft });
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
		clearConflictDismissal("publicationTemplate");
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
		clearConflictDismissal("workDirectory");
		setNotice("saved");
	};
	const saveProviders = (
		musicBrainzMode: SettingsTextDraft["musicBrainzMode"],
		musicBrainzURL: string,
		musicBrainzDelay: string,
	) => {
		const selfHosted = musicBrainzMode === "self-hosted";
		const savedMusicBrainzURL = selfHosted
			? musicBrainzURL
			: (settings.musicBrainzURL ?? "");
		const savedMusicBrainzDelay = selfHosted
			? musicBrainzDelay
			: (settings.musicBrainzDelay ?? "");
		const configuration = {
			mode: musicBrainzMode,
			url: savedMusicBrainzURL,
			throttle: settings.musicBrainzThrottle,
			delay: savedMusicBrainzDelay,
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
		onSettings({
			...settings,
			musicBrainzMode,
			musicBrainzURL: savedMusicBrainzURL,
			musicBrainzDelay: savedMusicBrainzDelay,
			musicBrainzVerified: true,
			musicBrainzVerifiedFingerprint:
				musicBrainzConfigurationFingerprint(configuration),
		});
		setDraftState((current) => ({
			baseline: {
				...current.baseline,
				musicBrainzMode,
				...(selfHosted && {
					musicBrainzURL: savedMusicBrainzURL,
					musicBrainzDelay: savedMusicBrainzDelay,
				}),
			},
			value: selfHosted
				? {
						...current.value,
						musicBrainzMode,
						musicBrainzURL,
						musicBrainzDelay,
					}
				: { ...current.value, musicBrainzMode },
		}));
		clearConflictDismissal("musicBrainz");
		setNotice("providerSaved");
	};
	const commitProviders = (approval?: ValueReplacement) => {
		const merged = mergeSettingsDraft(draftState, currentDraftValues, [
			"musicBrainzMode",
			"musicBrainzURL",
			"musicBrainzDelay",
		]);
		const { musicBrainzMode } = merged;
		const selfHosted = musicBrainzMode === "self-hosted";
		const musicBrainzURL = selfHosted ? merged.musicBrainzURL : "";
		const musicBrainzDelay = selfHosted ? merged.musicBrainzDelay : "";
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
			musicBrainzMode === "self-hosted" &&
			!validProviderDelay(musicBrainzDelay)
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
		if (
			(approval || selfHosted || draftConflicts.includes("musicBrainzMode")) &&
			confirmValueConflict("musicBrainz", approval, {
				details: {
					saved: conflictGroupValue("musicBrainz", currentDraftValues),
					draft: conflictGroupValue("musicBrainz", merged),
				},
			})
		)
			return;
		saveProviders(musicBrainzMode, musicBrainzURL, musicBrainzDelay);
	};
	const selectMusicBrainzMode = (
		musicBrainzMode: SettingsTextDraft["musicBrainzMode"],
	) => {
		const musicBrainzURL = settings.musicBrainzURL ?? "";
		const musicBrainzDelay = settings.musicBrainzDelay ?? "";
		const configuration = {
			mode: musicBrainzMode,
			url: musicBrainzURL,
			throttle: settings.musicBrainzThrottle,
			delay: musicBrainzDelay,
		};
		const providerGroupConflicts = groupHasConflict("musicBrainz");
		const delayValid =
			musicBrainzDelay.trim() === "" ||
			/^\d+(?:\.\d+)?$/.test(musicBrainzDelay.trim());
		if (
			isValidMusicBrainzConfiguration(configuration) &&
			delayValid &&
			!providerGroupConflicts
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
	const unmanagedToolFiles =
		settings.unmanagedToolFiles ?? initialDemoUnmanagedToolFiles;
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
				onPress: (opener) => {
					const release = selectedRelease[kind];
					if (!release) return;
					const preflight = installPreflightDemo(
						settings.toolsPath,
						kind,
						release,
						installations,
						unmanagedToolFiles,
					);
					if (preflight.conflicts.length === 0) {
						update({
							installations: createDemoInstallation(
								installations,
								kind,
								release,
							),
						});
						setNotice("");
						return;
					}
					setInstallPlan({
						preflight,
						opener: opener instanceof HTMLElement ? opener : null,
					});
				},
			},
		};
	});
	const saveAcoustIDApplicationKey = () => {
		if (!acoustIDApplicationKey) return;
		update({ hasAcoustIDApplicationKey: true });
		setAcoustIDApplicationKey("");
	};
	const deleteAcoustIDApplicationKey = () => {
		update({ hasAcoustIDApplicationKey: false });
		setAcoustIDApplicationKey("");
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
		setOutputFeedback(undefined);
	};
	const openOutputReset = (approval?: FolderReplacement) => {
		if (confirmFolderConflict("outputDirectory", approval)) return;
		setOutputFeedback(undefined);
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
		const { kind } = installPlan.preflight;
		const release = selectedRelease[kind];
		if (!release) {
			setInstallPlan(null);
			setNotice("staleInstall");
			return;
		}
		const fresh = installPreflightDemo(
			settings.toolsPath,
			kind,
			release,
			installations,
			unmanagedToolFiles,
		);
		if (fresh.token !== installPlan.preflight.token) {
			setInstallPlan(null);
			setNotice("staleInstall");
			return;
		}
		update({
			installations: createDemoInstallation(
				installations,
				kind,
				installPlan.preflight.release,
			),
			unmanagedToolFiles: unmanagedToolFiles.filter(
				(path) => !fresh.conflicts.includes(path),
			),
		});
		setInstallPlan(null);
		setNotice("");
	};
	const startMove = (opener: Element, candidatePath: string) => {
		if (!isAbsoluteDemoServerPath(candidatePath.trim())) {
			setNotice("invalidPath");
			return;
		}
		if (
			overlapsAnyDemoPath(candidatePath, [
				settings.workDirectory,
				currentOutput,
				...sourceRoots,
			])
		) {
			setMovePlan(null);
			setNotice("pathOverlap");
			return;
		}
		moveOpener.current = opener instanceof HTMLElement ? opener : null;
		setMovePlan({
			...movePreflightDemo(candidatePath, managedCount),
			sourceDirectory: settings.toolsPath,
			removeOld,
		});
	};
	const requestMove = (opener: Element, approval?: ValueReplacement) => {
		const candidatePath = moveCandidate;
		if (
			confirmValueConflict("toolsDirectory", approval, {
				opener,
				details: {
					saved: conflictGroupValue("toolsDirectory", currentDraftValues),
					draft: displaySettingValue(candidatePath.trim()),
				},
			})
		)
			return;
		startMove(opener, candidatePath);
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
			setNotice("pathOverlap");
			return;
		}
		if (
			platformMismatch ||
			settings.toolsPath !== movePlan.sourceDirectory ||
			removeOld !== movePlan.removeOld ||
			freshPlan.token !== movePlan.token
		) {
			setMovePlan(null);
			setNotice(platformMismatch ? "conflicts" : "staleMove");
			return;
		}
		update({ toolsPath: movePlan.path, pathChecked: false, setupVerified: {} });
		setMovePlan(null);
		clearConflictDismissal("toolsDirectory");
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
		<SettingsPageFrame locale={locale}>
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
			{conflictGroupsPresent.map((group) => {
				if (
					dismissedConflicts[group] ===
					conflictGroupSignature(group, currentDraftValues)
				)
					return null;
				const saved = currentDraftValues;
				const label = conflictGroupLabel(group, copy);
				const inactiveProviderDraft =
					group === "musicBrainz" &&
					!selfHostedDraft &&
					(draftConflicts.includes("musicBrainzURL") ||
						draftConflicts.includes("musicBrainzDelay"));
				return (
					<SettingsDraftConflictNotice
						key={group}
						label={formatMessage(copy.settingsConflict, { label })}
						announcement="status"
						summary={`${formatMessage(copy.changedSavedValue, { label })} ${conflictGroupValue(group, saved)}.${inactiveProviderDraft ? ` ${copy.inactiveMusicBrainzDraft}` : ""}`}
						takeSaved={{
							label: formatMessage(copy.takeSavedValue, { label }),
							onPress: () => takeSavedGroup(group),
						}}
						continueEditing={{
							label: formatMessage(copy.continueEditing, { label }),
							onPress: () => dismissConflict(group),
						}}
					/>
				);
			})}
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
									settings.publicationFormat === "original"
										? "source"
										: settings.publicationFormat || "mka",
								choices: ["source", "mka", "m4a"],
								onChange: (publicationFormat) =>
									update({
										publicationFormat:
											publicationFormat === "source"
												? "original"
												: publicationFormat,
									}),
							},
							concurrency: {
								value: draftState.value.concurrency,
								invalid: Boolean(fieldErrors.concurrency),
								dirty:
									draftState.value.concurrency !==
									draftState.baseline.concurrency,
								saveDisabled: false,
								onChange: (concurrency) => updateDraft({ concurrency }),
								onSave: () => commitConcurrency(),
							},
							matchingThreshold: {
								value: draftState.value.matchingPairThreshold,
								invalid: Boolean(fieldErrors.matchingPairThreshold),
								dirty: fieldDirty("matchingPairThreshold"),
								saveDisabled: false,
								onChange: (matchingPairThreshold) =>
									updateDraft({ matchingPairThreshold }),
								onSave: () => commitMatchingThreshold(),
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
							albumCompletion: {
								enabled: settings.publicationAutoCompleteAlbums ?? false,
								dirty: false,
								onChange: (publicationAutoCompleteAlbums) =>
									update({ publicationAutoCompleteAlbums }),
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
				<SettingsTabPanel id="tools" className="grid gap-4">
					<SettingsToolsRootHeader
						locale={locale}
						savedDirectory={settings.toolsPath}
					/>
					<section aria-labelledby="managed-title" className="grid gap-3">
						<GitHubTokenDemo
							locale={locale}
							hasGitHubToken={settings.hasGitHubToken ?? false}
							onGitHubTokenChange={(hasGitHubToken) =>
								update({ hasGitHubToken })
							}
						/>
						<SettingsToolsCatalogSummary
							locale={locale}
							refresh={{
								checkedAt: toolCatalog.checkedAt,
								busy: toolCatalog.busy,
								onRefresh: toolCatalog.refresh,
							}}
						/>
						<SettingsManagedPackageCards
							locale={locale}
							packages={managedPackages}
						/>
						<SettingsToolsMoveEditor
							locale={locale}
							destination={{
								value: moveCandidate,
								onChange: (value) => {
									updateDraft({ toolsPathCandidate: value });
									setMovePlan(null);
									setNotice("");
								},
								browse: browseDirectory ?? inactiveDirectoryBrowse,
								resolveInitialPath: resolveDirectoryPath,
							}}
							removeOld={{
								checked: removeOld,
								onChange: (value) => {
									setRemoveOld(value);
									setMovePlan(null);
								},
							}}
							preflight={{
								disabled: platformMismatch,
								onPress: requestMove,
							}}
						/>
					</section>
				</SettingsTabPanel>
				<SettingsTabPanel id="providers">
					<SettingsProvidersSection
						locale={locale}
						snapshot={{
							busy: false,
							acoustID: {
								applicationKey: acoustIDApplicationKey,
								hasKey: settings.hasAcoustIDApplicationKey ?? false,
							},
							musicBrainz: {
								mode: draftState.value.musicBrainzMode,
								baseURL: draftState.value.musicBrainzURL,
								throttle: settings.musicBrainzThrottle ?? false,
								throttleDisabled: settings.musicBrainzMode !== "self-hosted",
								delaySeconds: draftState.value.musicBrainzDelay,
								verifiedAt: null,
								checked: musicBrainzIsVerified,
								dirty: providerDirty,
							},
							lrclib: {
								enabled: settings.lrclibEnabled ?? true,
								dirty: false,
							},
							errors: {
								musicBrainzMode: Boolean(fieldErrors.musicBrainzMode),
								musicBrainzBaseURL: Boolean(fieldErrors.musicBrainzURL),
								musicBrainzDelaySeconds: Boolean(fieldErrors.musicBrainzDelay),
							},
						}}
						actions={{
							onAcoustIDApplicationKeyChange: setAcoustIDApplicationKey,
							onSaveAcoustIDApplicationKey: saveAcoustIDApplicationKey,
							onDeleteAcoustIDApplicationKey: deleteAcoustIDApplicationKey,
							onMusicBrainzModeChange: selectMusicBrainzMode,
							onMusicBrainzBaseURLChange: (musicBrainzURL) =>
								updateDraft({ musicBrainzURL }),
							onMusicBrainzThrottleChange: (musicBrainzThrottle) =>
								update({ musicBrainzThrottle }),
							onMusicBrainzDelaySecondsChange: (musicBrainzDelay) =>
								updateDraft({ musicBrainzDelay }),
							onSaveAndCheckMusicBrainz: () => commitProviders(),
							onLrclibEnabledChange: (lrclibEnabled) =>
								update({ lrclibEnabled }),
						}}
					/>
				</SettingsTabPanel>
				<SettingsTabPanel id="genres" forceMount>
					<GenreCatalogDemoProvider
						loaded={settings.genresLoaded}
						onRefresh={() => update({ genresLoaded: true })}
					/>
				</SettingsTabPanel>
			</SettingsTabs>
			{leavePrompt && (
				<SettingsLeaveDialog
					title={copy.leaveSettingsTitle}
					description={copy.leaveSettingsText}
					dismissOnBackdrop
					error={
						notice === "invalidTemplate" ||
						notice === "invalidPath" ||
						notice === "busy" ||
						notice === "pathOverlap"
							? copy[notice]
							: undefined
					}
					status={draftStorageWarning ? copy.draftStorageWarning : undefined}
					actions={{
						stay: { label: copy.stay, onPress: () => setLeavePrompt(false) },
						keepDraft: {
							label: copy.leaveKeepDraft,
							onPress: () => leaveWithDraft(false),
						},
						discardDraft: {
							label: copy.leaveDiscard,
							onPress: () => leaveWithDraft(true),
						},
						save: {
							label: copy.leaveSave,
							isHidden: draftConflicts.length > 0,
							onPress: () => {
								if (outputDirty) {
									openOutputReset();
									return;
								}
								if (commitDraft()) leaveWithDraft(false);
							},
						},
					}}
				/>
			)}
			{(valueReplacement || folderReplacement) && (
				<SettingsConflictDialog
					title={copy.replaceSettingsTitle}
					message={copy.replaceSettingsText}
					details={folderReplacement ?? valueReplacement?.details}
					dismissOnBackdrop
					onDismiss={() => {
						setValueReplacement(undefined);
						setFolderReplacement(undefined);
					}}
					actions={{
						confirm: {
							label: copy.confirmReplace,
							onPress: () => {
								setValueReplacement(undefined);
								setFolderReplacement(undefined);
								if (folderReplacement) {
									if (folderReplacement.field === "publicationTemplate")
										commitTemplate(folderReplacement);
									else if (folderReplacement.field === "workDirectory")
										commitWorkDirectory(folderReplacement);
									else openOutputReset(folderReplacement);
								} else if (valueReplacement?.group === "concurrency") {
									commitConcurrency(valueReplacement);
								} else if (valueReplacement?.group === "matchingThreshold") {
									commitMatchingThreshold(valueReplacement);
								} else if (valueReplacement?.group === "musicBrainz") {
									commitProviders(valueReplacement);
								} else if (
									valueReplacement?.group === "toolsDirectory" &&
									valueReplacement.opener
								) {
									requestMove(valueReplacement.opener, valueReplacement);
								}
							},
						},
						cancel: {
							label: copy.cancel,
							onPress: () => {
								setValueReplacement(undefined);
								setFolderReplacement(undefined);
							},
						},
					}}
				/>
			)}
			{outputResetDialog}
			{installPlan && (
				<SettingsToolInstallDialog
					locale={locale}
					release={installPlan.preflight.release}
					targets={installPlan.preflight.targets}
					conflicts={installPlan.preflight.conflicts}
					busy={false}
					confirmDisabled={platformMismatch}
					returnFocusTo={installPlan.opener}
					onConfirm={startInstall}
					onDismiss={() => setInstallPlan(null)}
				/>
			)}
			{movePlan && (
				<SettingsToolsMoveReviewDialog
					returnFocusTo={moveOpener.current}
					locale={locale}
					sourceDirectory={movePlan.sourceDirectory}
					destinationDirectory={movePlan.path}
					removeOld={movePlan.removeOld}
					managedCount={movePlan.managedCount}
					conflicts={movePlan.conflicts}
					busy={false}
					confirmDisabled={platformMismatch}
					onConfirm={confirmMove}
					onDismiss={() => setMovePlan(null)}
				/>
			)}
		</SettingsPageFrame>
	);
}
