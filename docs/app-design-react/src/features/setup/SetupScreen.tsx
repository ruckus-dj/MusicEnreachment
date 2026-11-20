import { useState } from "react";
import { GenreLibrary } from "../../components/GenreLibrary";
import { Button, Dialog, Heading, Modal } from "../../components/ui";
import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "../../fixtures/directoryBrowseDemo";
import { useI18n } from "../../i18n";
import { settingsMessages } from "../../i18n/namespaces/settings";
import {
	isValidMusicBrainzConfiguration,
	musicBrainzConfigurationFingerprint,
} from "../../infrastructure/providerValidation";
import type {
	DemoSettings,
	SettingsScreenProps,
} from "../settings/SettingsScreen";
import {
	activateOrCreateDemoInstallation,
	isAbsoluteDemoServerPath,
	type ManagedToolPackage,
} from "../settings/ToolLifecycleDemo";
import { GitHubTokenDemo } from "../tools/GitHubTokenDemo";
import { SetupDirectoryFieldsView } from "./SetupDirectoryFields";
import { SetupMusicBrainzFields } from "./SetupMusicBrainzFields";
import { SetupSummaryView } from "./SetupSummaryView";
import { SetupToolReleasePicker } from "./SetupToolReleasePicker";
import { SetupWizard } from "./SetupWizard";
import {
	type SetupToolKind,
	setupToolExecutables,
	setupToolReleases,
	setupToolStateAfterAttempt,
} from "./setupToolsLifecycle";

const stepKeys = ["directories", "tools", "publication", "summary"] as const;
type NoticeKey =
	| "missingPath"
	| "missingTools"
	| "missingPublication"
	| "staleMove"
	| "invalidProvider";

export function SetupScreen({
	settings,
	onSettings,
	step: savedStep,
	onStepChange,
}: SettingsScreenProps & {
	step?: number;
	onStepChange?: (step: number) => void;
}) {
	const { locale } = useI18n();
	const copy = settingsMessages[locale];
	const [localStep, setLocalStep] = useState(0);
	const step = savedStep ?? localStep;
	const [notice, setNotice] = useState<NoticeKey | "">("");
	const [installTarget, setInstallTarget] = useState<ManagedToolPackage | null>(
		null,
	);
	const [installToken, setInstallToken] = useState("");
	const update = (values: Partial<DemoSettings>) =>
		onSettings({ ...settings, ...values });
	const toolSelection = settings.selectedRelease ?? {};
	const installations = settings.installations ?? [];
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
	const musicBrainzMode = settings.musicBrainzMode ?? "";
	const musicBrainzURL = settings.musicBrainzURL ?? "";
	const providerReady = isValidMusicBrainzConfiguration({
		mode: musicBrainzMode,
		url: musicBrainzURL,
	});
	const publicationReady = Boolean(
		outputPath &&
			settings.outputChecked &&
			providerReady &&
			settings.musicBrainzVerified &&
			settings.musicBrainzVerifiedFingerprint ===
				musicBrainzConfigurationFingerprint({
					mode: musicBrainzMode,
					url: musicBrainzURL,
					throttle: settings.musicBrainzThrottle,
					delay: settings.musicBrainzDelay,
				}),
	);
	const nextTool = (["ffmpeg", "fpcalc"] as const).find(
		(kind) =>
			!toolSelection[kind] ||
			settings.setupVerified?.[kind] !== toolSelection[kind] ||
			activeInstallation(kind)?.release !== toolSelection[kind],
	);

	const setStep = (nextStep: number) => {
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
	const requestInstall = (kind: SetupToolKind) => {
		const release = toolSelection[kind];
		if (!release || platformMismatch) return;
		setInstallTarget(kind);
		setInstallToken([kind, release, settings.toolsPath].join("|"));
	};
	const installTool = () => {
		if (!installTarget) return;
		const release = toolSelection[installTarget];
		const token = [installTarget, release, settings.toolsPath].join("|");
		if (
			!release ||
			token !== installToken ||
			!isAbsoluteDemoServerPath(settings.toolsPath)
		) {
			setInstallTarget(null);
			setNotice("staleMove");
			return;
		}
		const outcome = setupToolStateAfterAttempt(release, true);
		update({
			installations: activateOrCreateDemoInstallation(
				installations,
				installTarget,
				release,
			),
			setupVerified: {
				...settings.setupVerified,
				[installTarget]: outcome.release,
			},
			setupFailures: { ...settings.setupFailures, [installTarget]: undefined },
		});
		setInstallTarget(null);
		setNotice("");
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
			if (!validOutput || !validProvider) {
				setNotice("missingPublication");
				return;
			}
			// Commit the validated output and provider state together; edits invalidate the check.
			update({
				outputChecked: true,
				musicBrainzVerified: true,
				musicBrainzVerifiedFingerprint: musicBrainzConfigurationFingerprint({
					mode: musicBrainzMode,
					url: musicBrainzURL,
					throttle: settings.musicBrainzThrottle,
					delay: settings.musicBrainzDelay,
				}),
			});
			setNotice("");
			setStep(3);
			return;
		} else {
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
					? Boolean(outputPath && musicBrainzMode)
					: settings.pathChecked && toolsReady && publicationReady;
	const noticeText = notice ? copy[notice] : "";

	return (
		<>
			<SetupWizard
				locale={locale}
				steps={stepKeys}
				step={step}
				error={platformMismatch ? copy.conflicts : undefined}
				notice={noticeText || undefined}
				onBack={() => setStep(step - 1)}
				onNext={next}
				nextDisabled={!canProceed || platformMismatch}
			>
				{step === 0 && (
					<>
						<SetupDirectoryFieldsView
							fields={[
								{
									kind: "tools",
									value: settings.toolsPath,
									onSelect: (path) => choosePath("tools", path),
								},
							]}
							locale={locale}
							browse={browseDemoDirectory}
							resolveInitialPath={demoDirectoryStart}
							description={copy.absolutePathHelp}
						/>
						{settings.pathChecked && <p role="status">{copy.pathReady}</p>}
					</>
				)}
				{step === 1 && (
					<>
						<p>{copy.toolDescription}</p>
						{(["ffmpeg", "fpcalc"] as const).map((kind) => {
							const active = activeInstallation(kind);
							return (
								<section
									className="grid gap-2 border-b border-[var(--line)] pb-3"
									key={kind}
									aria-label={
										kind === "ffmpeg" ? copy.ffmpegPackage : copy.fpcalcPackage
									}
								>
									<h3 className="m-0 font-semibold">
										{kind === "ffmpeg"
											? copy.ffmpegPackage
											: copy.fpcalcPackage}
									</h3>
									<SetupToolReleasePicker
										locale={locale}
										kind={kind}
										selected={toolSelection[kind] ?? null}
										releases={setupToolReleases[kind].map((release) => ({
											identity: release,
										}))}
										onChange={(identity) =>
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
											})
										}
									/>
									<p className="m-0">
										{copy.status}:{" "}
										{settings.setupVerified?.[kind] === toolSelection[kind] &&
										active?.release === toolSelection[kind]
											? `${copy.verifiedTool} · ${active?.release ?? ""}`
											: copy.available}
									</p>
									<p className="m-0 text-xs text-[var(--muted)]">
										{setupToolExecutables(kind).join(", ")}
									</p>
									<Button
										isDisabled={!toolSelection[kind] || platformMismatch}
										onPress={() => requestInstall(kind)}
									>
										{copy.install}
									</Button>
								</section>
							);
						})}
						{!toolsReady && (
							<p role="status">
								{copy.nextInstrument}:{" "}
								{nextTool === "ffmpeg" ? copy.ffmpegName : copy.fpcalcName}
							</p>
						)}
						<GitHubTokenDemo
							locale={locale}
							hasGitHubToken={settings.hasGitHubToken ?? false}
							onGitHubTokenChange={(hasGitHubToken) =>
								update({ hasGitHubToken })
							}
						/>
					</>
				)}
				{step === 2 && (
					<>
						<section className="grid gap-2">
							<h3 className="m-0 font-semibold">{copy.publication}</h3>
							<p>{copy.outputDescription}</p>
							<SetupDirectoryFieldsView
								fields={[
									{
										kind: "output",
										value: outputPath,
										onSelect: (path) => choosePath("output", path),
									},
								]}
								locale={locale}
								browse={browseDemoDirectory}
								resolveInitialPath={demoDirectoryStart}
							/>
						</section>
						<section className="grid gap-2 border-t border-[var(--line)] pt-3">
							<SetupMusicBrainzFields
								locale={locale}
								mode={musicBrainzMode}
								baseURL={musicBrainzURL}
								onModeChange={(mode) =>
									update({
										musicBrainzMode: mode,
										musicBrainzVerified: false,
									})
								}
								onBaseURLChange={(musicBrainzURL) =>
									update({ musicBrainzURL, musicBrainzVerified: false })
								}
							/>
						</section>
						<section className="grid gap-2 border-t border-[var(--line)] pt-3">
							<h3 className="m-0 font-semibold">{copy.genreOptional}</h3>
							<GenreLibrary
								loaded={settings.genresLoaded}
								onLoadedChange={(genresLoaded) => update({ genresLoaded })}
								buttonLabel={copy.refreshGenres}
							/>
						</section>
					</>
				)}
				{step === 3 && (
					<SetupSummaryView
						locale={locale}
						status={{
							healthy:
								!platformMismatch &&
								settings.pathChecked &&
								toolsReady &&
								publicationReady &&
								isAbsoluteDemoServerPath(outputPath.trim()),
							problems: [
								...(platformMismatch ? [copy.conflicts] : []),
								...(!settings.pathChecked ? [copy.missingPath] : []),
								...(!toolsReady ? [copy.missingTools] : []),
								...(!publicationReady ||
								!isAbsoluteDemoServerPath(outputPath.trim())
									? [copy.missingPublication]
									: []),
							],
						}}
						values={{
							publication: {
								directory: outputPath,
								format:
									settings.publicationFormat === "mka"
										? "mka"
										: settings.publicationFormat === "original"
											? "source"
											: undefined,
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
								genres: settings.genresLoaded
									? copy.genreLoaded
									: copy.genreUnavailable,
							},
						}}
					/>
				)}
			</SetupWizard>
			{installTarget && toolSelection[installTarget] && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setInstallTarget(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">{copy.installVerify}</Heading>
						<p>
							{installTarget} · {toolSelection[installTarget]}
						</p>
						<p>{setupToolExecutables(installTarget).join(", ")}</p>
						<Button isDisabled={platformMismatch} onPress={installTool}>
							{copy.installVerify}
						</Button>
						<Button onPress={() => setInstallTarget(null)}>
							{copy.cancel}
						</Button>
					</Dialog>
				</Modal>
			)}
		</>
	);
}
