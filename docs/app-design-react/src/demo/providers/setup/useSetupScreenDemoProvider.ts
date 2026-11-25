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
	activateOrCreateDemoInstallation,
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
	setupToolStateAfterAttempt,
} from "./setupToolsLifecycle";
import {
	setupGenreCatalogEndpoint,
	useSetupGenreCatalogDemoProvider,
} from "./useSetupGenreCatalogDemoProvider";

type NoticeKey =
	| "missingPath"
	| "missingTools"
	| "missingPublication"
	| "missingGenres"
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
}: SetupScreenProps): SetupScreenViewProps {
	const { locale } = useI18n();
	const copy = settingsMessages[locale];
	const setupCopy = setupMessages[locale];
	const [localStep, setLocalStep] = useState(0);
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
	const toolsReady = (["ffmpeg", "fpcalc"] as const).every((kind) =>
		Boolean(
			toolSelection[kind] &&
				settings.setupVerified?.[kind] === toolSelection[kind] &&
				activeInstallation(kind)?.release === toolSelection[kind],
		),
	);
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
		(kind) =>
			!toolSelection[kind] ||
			settings.setupVerified?.[kind] !== toolSelection[kind] ||
			activeInstallation(kind)?.release !== toolSelection[kind],
	);

	const setStep = (nextStep: number) => {
		if (nextStep === 2) setLrclibDraft(settings.lrclibEnabled ?? true);
		setLocalStep(nextStep);
		onStepChange?.(nextStep);
	};
	const choosePath = (kind: "tools" | "output", path: string) => {
		if (kind === "tools") {
			const accepted = isAbsoluteDemoServerPath(path.trim());
			update({
				toolsPath: path.trim(),
				pathChecked: accepted,
				setupVerified: {},
			});
			setNotice(accepted ? "" : "missingPath");
		} else {
			const accepted = isAbsoluteDemoServerPath(path.trim());
			update({
				outputCandidate: path.trim(),
				outputChecked: accepted,
				musicBrainzVerified: false,
			});
			setNotice(accepted ? "" : "missingPublication");
		}
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
		const outcome = setupToolStateAfterAttempt(preflight.release, true);
		update({
			installations: activateOrCreateDemoInstallation(
				installations,
				preflight.kind,
				preflight.release,
			),
			setupVerified: {
				...settings.setupVerified,
				[preflight.kind]: outcome.release,
			},
			setupFailures: {
				...settings.setupFailures,
				[preflight.kind]: undefined,
			},
			...(preflight.conflicts.length > 0 && {
				unmanagedToolFiles: unmanagedToolFiles.filter(
					(path) => !preflight.conflicts.includes(path),
				),
			}),
		});
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
			if (
				!isAbsoluteDemoServerPath(settings.toolsPath) ||
				!settings.pathChecked
			) {
				setNotice("missingPath");
				return;
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
			setNotice("");
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
			window.location.hash = "review";
			return;
		}
		setNotice("");
		setStep(Math.min(step + 1, 3));
	};
	const canProceed =
		step === 0
			? settings.pathChecked && isAbsoluteDemoServerPath(settings.toolsPath)
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
	const noticeText = notice ? copy[notice] : "";

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

	return {
		locale,
		wizard: {
			step,
			error: platformMismatch ? copy.conflicts : undefined,
			notice: noticeText || undefined,
			nextDisabled: !canProceed || platformMismatch,
			onBack: () => setStep(step - 1),
			onNext: next,
		},
		directories: {
			kind: "tools",
			selected: settings.pathChecked,
			fields: [
				{
					kind: "tools",
					value: settings.toolsPath,
					onSelect: (path) => choosePath("tools", path),
				},
			],
			locale,
			browse: browseDemoDirectory,
			resolveInitialPath: demoDirectoryStart,
		},
		tools: {
			description: copy.toolDescription,
			cards: {
				locale,
				tools: (["ffmpeg", "fpcalc"] as const).map((kind): SetupToolCard => {
					const active = activeInstallation(kind);
					const selected = toolSelection[kind];
					const verified =
						active &&
						selected &&
						settings.setupVerified?.[kind] === selected &&
						active.release === selected;
					return {
						kind,
						state: "install",
						releases: setupToolReleases[kind].map((identity) => ({
							identity,
						})),
						selected: selected ?? null,
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
							disabled: !selected || platformMismatch || Boolean(installPlan),
							onPress: (event) => requestInstall(kind, event.target),
						},
						verification: verified
							? {
									state: "verified",
									release: active.release,
								}
							: { state: "available" },
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
						onSelect: (path) => choosePath("output", path),
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
					directory: outputPath,
					format: format || undefined,
					removeSources: settings.sourceCleanupEnabled,
				},
				tools: {
					ffmpeg: activeInstallation("ffmpeg")?.release ?? "",
					fpcalc: activeInstallation("fpcalc")?.release ?? "",
				},
				metadata: {
					lrclibEnabled: settings.lrclibEnabled,
					hasAcoustIdKey: settings.hasAcoustIDApplicationKey,
					musicbrainz:
						musicBrainzMode === "public"
							? copy.publicSource
							: `${copy.privateSource} · ${musicBrainzURL}`,
					genres: genreCatalogReady ? copy.genreLoaded : copy.genreUnavailable,
				},
			},
		},
	};
}
