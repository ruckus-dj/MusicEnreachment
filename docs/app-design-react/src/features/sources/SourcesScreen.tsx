import { useState } from "react";
import { DirectoryPicker } from "../../components/DirectoryPicker";
import { formStyles as styles } from "../../components/styles/formStyles";
import {
	Button,
	Input,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Radio,
	RadioGroup,
	Select,
	SelectValue,
	TextField,
} from "../../components/ui";
import type { SourceScanDemoProvider } from "../../fixtures/useSourceScanDemoProvider";
import { type Locale, useI18n } from "../../i18n";
import { overlapsAnyDemoPath } from "../../infrastructure/demoPathValidation";
import type { SourceFiles } from "../library/catalogData";
import { SourceArtifactCleanupView } from "./SourceArtifactCleanupView";
import { useSourceArtifactCleanupDemoProvider } from "./sourceArtifactCleanupDemoProvider";
import { SourceInspectorView } from "./SourceInspectorView";
import { SourceInventoryView } from "./SourceInventoryView";
import { SourceRootHeadingView } from "./SourceRootHeadingView";
import { SourceRootList, type SourceRootListRow } from "./SourceRootList";
import { SourceRootRemovalDialog } from "./SourceRootRemovalDialog";
import { SourceScanPanel } from "./SourceScanPanel";
import { demoSourceLocationHref } from "./sourceDemoRoute";
import { sourceInspectorDemoProvider } from "./sourceInspectorDemoProvider";
import { rootLocations } from "./sourceInventory";
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
	onRoots: (roots: SourceRoot[]) => void;
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
	const [name, setName] = useState("");
	const [path, setPath] = useState("/mnt/music");
	const [mode, setMode] = useState<Mode | "">("");
	const [picker, setPicker] = useState(false);
	const [notice, setNotice] = useState<{
		key: SourceMessageKey;
		values?: Record<string, string | number>;
	} | null>(null);
	const notify = (
		key: SourceMessageKey,
		values?: Record<string, string | number>,
	) => setNotice({ key, values });
	const [removal, setRemoval] = useState<RootRemoval | null>(null);
	const root = selectedRootId
		? roots.find((item) => item.id === selectedRootId)
		: undefined;
	const demoScanState = root ? scans.states[root.id] : undefined;
	const inventoryLocations = root?.lastSuccessfulScanAt
		? rootLocations(files, root.id)
		: [];
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

	const availabilityLabel = (item: SourceRoot) =>
		item.available === null
			? m("notChecked")
			: item.available
				? m("available")
				: m("unavailable");

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
					<>
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
						{noticeNode}
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
						{inventoryLocations.length > 0 && (
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
										status: step ? audioStepLabel(step) : m("audioNotChecked"),
										statusCode:
											step?.status === "failed" ? "probe_error" : undefined,
									};
								})}
							/>
						)}
						<section className={styles.panel}>
							<h2>{m("sourceSummary")}</h2>
							<dl className="m-0 grid grid-cols-[minmax(130px,.3fr)_minmax(0,1fr)] gap-x-3 gap-y-2 text-xs max-[600px]:grid-cols-1">
								<dt className="text-[var(--muted)]">{m("nameColumn")}</dt>
								<dd className="m-0 min-w-0 [overflow-wrap:anywhere]">
									{root.name}
								</dd>
								<dt className="text-[var(--muted)]">{m("pathColumn")}</dt>
								<dd className="m-0 min-w-0">
									<code className="[overflow-wrap:anywhere]">{root.path}</code>
								</dd>
								<dt className="text-[var(--muted)]">{m("modeColumn")}</dt>
								<dd className="m-0 min-w-0">
									<Select
										selectedKey={root.mode}
										aria-label={m("modeFor", { name: root.name })}
										onSelectionChange={(key) => {
											const next = String(key) as Mode;
											updateRoots(
												roots.map((item) =>
													item.id === root.id ? { ...item, mode: next } : item,
												),
											);
										}}
									>
										<Button>
											<SelectValue />
										</Button>
										<Popover>
											<ListBox>
												<ListBoxItem id="in_place">{m("inPlace")}</ListBoxItem>
												<ListBoxItem id="staged">{m("staged")}</ListBoxItem>
											</ListBox>
										</Popover>
									</Select>
									<p className="my-1 text-xs text-[var(--muted)]">
										{m("futureMode")}
									</p>
									{root.mode === "staged" && (
										<p className="my-1 text-xs text-[var(--muted)]">
											{m("workDirectory")}: {workDirectory || "—"} ·{" "}
											<a href="#settings">{m("configure")}</a>
										</p>
									)}
								</dd>
								<dt className="text-[var(--muted)]">
									{m("availabilityColumn")}
								</dt>
								<dd className="m-0 min-w-0">
									{availabilityLabel(root)}
									{root.available === false && (
										<p className="my-1 text-xs text-[var(--muted)]">
											{m("checkUnavailable")}
										</p>
									)}
								</dd>
								<dt className="text-[var(--muted)]">
									{m("lastSuccessfulScan")}
								</dt>
								<dd className="m-0 min-w-0">
									{root.lastSuccessfulScanAt ? (
										<time dateTime={root.lastSuccessfulScanAt}>
											{scanLabel}
										</time>
									) : (
										scanLabel
									)}
								</dd>
								<dt className="text-[var(--muted)]">{m("inventory")}</dt>
								<dd className="m-0 min-w-0">
									{inventoryLabel}
									{root.lastSuccessfulScanAt && root.inventoryCount === 0 && (
										<span className="mt-1 block text-xs text-[var(--muted)]">
											{m("noInventory")}
										</span>
									)}
								</dd>
							</dl>
							<div className={styles.objectActions}>
								<Button
									aria-label={m("rootActionLabel", { name: root.name })}
									onPress={() => requestRootRemoval(root)}
								>
									{m("removeRoot")}
								</Button>
							</div>
						</section>
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
															processSourceQueue(retried, file.rootId),
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
																		processSourceQueue(retried, file.rootId),
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
					</>
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
						<div className={styles.objectActions}>
							<Button
								onPress={() => {
									setAdding(true);
									setName("");
									setPath("/mnt/music");
									setMode("");
								}}
							>
								{m("addSource")}
							</Button>
						</div>
					</header>
					{noticeNode}
					{adding && (
						<section className={styles.panel}>
							<h2>{m("newSource")}</h2>
							<TextField
								className={`${styles.searchField} max-w-none`}
								value={name}
								onChange={setName}
							>
								<Label>{m("name")}</Label>
								<Input placeholder={m("namePlaceholder")} />
							</TextField>
							<TextField
								className={`${styles.searchField} max-w-none`}
								value={path}
								isReadOnly
							>
								<Label>{m("serverDirectory")}</Label>
								<Input />
							</TextField>
							<Button onPress={() => setPicker(true)}>
								{m("chooseFolder")}
							</Button>
							<p>{m("onlyDirectory")}</p>
							<RadioGroup
								className="my-3 grid gap-1"
								aria-label={m("processingMode")}
								value={mode}
								onChange={(value) => setMode(value as Mode)}
							>
								<Radio className={styles.modeChoice} value="in_place">
									{m("inPlace")}
								</Radio>
								<p className="my-[3px] ml-[25px] text-xs text-[var(--muted)]">
									{m("inPlaceHelp")}
								</p>
								<Radio className={styles.modeChoice} value="staged">
									{m("staged")}
								</Radio>
								<p className="my-[3px] ml-[25px] text-xs text-[var(--muted)]">
									{m("stagedHelp")}
								</p>
							</RadioGroup>
							<p>{m("sourcePolicy")}</p>
							<div className={styles.objectActions}>
								<Button
									onPress={() => {
										if (!name.trim()) {
											notify("nameRequired");
											return;
										}
										if (!mode) {
											notify("modeRequired");
											return;
										}
										if (
											overlapsAnyDemoPath(path, [
												workDirectory,
												toolsPath,
												currentOutput,
											])
										) {
											notify("pathOverlap");
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
										notify("sourceAdded");
										// Registration only opens the root. Registering never
										// scans, processes, or invents scan results.
										window.location.hash = `sources/${encodeURIComponent(id)}`;
									}}
								>
									{m("addSource")}
								</Button>
								<Button onPress={() => setAdding(false)}>{m("cancel")}</Button>
							</div>
						</section>
					)}
					<p className="my-2 text-xs text-[var(--muted)]">
						{m("sourceTableHelp")}
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
			{picker && (
				<DirectoryPicker
					initialPath={path}
					onClose={() => setPicker(false)}
					onChoose={(value) => {
						setPath(value);
						setPicker(false);
					}}
				/>
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
