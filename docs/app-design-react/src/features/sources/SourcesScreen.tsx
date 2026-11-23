import { type Dispatch, type SetStateAction, useEffect, useState } from "react";
import { formStyles as styles } from "../../components/styles/formStyles";
import { Button } from "../../components/ui";
import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "../../fixtures/directoryBrowseDemo";
import type { SourceScanDemoProvider } from "../../fixtures/useSourceScanDemoProvider";
import { type Locale, useI18n } from "../../i18n";
import { overlapsAnyDemoPath } from "../../infrastructure/demoPathValidation";
import type { SourceFiles } from "../library/catalogData";
import { SourceArtifactCleanupView } from "./SourceArtifactCleanupView";
import { useSourceArtifactCleanupDemoProvider } from "./sourceArtifactCleanupDemoProvider";
import { SourceInspectorView } from "./SourceInspectorView";
import { SourceInventoryView } from "./SourceInventoryView";
import {
	SourceRegistrationView,
	sourceRegistrationMessage,
	sourceRegistrationMessages,
	type SourceRegistrationErrorKey,
	useSourceRegistrationFocus,
} from "./SourceRegistrationView";
import { SourceRootDetailView } from "./SourceRootDetailView";
import { SourceRootHeadingView } from "./SourceRootHeadingView";
import { SourceRootList, type SourceRootListRow } from "./SourceRootList";
import { SourceRootRemovalDialog } from "./SourceRootRemovalDialog";
import {
	SourceRootSettingsView,
	type SourceRootChange,
} from "./SourceRootSettingsView";
import { SourceScanPanel } from "./SourceScanPanel";
import { demoSourceLocationHref } from "./sourceDemoRoute";
import { sourceInspectorDemoProvider } from "./sourceInspectorDemoProvider";
import {
	inventoryPathStale,
	retainedInventoryLocations,
	rootLocations,
} from "./sourceInventory";
import { type SourceMessageKey, sourceMessage } from "./sourceMessages";
import {
	blockStagedSourceQueue,
	canRemoveSourceRoot,
	processSourceQueue,
	removeRootProcessingState,
	retryCancelledSourceFile,
	retrySourceStep,
	type SourceProcessingState,
	type SourceProcessingStep,
} from "./sourceProcessingModel";

type Mode = "in_place" | "staged";
export interface SourceRoot {
	id: string;
	name: string;
	path: string;
	mode: Mode;
	available: boolean | null;
	checked: string;
	/** Set only after an explicit successful scan in this prototype session. */
	lastSuccessfulScanAt?: string;
	/** Inventory generation recorded by that same scan. */
	inventoryCount?: number;
	/** Folder whose successful scan produced the current inventory. */
	inventoryPath?: string;
	/** The retained inventory covers only locations inside that scanned folder. */
	fixturePath?: string;
	/** The inventory still belongs to a previous folder until the next scan. */
}
export const initialSourceRoots: SourceRoot[] = [
	{
		id: "root-inbox",
		name: "Входящие",
		path: "/mnt/inbox",
		mode: "staged",
		available: true,
		checked: "26.09.2026 10:20",
	},
	{
		id: "root-library",
		name: "Музыкальная библиотека",
		path: "/mnt/music",
		mode: "in_place",
		available: true,
		checked: "26.09.2026 10:20",
	},
	{
		id: "root-backup",
		name: "Резервный источник",
		path: "/mnt/backup",
		mode: "in_place",
		available: true,
		checked: "26.09.2026 10:20",
	},
	{
		id: "root-archive",
		name: "Архивный диск",
		path: "/mnt/archive",
		mode: "in_place",
		available: false,
		checked: "25.09.2026 18:40",
	},
];

type RootRemoval = { id: string; path: string; locations: string[] };

const scanDate = (locale: Locale, value: string) =>
	new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
		dateStyle: "short",
		timeStyle: "short",
	}).format(new Date(value));

export function SourcesScreen({
	roots,
	onRoots,
	files,
	onRemove,
	selectedRootId,
	selectedLocationId,
	workDirectory = "",
	toolsPath = "",
	currentOutput = "",
	processing,
	scans,
	onProcessingState,
}: {
	roots: SourceRoot[];
	onRoots: Dispatch<SetStateAction<SourceRoot[]>>;
	files: SourceFiles;
	onRemove: (rootId: string) => void;
	selectedRootId?: string;
	selectedLocationId?: string;
	workDirectory?: string;
	toolsPath?: string;
	currentOutput?: string;
	processing: SourceProcessingState;
	scans: SourceScanDemoProvider;
	onProcessingState: (state: SourceProcessingState) => void;
}) {
	const { locale } = useI18n();
	const m = (key: SourceMessageKey, values?: Record<string, string | number>) =>
		sourceMessage(locale, key, values);
	const [adding, setAdding] = useState(false);
	const registrationFocus = useSourceRegistrationFocus(adding);
	const [name, setName] = useState("");
	const [path, setPath] = useState("/mnt/music");
	const [mode, setMode] = useState<Mode | "">("");
	const [registrationError, setRegistrationError] =
		useState<SourceRegistrationErrorKey | null>(null);
	const [notice, setNotice] = useState<{
		key: SourceMessageKey;
		values?: Record<string, string | number>;
	} | null>(null);
	const [settingsError, setSettingsError] = useState<{
		rootId: string;
		key: "pathScanActive" | "pathOverlap";
	} | null>(null);
	useEffect(() => {
		setSettingsError((current) =>
			current && current.rootId !== selectedRootId ? null : current,
		);
	}, [selectedRootId]);
	const notify = (
		key: SourceMessageKey,
		values?: Record<string, string | number>,
	) => setNotice({ key, values });
	const [removal, setRemoval] = useState<RootRemoval | null>(null);
	const root = selectedRootId
		? roots.find((item) => item.id === selectedRootId)
		: undefined;
	const demoScanState = root ? scans.states[root.id] : undefined;
	const inventoryLocations = root
		? retainedInventoryLocations(files, root)
		: [];
	const retryableInventory = new Set(
		root && !scans.active(root.id) && !inventoryPathStale(root)
			? inventoryLocations.map((location) => `${root.id}:${location.id}`)
			: [],
	);
	const rootFiles = root
		? processing.files.filter((file) => file.rootId === root.id)
		: [];

	const updateRoots = onRoots;
	const updateProcessing = onProcessingState;
	const cleanup = useSourceArtifactCleanupDemoProvider({
		locale,
		roots,
		files,
		processing,
		updateProcessing,
	});
	const hasActiveRootWork = (rootId: string) =>
		scans.active(rootId) || !canRemoveSourceRoot(processing, rootId);
	const statusLabel = (status: string) => {
		const key: Record<string, SourceMessageKey> = {
			queued: "statusQueued",
			running: "statusRunning",
			succeeded: "statusSucceeded",
			failed: "statusFailed",
			completed: "statusCompleted",
		};
		return key[status] ? m(key[status]) : status;
	};
	const audioStepLabel = (step: SourceProcessingStep) => {
		if (step.id !== "audio_inspection") return statusLabel(step.status);
		if (step.status === "failed") return m("audioCheckFailed");
		if (step.status === "queued" && !step.attempts) return m("audioNotChecked");
		return statusLabel(step.status);
	};
	const requestRootRemoval = (root: SourceRoot) => {
		if (hasActiveRootWork(root.id)) {
			notify("rootActive");
			return;
		}
		setRemoval({
			id: root.id,
			path: root.path,
			locations: rootLocations(files, root.id)
				.map((location) => location.id)
				.sort(),
		});
	};
	const startScan = (root: SourceRoot) => scans.start(root);
	const register = () => {
		if (!name.trim()) {
			setRegistrationError("nameRequired");
			return;
		}
		if (!mode) {
			setRegistrationError("modeRequired");
			return;
		}
		if (overlapsAnyDemoPath(path, [workDirectory, toolsPath, currentOutput])) {
			setRegistrationError("pathOverlap");
			return;
		}
		const id = `root-${crypto.randomUUID()}`;
		updateRoots([
			...roots,
			{
				id,
				name: name.trim(),
				path,
				mode,
				available: null,
				checked: "—",
			},
		]);
		setAdding(false);
		setRegistrationError(null);
		notify("sourceAdded");
		// Registration only opens the root. Registering never scans, processes,
		// or invents scan results.
		window.location.hash = `sources/${encodeURIComponent(id)}`;
	};
	const saveRoot = async (change: SourceRootChange): Promise<boolean> => {
		if (!root) return false;
		setSettingsError(null);
		if (change.field === "path") {
			if (scans.active(root.id)) {
				setSettingsError({ rootId: root.id, key: "pathScanActive" });
				return false;
			}
			if (
				overlapsAnyDemoPath(change.value, [
					workDirectory,
					toolsPath,
					currentOutput,
				])
			) {
				setSettingsError({
					rootId: root.id,
					key: "pathOverlap",
				});
				return false;
			}
		}
		updateRoots((current) =>
			current.map((item) => {
				if (item.id !== root.id) return item;
				switch (change.field) {
					case "name":
						return { ...item, name: change.value };
					case "mode":
						return { ...item, mode: change.value };
					case "path":
						return {
							...item,
							path: change.value,
							fixturePath: item.fixturePath ?? item.path,
						};
				}
			}),
		);
		return true;
	};

	const scanLabel = root?.lastSuccessfulScanAt
		? scanDate(locale, root.lastSuccessfulScanAt)
		: m("neverScanned");
	const inventoryLabel = root?.lastSuccessfulScanAt
		? m("inventoryCount", { count: root.inventoryCount ?? 0 })
		: m("neverScanned");

	const rows: SourceRootListRow[] = roots.map((item) => ({
		id: item.id,
		name: item.name,
		path: item.path,
		mode: item.mode,
		scanning: scans.active(item.id),
		availability:
			item.available === true
				? "available"
				: item.available === false
					? "unavailable"
					: "unknown",
		lastSuccessfulScan: item.lastSuccessfulScanAt
			? {
					label: scanDate(locale, item.lastSuccessfulScanAt),
					dateTime: item.lastSuccessfulScanAt,
				}
			: undefined,
		inventory: item.lastSuccessfulScanAt
			? {
					label: m("inventoryCount", { count: item.inventoryCount ?? 0 }),
					notes: item.inventoryCount === 0 ? [m("noInventory")] : undefined,
				}
			: { label: m("neverScanned") },
		href: `#sources/${encodeURIComponent(item.id)}`,
	}));

	const noticeNode = notice ? (
		<p className={styles.formNotice} role="status">
			{m(notice.key, notice.values)}
		</p>
	) : null;

	if (selectedRootId && selectedLocationId)
		return (
			<SourceInspectorView
				key={`${selectedRootId}:${selectedLocationId}`}
				{...sourceInspectorDemoProvider({
					locale,
					rootId: selectedRootId,
					locationId: selectedLocationId,
					root,
					files,
					processing,
					updateProcessing,
					workDirectory,
					scanning: scans.active(selectedRootId),
				})}
			/>
		);

	return (
		<section className={styles.page}>
			{selectedRootId ? (
				root ? (
					<SourceRootDetailView
						locale={locale}
						heading={
							<SourceRootHeadingView
								title={root.name}
								path={root.path}
								back={{ href: "#sources", label: m("backToSources") }}
								incoming={
									root.available === true
										? { href: "#review", label: m("reviewGroups") }
										: undefined
								}
							/>
						}
						status={noticeNode}
						scan={
							<SourceScanPanel
								locale={locale}
								state={
									demoScanState?.kind === "fixtureFailure"
										? { kind: "failed", message: m("scanFailedFixture") }
										: (demoScanState ?? { kind: "idle" })
								}
								onStart={() => startScan(root)}
								onRetry={() => startScan(root)}
								startDisabled={root.available === false}
							/>
						}
						inventory={
							inventoryLocations.length > 0 ? (
								<SourceInventoryView
									locale={locale}
									rows={inventoryLocations.map((location) => {
										const observed = rootFiles.find(
											(item) => item.id === `${root.id}:${location.id}`,
										);
										const step = observed?.steps.find(
											(item) => item.id === "audio_inspection",
										);
										const modified = location.mtime
											? new Date(location.mtime)
											: null;
										return {
											id: location.id,
											path: location.path,
											href: demoSourceLocationHref(root.id, location.id),
											size:
												location.size === undefined
													? "—"
													: new Intl.NumberFormat(locale, {
															style: "unit",
															unit: "byte",
														}).format(location.size),
											modified:
												modified && Number.isFinite(modified.getTime())
													? new Intl.DateTimeFormat(locale, {
															dateStyle: "short",
															timeStyle: "short",
														}).format(modified)
													: undefined,
											status: step
												? audioStepLabel(step)
												: m("audioNotChecked"),
											statusCode:
												step?.status === "failed" ? "probe_error" : undefined,
										};
									})}
								/>
							) : undefined
						}
						overview={{
							name: root.name,
							path: root.path,
							mode: root.mode,
							modeNote:
								root.mode === "staged" ? (
									<>
										{m("workDirectory")}: {workDirectory || "—"} ·{" "}
										<a href="#settings">{m("configure")}</a>
									</>
								) : undefined,
							availability:
								root.available === true
									? "available"
									: root.available === false
										? "unavailable"
										: "unknown",
							availabilityNote:
								root.available === false ? m("checkUnavailable") : undefined,
							lastScan: {
								label: scanLabel,
								dateTime: root.lastSuccessfulScanAt,
							},
							inventory: {
								label: inventoryLabel,
								path: root.inventoryPath,
								stale: inventoryPathStale(root),
								notes:
									root.lastSuccessfulScanAt && root.inventoryCount === 0
										? [m("noInventory")]
										: undefined,
							},
							onRemove: () => requestRootRemoval(root),
						}}
					>
						<SourceRootSettingsView
							key={root.id}
							locale={locale}
							name={root.name}
							path={root.path}
							mode={root.mode}
							browse={browseDemoDirectory}
							resolveInitialPath={demoDirectoryStart}
							error={
								settingsError?.rootId === root.id
									? settingsError.key === "pathOverlap"
										? sourceRegistrationMessage(locale, "pathOverlap")
										: m(settingsError.key)
									: undefined
							}
							onSave={saveRoot}
						/>
						<section
							className={styles.panel}
							aria-labelledby="source-processing-title"
						>
							<h2 id="source-processing-title">{m("processingTitle")}</h2>
							<p>{m("processingDescription")}</p>
							<p className="text-xs text-[var(--muted)]">{m("freshness")}</p>
							{rootFiles.length === 0 ? (
								<p>{m("noFiles")}</p>
							) : (
								<ul className="m-0 grid gap-3 pl-5">
									{rootFiles.map((file) => (
										<li key={file.id}>
											<strong>{file.path}</strong> ·{" "}
											{m("fileStatus", { status: statusLabel(file.status) })}
											{file.blockedReason && (
												<p role="status">
													{file.blockedReason === "output_reset"
														? m("outputResetCancelled")
														: m("workDirectoryRequired")}{" "}
													{file.blockedReason === "work_directory_required" && (
														<a href="#settings">{m("configureInSettings")}</a>
													)}
												</p>
											)}
											{file.blockedReason === "output_reset" && (
												<Button
													isDisabled={!retryableInventory.has(file.id)}
													onPress={() => {
														const retried = retryCancelledSourceFile(
															processing,
															file.id,
														);
														if (file.mode === "staged" && !workDirectory) {
															updateProcessing(
																blockStagedSourceQueue(retried, file.rootId),
															);
															notify("workDirectoryRequired");
															return;
														}
														updateProcessing(
															processSourceQueue(retried, file.rootId, file.id),
														);
													}}
												>
													{m("retryCancelled")}
												</Button>
											)}
											<ul className="grid gap-2 pl-5">
												{file.steps.map((step) => (
													<li key={step.id}>
														{step.id === "audio_inspection"
															? m("stepAudio")
															: m("stepFingerprint")}
														: {audioStepLabel(step)}
														{step.error && <p role="alert">{m(step.error)}</p>}
														{step.status === "failed" && (
															<Button
																isDisabled={!retryableInventory.has(file.id)}
																aria-label={m("retryStep", {
																	step:
																		step.id === "audio_inspection"
																			? m("stepAudio")
																			: m("stepFingerprint"),
																})}
																onPress={() => {
																	const retried = retrySourceStep(
																		processing,
																		file.id,
																		step.id,
																	);
																	if (
																		file.mode === "staged" &&
																		!workDirectory
																	) {
																		updateProcessing(
																			blockStagedSourceQueue(
																				retried,
																				file.rootId,
																			),
																		);
																		notify("workDirectoryRequired");
																		return;
																	}
																	updateProcessing(
																		processSourceQueue(
																			retried,
																			file.rootId,
																			file.id,
																		),
																	);
																}}
															>
																{m("retry")}
															</Button>
														)}
													</li>
												))}
											</ul>
										</li>
									))}
								</ul>
							)}
						</section>
						<p>{m("unavailablePublications")}</p>
					</SourceRootDetailView>
				) : (
					<>
						<header className={styles.pageHeader}>
							<div>
								<h1 className={styles.pageTitle}>{m("sourceNotFound")}</h1>
								<p className={styles.pageDescription}>
									<code className="[overflow-wrap:anywhere]">
										{selectedRootId}
									</code>
								</p>
							</div>
						</header>
						<p>
							<a href="#sources">{m("backToSources")}</a>
						</p>
					</>
				)
			) : (
				<>
					<header
						className={`${styles.pageHeader} flex flex-wrap items-center justify-between gap-3`}
					>
						<div>
							<h1 className={styles.pageTitle}>{m("title")}</h1>
							<p className={styles.pageDescription}>{m("description")}</p>
						</div>
						{!adding && (
							<div className={styles.objectActions}>
								<Button
									id={registrationFocus.triggerId}
									onPress={() => {
										setAdding(true);
										setName("");
										setPath("/mnt/music");
										setMode("");
										setRegistrationError(null);
									}}
								>
									{sourceRegistrationMessages[locale].addSource}
								</Button>
							</div>
						)}
					</header>
					{noticeNode}
					{adding && (
						<SourceRegistrationView
							locale={locale}
							name={name}
							path={path}
							mode={mode}
							error={
								registrationError
									? sourceRegistrationMessage(locale, registrationError)
									: undefined
							}
							browse={browseDemoDirectory}
							resolveInitialPath={demoDirectoryStart}
							onNameChange={setName}
							onPathChange={setPath}
							onModeChange={setMode}
							onSubmit={register}
							onCancel={() => {
								registrationFocus.restoreOnClose();
								setAdding(false);
								setRegistrationError(null);
							}}
						/>
					)}
					<p className="my-2 text-xs text-[var(--muted)]">
						{sourceRegistrationMessages[locale].sourceTableHelp}
					</p>
					<SourceRootList
						rows={rows}
						locale={locale}
						onScan={(id) => {
							const source = roots.find((item) => item.id === id);
							if (source) startScan(source);
						}}
					/>
					<SourceArtifactCleanupView {...cleanup} />
					<p>{m("unavailablePublications")}</p>
					<p>
						{m("workDirectory")}: {workDirectory || "—"} ·{" "}
						<a href="#settings">{m("configureInSettings")}</a>
					</p>
				</>
			)}
			{removal && (
				<SourceRootRemovalDialog
					locale={locale}
					path={removal.path}
					count={removal.locations.length}
					onCancel={() => setRemoval(null)}
					onConfirm={() => {
						if (hasActiveRootWork(removal.id)) {
							notify("rootActive");
							setRemoval(null);
							return;
						}
						const currentLocations = rootLocations(files, removal.id)
							.map((location) => location.id)
							.sort();
						if (currentLocations.join("|") !== removal.locations.join("|")) {
							notify("rootChanged");
							setRemoval(null);
							return;
						}
						updateProcessing(removeRootProcessingState(processing, removal.id));
						onRemove(removal.id);
						notify("rootRemoved");
						setRemoval(null);
						window.location.hash = "sources";
					}}
				/>
			)}
		</section>
	);
}
