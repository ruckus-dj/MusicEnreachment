import { useEffect, useState } from "react";
import { useI18n } from "../../../app/i18n";
import { settingsMessages } from "../../../ui/features/settings/settingsFormMessages";
import type { SetupScreenViewProps } from "../../../ui/features/setup/SetupScreenView";
import type { SetupToolCard } from "../../../ui/features/setup/SetupToolCardsView";
import { setupMessages } from "../../../ui/features/setup/setupMessages";
import { useSetupTriggerFocus } from "../../../ui/features/setup/useSetupTriggerFocus";
import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "../../fixtures/directoryBrowseDemo";
import {
	isValidMusicBrainzConfiguration,
	musicBrainzConfigurationFingerprint,
} from "../settings/providerValidation";
import type {
	DemoSettings,
	SettingsScreenProps,
} from "../settings/SettingsScreen";
import {
	type DemoInstallPreflight,
	initialDemoUnmanagedToolFiles,
	installPreflightDemo,
	isAbsoluteDemoServerPath,
} from "../settings/ToolLifecycleDemo";
import { useGitHubTokenDemo } from "../tools/useGitHubTokenDemo";
import {
	type SetupToolKind,
	setupToolExecutables,
	setupToolReleases,
} from "./setupToolsLifecycle";
import {
	setupGenreCatalogEndpoint,
	useSetupGenreCatalogDemoProvider,
} from "./useSetupGenreCatalogDemoProvider";
import type { SetupToolDownloadActivity } from "./useSetupToolDownloadsDemoProvider";

type NoticeKey =
	| "toolsDirectoryRequired"
	| "missingPath"
	| "missingTools"
	| "missingPublication"
	| "missingGenres"
	| "summaryChecked"
	| "staleMove"
	| "invalidProvider";

type SetupAcoustIDNotice = "saved" | "deleted";

/**
 * The AcoustID draft lives only while the publication step is mounted. The
 * provider keeps the state, but resets it exactly as the former step-local
 * component did when the step is left or the platform check blocks the form.
 */
function useSetupAcoustIDDraftProvider({
	active,
	hasKey,
	onChanged,
}: {
	active: boolean;
	hasKey: boolean;
	onChanged: (hasKey: boolean) => void;
}) {
	const [draft, setDraft] = useState("");
	const [notice, setNotice] = useState<SetupAcoustIDNotice>();
	useEffect(() => {
		if (active) return;
		setDraft("");
		setNotice(undefined);
	}, [active]);
	const changeKey = (present: boolean) => {
		onChanged(present);
		setDraft("");
		setNotice(present ? "saved" : "deleted");
	};
	return {
		hasKey,
		draft,
		notice,
		onDraftChange: setDraft,
		onSave: () => {
			if (draft) changeKey(true);
		},
		onDelete: () => changeKey(false),
	};
}

export type SetupScreenProps = SettingsScreenProps & {
	step?: number;
	onStepChange?: (step: number) => void;
	/** Current tool activity supplied by the prototype data host, if available. */
	toolActivity?: Partial<SetupToolDownloadActivity>;
};

/**
 * Demo provider for Setup. Owns step selection, validation, tool catalog
 * state, directory checks and transient drafts; the view owns all markup.
 */
export function useSetupScreenDemoProvider({
	settings,
	onSettings,
	step: savedStep,
	onStepChange,
	toolActivity,
}: SetupScreenProps): SetupScreenViewProps {
	const { locale } = useI18n();
	const copy = settingsMessages[locale];
	const setupCopy = setupMessages[locale];
	const [localStep, setLocalStep] = useState(0);
	const [toolsDirectory, setToolsDirectory] = useState(settings.toolsPath);
	const step = savedStep ?? localStep;
	const [notice, setNotice] = useState<NoticeKey | "">("");
	const [lrclibDraft, setLrclibDraft] = useState(
		settings.lrclibEnabled ?? true,
	);
	const [musicBrainzChanged, setMusicBrainzChanged] = useState(false);
	const [installPlan, setInstallPlan] = useState<DemoInstallPreflight | null>(
		null,
	);
	const [installPlanChanged, setInstallPlanChanged] = useState(false);
	const installTriggerFocus = useSetupTriggerFocus();
	const update = (values: Partial<DemoSettings>) =>
		onSettings({ ...settings, ...values });
	const toolSelection = settings.selectedRelease ?? {};
	const installations = settings.installations ?? [];
	const unmanagedToolFiles =
		settings.unmanagedToolFiles ?? initialDemoUnmanagedToolFiles;
	const activeInstallation = (kind: SetupToolKind) =>
		installations.find(
			(item) => item.package === kind && item.status === "active",
		);
	const verifiedInstallation = (kind: SetupToolKind) => {
		const active = activeInstallation(kind);
		const selected = toolSelection[kind];
		if (
			active &&
			selected &&
			settings.setupVerified?.[kind] === selected &&
			active.release === selected
		) {
			return active;
		}
		return undefined;
	};
	const ffmpeg = verifiedInstallation("ffmpeg");
	const fpcalc = verifiedInstallation("fpcalc");
	const toolsReady = Boolean(ffmpeg && fpcalc);
	const platformMismatch = settings.platformMismatch ?? false;
	const outputPath = settings.outputCandidate;
	const format =
		settings.publicationFormat === "original"
			? "source"
			: (settings.publicationFormat ?? "");
	const musicBrainzMode = settings.musicBrainzMode ?? "";
	const musicBrainzURL = settings.musicBrainzURL ?? "";
	const providerReady = isValidMusicBrainzConfiguration({
		mode: musicBrainzMode,
		url: musicBrainzURL,
	});
	const providerFingerprint = musicBrainzConfigurationFingerprint({
		mode: musicBrainzMode,
		url: musicBrainzURL,
		throttle: settings.musicBrainzThrottle,
		delay: settings.musicBrainzDelay,
	});
	const providerVerified =
		settings.musicBrainzVerified &&
		settings.musicBrainzVerifiedFingerprint === providerFingerprint;
	const publicationReady = Boolean(
		outputPath &&
			format &&
			settings.outputChecked &&
			providerReady &&
			providerVerified,
	);
	const genreCatalogEndpoint = setupGenreCatalogEndpoint(
		musicBrainzMode,
		musicBrainzURL,
	);
	const genreCatalog = useSetupGenreCatalogDemoProvider({
		endpoint: genreCatalogEndpoint,
	});
	const genreCatalogReady = genreCatalog.state === "ready";

	const nextTool = (["ffmpeg", "fpcalc"] as const).find(
		(kind) => !verifiedInstallation(kind),
	);

	const setStep = (nextStep: number) => {
		if (nextStep === 2) setLrclibDraft(settings.lrclibEnabled ?? true);
		setNotice("");
		setLocalStep(nextStep);
		onStepChange?.(nextStep);
	};
	const chooseOutputPath = (path: string) => {
		const candidate = path.trim();
		const accepted = isAbsoluteDemoServerPath(candidate);
		update({
			outputCandidate: candidate,
			outputChecked: accepted,
			musicBrainzVerified: false,
		});
		setNotice(accepted ? "" : "missingPublication");
	};
	const preflightInstall = (kind: SetupToolKind, release: string) =>
		// Mirror the server: a clean preflight starts directly, and only exact
		// conflicts retain a plan for explicit approval.
		installPreflightDemo(
			settings.toolsPath,
			kind,
			release,
			installations,
			unmanagedToolFiles,
		);
	const beginInstall = (preflight: DemoInstallPreflight) => {
		// The host owns the admitted download lifecycle and settles settings only
		// while the frozen root and token still match the current state.
		toolActivity?.start?.(preflight);
		setInstallPlan(null);
		setNotice("");
	};
	const requestInstall = (kind: SetupToolKind, target: EventTarget | null) => {
		const release = toolSelection[kind];
		if (!release || platformMismatch || installPlan) return;
		if (!isAbsoluteDemoServerPath(settings.toolsPath)) {
			setNotice("missingPath");
			return;
		}
		setInstallPlanChanged(false);
		installTriggerFocus.capture(target);
		const preflight = preflightInstall(kind, release);
		if (!preflight.conflicts.length) {
			beginInstall(preflight);
			return;
		}
		setInstallPlan(preflight);
	};
	const confirmInstall = () => {
		if (!installPlan) return;
		const release = toolSelection[installPlan.kind];
		const fresh =
			!platformMismatch &&
			release &&
			isAbsoluteDemoServerPath(settings.toolsPath)
				? preflightInstall(installPlan.kind, release)
				: undefined;
		if (!fresh || fresh.token !== installPlan.token) {
			setInstallPlanChanged(true);
			return;
		}
		beginInstall(fresh);
	};
	const cancelInstall = () => {
		setInstallPlan(null);
		setInstallPlanChanged(false);
		installTriggerFocus.restore();
	};
	const next = () => {
		if (platformMismatch) return;
		if (step === 0) {
			const candidate = toolsDirectory.trim();
			if (!isAbsoluteDemoServerPath(candidate)) {
				setNotice(candidate ? "missingPath" : "toolsDirectoryRequired");
				return;
			}
			setToolsDirectory(candidate);
			if (candidate !== settings.toolsPath || !settings.pathChecked) {
				update({
					toolsPath: candidate,
					pathChecked: true,
					...(candidate !== settings.toolsPath ? { setupVerified: {} } : {}),
				});
			}
		} else if (step === 1) {
			if (!toolsReady) {
				setNotice("missingTools");
				return;
			}
		} else if (step === 2) {
			const validOutput = isAbsoluteDemoServerPath(outputPath.trim());
			const validProvider = providerReady;
			if (!validProvider && musicBrainzMode === "self-hosted") {
				setNotice("invalidProvider");
				return;
			}
			if (!validOutput || !validProvider || !format) {
				setNotice("missingPublication");
				return;
			}
			if (!genreCatalogReady) {
				setNotice("missingGenres");
				return;
			}
			// Commit the validated output and provider state together; edits invalidate the check.
			update({
				outputChecked: true,
				genresLoaded: true,
				lrclibEnabled: lrclibDraft,
				musicBrainzVerified: true,
				musicBrainzVerifiedFingerprint: providerFingerprint,
			});
			setStep(3);
			return;
		} else {
			if (!genreCatalogReady) {
				setNotice("missingGenres");
				return;
			}
			if (
				!settings.pathChecked ||
				!toolsReady ||
				!publicationReady ||
				!isAbsoluteDemoServerPath(outputPath.trim())
			) {
				setNotice("staleMove");
				return;
			}
			update({ currentOutput: outputPath });
			window.location.hash = "/";
			return;
		}
		setStep(Math.min(step + 1, 3));
	};
	const canProceed =
		step === 0
			? true
			: step === 1
				? toolsReady
				: step === 2
					? Boolean(
							outputPath && musicBrainzMode && format && genreCatalogReady,
						)
					: settings.pathChecked &&
						toolsReady &&
						publicationReady &&
						genreCatalogReady;
	const noticeText =
		notice === "summaryChecked"
			? locale === "ru"
				? "Настройки прототипа проверены."
				: "Prototype selections checked."
			: notice === "toolsDirectoryRequired"
				? setupCopy.toolsDirectoryRequired
				: notice
					? copy[notice]
					: "";

	const acoustID = useSetupAcoustIDDraftProvider({
		active: step === 2 && !platformMismatch,
		hasKey: settings.hasAcoustIDApplicationKey ?? false,
		onChanged: (hasAcoustIDApplicationKey) =>
			update({ hasAcoustIDApplicationKey }),
	});
	const githubToken = useGitHubTokenDemo(
		{
			locale,
			hasGitHubToken: settings.hasGitHubToken ?? false,
			onGitHubTokenChange: (hasGitHubToken) => update({ hasGitHubToken }),
		},
		step === 1,
	);
	// Only the portable activity fields belong to the view model; the host's
	// start and duplicate-package authority stay out of the presentation tree.
	const toolActivityView = toolActivity
		? {
				operations: toolActivity.operations,
				completed: toolActivity.completed,
				operationError: toolActivity.operationError,
				loadingOperations: toolActivity.loadingOperations,
			}
		: undefined;

	return {
		locale,
		wizard: {
			step,
			help: [
				setupCopy.toolsDirectoryHelp,
				setupCopy.toolsHelp,
				`${setupCopy.publicationHelp} ${setupCopy.metadataHelp}`,
				locale === "ru"
					? "Итог составлен по выбранным данным прототипа. Если что-то не готово, вернитесь к нужному шагу."
					: "The summary uses the prototype’s selections. If anything is not ready, return to the relevant step.",
			][step],
			error: platformMismatch
				? copy.conflicts
				: step === 0
					? noticeText || undefined
					: undefined,
			notice: step === 0 ? undefined : noticeText || undefined,
			nextDisabled: !canProceed || platformMismatch,
			onBack: () => setStep(step - 1),
			onNext: next,
		},
		directories: {
			kind: "tools",
			selected: settings.pathChecked && toolsDirectory === settings.toolsPath,
			fields: [
				{
					kind: "tools",
					value: toolsDirectory,
					onSelect: setToolsDirectory,
				},
			],
			locale,
			browse: browseDemoDirectory,
			resolveInitialPath: demoDirectoryStart,
		},
		tools: {
			...toolActivityView,
			description: copy.toolDescription,
			cards: {
				locale,
				tools: (["ffmpeg", "fpcalc"] as const).map((kind): SetupToolCard => {
					const verified = verifiedInstallation(kind);
					if (verified) {
						return {
							kind,
							state: "ready",
							versions: [
								{
									id: verified.id,
									version: verified.release,
									active: verified.status === "active",
								},
							],
						};
					}
					const selected = toolSelection[kind];
					const pendingDownload = toolActivity?.busyKinds?.has(kind) ?? false;
					return {
						kind,
						state: "install",
						releases: setupToolReleases[kind].map((identity) => ({
							identity,
						})),
						selected: selected ?? null,
						pending: pendingDownload,
						onSelect: (identity) =>
							update({
								selectedRelease: {
									...toolSelection,
									[kind]: identity ?? undefined,
								},
								setupVerified: {
									...settings.setupVerified,
									[kind]: undefined,
								},
								setupFailures: {
									...settings.setupFailures,
									[kind]: undefined,
								},
							}),
						action: {
							disabled:
								!selected ||
								platformMismatch ||
								Boolean(installPlan) ||
								pendingDownload,
							onPress: (event) => requestInstall(kind, event.target),
						},
						verification: { state: "available" },
						executables: setupToolExecutables(kind),
					};
				}),
				nextTool: toolsReady ? undefined : nextTool,
			},
			confirmation: installPlan
				? {
						title: setupCopy.confirmOverwrite,
						packageName: setupCopy[installPlan.kind],
						release: installPlan.release,
						executables: setupToolExecutables(installPlan.kind),
						conflicts: installPlan.conflicts,
						confirmLabel: setupCopy.confirmOverwriteButton,
						cancelLabel: setupCopy.cancel,
						error: installPlanChanged ? copy.staleInstall : undefined,
						onConfirm: confirmInstall,
						onCancel: cancelInstall,
					}
				: undefined,
			token: githubToken,
		},
		publication: {
			directory: {
				kind: "output",
				fields: [
					{
						kind: "output",
						value: outputPath,
						onSelect: chooseOutputPath,
					},
				],
				locale,
				browse: browseDemoDirectory,
				resolveInitialPath: demoDirectoryStart,
			},
			options: {
				locale,
				format,
				removeSources: settings.sourceCleanupEnabled ?? false,
				onFormat: (value) =>
					update({
						publicationFormat: value === "source" ? "original" : value,
					}),
				onRemoveSources: (sourceCleanupEnabled) =>
					update({ sourceCleanupEnabled }),
			},
			metadata: {
				providers: {
					locale,
					mode: musicBrainzMode,
					baseURL: musicBrainzURL,
					verification:
						providerVerified || genreCatalogReady
							? "verified"
							: musicBrainzChanged
								? "changed"
								: "untested",
					disabled: platformMismatch,
					lrclib: { enabled: lrclibDraft, onChange: setLrclibDraft },
					onModeChange: (mode) => {
						setMusicBrainzChanged(true);
						update({ musicBrainzMode: mode, musicBrainzVerified: false });
					},
					onBaseURLChange: (musicBrainzURL) => {
						setMusicBrainzChanged(true);
						update({ musicBrainzURL, musicBrainzVerified: false });
					},
				},
				acoustID: platformMismatch ? undefined : { locale, ...acoustID },
			},
			genres: {
				locale,
				state: genreCatalog.state,
				count: genreCatalog.count,
				disabled: !providerReady || platformMismatch,
				onLoad: () => {
					setMusicBrainzChanged(false);
					genreCatalog.load();
				},
			},
		},
		summary: {
			locale,
			status: {
				healthy:
					!platformMismatch &&
					settings.pathChecked &&
					toolsReady &&
					publicationReady &&
					genreCatalogReady &&
					isAbsoluteDemoServerPath(outputPath.trim()),
				problems: [
					...(platformMismatch ? [copy.conflicts] : []),
					...(!settings.pathChecked ? [copy.missingPath] : []),
					...(!toolsReady ? [copy.missingTools] : []),
					...(!publicationReady || !isAbsoluteDemoServerPath(outputPath.trim())
						? [copy.missingPublication]
						: []),
					...(!genreCatalogReady ? [copy.missingGenres] : []),
				],
			},
			values: {
				publication: {
					directory: outputPath || setupCopy.notSpecified,
					format: format || "unselected",
					explanation:
						format === "source"
							? setupCopy.sourceFormatExplanation
							: format === "mka"
								? setupCopy.mkaExplanation
								: format === "m4a"
									? setupCopy.m4aExplanation
									: "",
					removeSources: settings.sourceCleanupEnabled ?? false,
				},
				tools: {
					ffmpeg: ffmpeg
						? `${setupCopy.ready}${ffmpeg.release}`
						: setupCopy.unknownToolState,
					fpcalc: fpcalc
						? `${setupCopy.ready}${fpcalc.release}`
						: setupCopy.unknownToolState,
				},
				metadata: {
					lrclibEnabled: settings.lrclibEnabled ?? true,
					hasAcoustIdKey: settings.hasAcoustIDApplicationKey ?? false,
					musicbrainz:
						providerVerified || genreCatalogReady
							? `${setupCopy.connectionChecked}${musicBrainzMode === "self-hosted" ? musicBrainzURL : setupCopy.publicService}`
							: setupCopy.connectionNotChecked,
					genres: genreCatalogReady
						? setupCopy.genreCatalogLoaded
						: setupCopy.genreCatalogMissing,
				},
				technical: {
					platform: setupCopy.noData,
					toolsDirectory: settings.toolsPath || setupCopy.notSpecified,
					musicbrainzCheckedAt: setupCopy.noData,
					ffmpegCheckedAt: setupCopy.noData,
					fpcalcCheckedAt: setupCopy.noData,
				},
				showNextStep: true,
			},
		},
		summaryRecheck: {
			label: setupCopy.updateCheck,
			disabled: platformMismatch,
			onPress: () => {
				if (!genreCatalogReady) {
					setStep(2);
					setNotice("missingGenres");
					return;
				}
				setNotice("summaryChecked");
			},
		},
	};
}
