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
	const [moveCandidate, setMoveCandidate] = useState("");
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
		mode: settings.musicBrainzMode ?? "",
		url: settings.musicBrainzURL,
		throttle: settings.musicBrainzThrottle,
		delay: settings.musicBrainzDelay,
	});
	const musicBrainzIsVerified =
		settings.musicBrainzVerified &&
		settings.musicBrainzVerifiedFingerprint === musicBrainzFingerprint;
	const saveProviders = () => {
		const configuration = {
			mode: settings.musicBrainzMode ?? "",
			url: settings.musicBrainzURL,
			throttle: settings.musicBrainzThrottle,
			delay: settings.musicBrainzDelay,
		};
		if (!isValidMusicBrainzConfiguration(configuration)) {
			setNotice("invalidProvider");
			return;
		}
		update({
			musicBrainzVerified: true,
			musicBrainzVerifiedFingerprint: musicBrainzFingerprint,
		});
		setNotice("providerSaved");
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
		setMoveCandidate("");
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
							<TextField
								value={settings.concurrency ?? "4"}
								onChange={(concurrency) => update({ concurrency })}
							>
								<Label>{copy.concurrency}</Label>
								<Input type="number" min="1" />
							</TextField>
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
						<Button
							className="justify-self-start min-[1400px]:col-span-2"
							onPress={() => {
								update({ runtimeSaved: true });
								setNotice("saved");
							}}
						>
							{copy.save}
						</Button>
						{settings.runtimeSaved && <p role="status">{copy.saved}</p>}
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
									setMoveCandidate(value);
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
						selectedKey={settings.musicBrainzMode ?? ""}
						onSelectionChange={(key) =>
							update({
								musicBrainzMode: key
									? (String(key) as "public" | "self-hosted")
									: "",
								musicBrainzVerified: false,
								musicBrainzVerifiedFingerprint: undefined,
							})
						}
					>
						<Label>{copy.musicbrainz}</Label>
						<Button>
							<SelectValue />
						</Button>
						<Popover>
							<ListBox>
								<ListBoxItem id="public">{copy.publicService}</ListBoxItem>
								<ListBoxItem id="self-hosted">{copy.selfHosted}</ListBoxItem>
							</ListBox>
						</Popover>
					</Select>
					{settings.musicBrainzMode === "self-hosted" && (
						<TextField
							value={settings.musicBrainzURL ?? ""}
							onChange={(musicBrainzURL) =>
								update({
									musicBrainzURL,
									musicBrainzVerified: false,
									musicBrainzVerifiedFingerprint: undefined,
								})
							}
						>
							<Label>{copy.baseUrl}</Label>
							<Input type="url" />
						</TextField>
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
						value={settings.musicBrainzDelay ?? ""}
						onChange={(musicBrainzDelay) => update({ musicBrainzDelay })}
					>
						<Label>{copy.delay}</Label>
						<Input type="number" min="0" />
					</TextField>
					<p>{musicBrainzIsVerified ? copy.verified : copy.notVerified}</p>
					<Button onPress={saveProviders}>{copy.saveProviders}</Button>
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
						} else if (picker === "tools") setMoveCandidate(path);
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
