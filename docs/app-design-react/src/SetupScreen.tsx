import { useState } from "react";
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
} from "./ui";
import { DirectoryPicker } from "./DirectoryPicker";
import { GenreLibrary } from "./GenreLibrary";
import type { DemoSettings, SettingsScreenProps } from "./SettingsScreen";
import { formStyles as styles } from "./formStyles";
import {
	activateOrCreateDemoInstallation,
	isAbsoluteDemoServerPath,
	type ManagedToolPackage,
} from "./ToolLifecycleDemo";
import {
	setupToolExecutables,
	setupToolReleases,
	setupToolStateAfterAttempt,
	type SetupToolKind,
} from "./setupToolsLifecycle";
import { useI18n } from "./i18n";
import { settingsMessages } from "./i18n/namespaces/settings";
import {
	isValidMusicBrainzConfiguration,
	musicBrainzConfigurationFingerprint,
} from "./providerValidation";

const stepKeys = [
	"stepToolsDirectory",
	"stepInstallTools",
	"stepPublication",
	"stepSummary",
] as const;
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
	const [picker, setPicker] = useState<"tools" | "output" | null>(null);
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
		setPicker(null);
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
		<section className={styles.page} aria-labelledby="setup-title">
			<header className={styles.pageHeader}>
				<h1 className={styles.pageTitle} id="setup-title">
					{copy.setup}
				</h1>
				<p className={styles.pageDescription}>{copy.subtitle}</p>
			</header>
			{platformMismatch && (
				<p className={styles.formNotice} role="alert">
					{copy.conflicts}
				</p>
			)}
			<nav aria-label={copy.setup}>
				<ol className={styles.setupSteps}>
					{stepKeys.map((key, index) => (
						<li aria-current={index === step ? "step" : undefined} key={key}>
							<span className={styles.setupStepIndex}>
								{index < step ? "✓" : index + 1}
							</span>
							<span>{copy[key]}</span>
						</li>
					))}
				</ol>
			</nav>
			<section className={styles.panel} aria-labelledby="setup-step-title">
				<h2 className="mb-2 text-sm font-semibold" id="setup-step-title">
					{copy[stepKeys[step]]}
				</h2>
				<div className="grid min-w-0 gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-[14px]">
					{step === 0 && (
						<>
							<TextField
								className={styles.searchField}
								value={settings.toolsPath}
								isReadOnly
							>
								<Label>{copy.toolsDirectory}</Label>
								<div className={styles.objectActions}>
									<Input id="tools-path" />
									<Button onPress={() => setPicker("tools")}>
										{copy.chooseDirectory}
									</Button>
								</div>
								<small>{copy.absolutePathHelp}</small>
							</TextField>
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
											kind === "ffmpeg"
												? copy.ffmpegPackage
												: copy.fpcalcPackage
										}
									>
										<h3 className="m-0 font-semibold">
											{kind === "ffmpeg"
												? copy.ffmpegPackage
												: copy.fpcalcPackage}
										</h3>
										<Select
											selectedKey={toolSelection[kind] ?? null}
											onSelectionChange={(key) =>
												update({
													selectedRelease: {
														...toolSelection,
														[kind]: key ? String(key) : undefined,
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
										>
											<Label>
												{copy.versionLabel} · {kind}
											</Label>
											<Button>
												<SelectValue />
											</Button>
											<Popover>
												<ListBox>
													{setupToolReleases[kind].map((release) => (
														<ListBoxItem id={release} key={release}>
															{release}
														</ListBoxItem>
													))}
												</ListBox>
											</Popover>
										</Select>
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
						</>
					)}
					{step === 2 && (
						<>
							<section className="grid gap-2">
								<h3 className="m-0 font-semibold">{copy.publication}</h3>
								<p>{copy.outputDescription}</p>
								<div className={styles.objectActions}>
									<TextField value={outputPath} isReadOnly>
										<Label>{copy.publicationDirectory}</Label>
										<Input />
									</TextField>
									<Button onPress={() => setPicker("output")}>
										{copy.chooseDirectory}
									</Button>
								</div>
							</section>
							<section className="grid gap-2 border-t border-[var(--line)] pt-3">
								<h3 className="m-0 font-semibold">{copy.musicbrainz}</h3>
								<p>{copy.providerDescription}</p>
								<Select
									selectedKey={musicBrainzMode || null}
									onSelectionChange={(key) =>
										update({
											musicBrainzMode: key
												? (String(key) as "public" | "self-hosted")
												: "",
											musicBrainzVerified: false,
										})
									}
								>
									<Label>{copy.musicbrainz}</Label>
									<Button>
										<SelectValue />
									</Button>
									<Popover>
										<ListBox>
											<ListBoxItem id="public">{copy.publicSource}</ListBoxItem>
											<ListBoxItem id="self-hosted">
												{copy.privateSource}
											</ListBoxItem>
										</ListBox>
									</Popover>
								</Select>
								{musicBrainzMode === "self-hosted" && (
									<TextField
										value={musicBrainzURL}
										onChange={(musicBrainzURL) =>
											update({ musicBrainzURL, musicBrainzVerified: false })
										}
									>
										<Label>{copy.baseUrl}</Label>
										<Input type="url" />
									</TextField>
								)}
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
						<dl className="my-[10px] grid grid-cols-[minmax(100px,1fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] [&_dd]:[overflow-wrap:anywhere] [&_dd]:my-0 [&_dt]:text-[var(--muted)]">
							<div>
								<dt>{copy.summaryTools}</dt>
								<dd>
									{(["ffmpeg", "fpcalc"] as const)
										.map(
											(kind) =>
												`${kind} · ${activeInstallation(kind)?.release ?? ""}`,
										)
										.join("; ")}
								</dd>
							</div>
							<div>
								<dt>{copy.summaryDirectory}</dt>
								<dd>{outputPath}</dd>
							</div>
							<div>
								<dt>{copy.summaryProvider}</dt>
								<dd>
									{musicBrainzMode === "public"
										? copy.publicSource
										: `${copy.privateSource} · ${musicBrainzURL}`}
								</dd>
							</div>
							<div>
								<dt>{copy.genreOptional}</dt>
								<dd>
									{settings.genresLoaded
										? copy.genreLoaded
										: copy.genreUnavailable}
								</dd>
							</div>
						</dl>
					)}
					{noticeText && (
						<p
							className="my-2 rounded bg-[var(--warning-soft)] px-3 py-[10px]"
							role="status"
							aria-live="polite"
						>
							{noticeText}
						</p>
					)}
					<div className={styles.setupFooter}>
						{step > 0 && (
							<Button onPress={() => setStep(step - 1)}>{copy.back}</Button>
						)}
						<Button isDisabled={!canProceed || platformMismatch} onPress={next}>
							{step === 3 ? copy.finish : copy.continue}
						</Button>
					</div>
				</div>
			</section>
			{picker && (
				<DirectoryPicker
					key={`setup-${picker}`}
					initialPath={
						picker === "tools" ? settings.toolsPath || "/" : outputPath || "/"
					}
					onChoose={(path) => choosePath(picker, path)}
					onClose={() => setPicker(null)}
				/>
			)}
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
		</section>
	);
}
