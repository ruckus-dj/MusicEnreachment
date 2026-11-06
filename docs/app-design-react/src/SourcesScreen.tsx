import { useEffect, useState } from "react";
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
	Radio,
	RadioGroup,
	Select,
	SelectValue,
	TextField,
} from "./ui";
import { DirectoryPicker } from "./DirectoryPicker";
import type { SourceFiles } from "./catalogData";
import {
	blockStagedSourceQueue,
	canRemoveSourceRoot,
	createSourceScan,
	eligibleSourceCopies,
	initialSourceProcessingState,
	processSourceQueue,
	removeEligibleCopy,
	removeRootProcessingState,
	retrySourceStep,
	retryCancelledSourceFile,
	type SourceProcessingState,
} from "./sourceProcessingModel";
import { rootLocations } from "./sourceInventory";
import { formStyles as styles } from "./formStyles";
import { useI18n } from "./i18n";
import { sourceMessage, type SourceMessageKey } from "./sourceMessages";
import { overlapsAnyDemoPath } from "./demoPathValidation";

type Mode = "in_place" | "staged";
export interface SourceRoot {
	id: string;
	name: string;
	path: string;
	mode: Mode;
	available: boolean | null;
	checked: string;
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

export function SourcesScreen({
	roots,
	onRoots,
	files,
	removed,
	onRemove,
	workDirectory = "",
	toolsPath = "",
	currentOutput = "",
	initialProcessingState = initialSourceProcessingState,
	onProcessingState,
}: {
	roots: SourceRoot[];
	onRoots: (roots: SourceRoot[]) => void;
	files: SourceFiles;
	removed: Set<string>;
	onRemove: (rootId: string) => void;
	workDirectory?: string;
	toolsPath?: string;
	currentOutput?: string;
	initialProcessingState?: SourceProcessingState;
	onProcessingState?: (state: SourceProcessingState) => void;
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
	const [processing, setProcessing] = useState(initialProcessingState);
	const [removal, setRemoval] = useState<RootRemoval | null>(null);

	useEffect(
		() => onProcessingState?.(processing),
		[onProcessingState, processing],
	);
	useEffect(() => {
		if (!workDirectory) return;
		setProcessing((current) => {
			if (
				!current.files.some(
					(file) => file.blockedReason === "work_directory_required",
				)
			)
				return current;
			return processSourceQueue(current);
		});
	}, [workDirectory]);

	const updateProcessing = (next: SourceProcessingState) => setProcessing(next);
	const hasActiveRootWork = (rootId: string) =>
		!canRemoveSourceRoot(processing, rootId);
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
	const scanAndProcess = (root: SourceRoot) => {
		if (root.available !== true) return;
		const scanned = createSourceScan(processing, root.id, root.mode, files);
		const queued = scanned.files.filter(
			(file) =>
				file.rootId === root.id &&
				file.status === "queued" &&
				file.steps.some((step) => step.status === "queued"),
		);
		if (root.mode === "staged" && !workDirectory) {
			updateProcessing(blockStagedSourceQueue(scanned, root.id));
			notify("workDirectoryRequired");
			return;
		}
		updateProcessing(processSourceQueue(scanned, root.id));
		if (queued.length)
			notify("scanStarted", { path: root.path, count: queued.length });
		else notify("noInventory");
	};

	return (
		<section className={styles.page}>
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
			{notice && (
				<p className={styles.formNotice} role="status">
					{m(notice.key, notice.values)}
				</p>
			)}
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
					<Button onPress={() => setPicker(true)}>{m("chooseFolder")}</Button>
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
								onRoots([
									...roots,
									{
										id: `root-${crypto.randomUUID()}`,
										name: name.trim(),
										path,
										mode,
										available: null,
										checked: "—",
									},
								]);
								setAdding(false);
								notify("sourceAdded");
							}}
						>
							{m("addSource")}
						</Button>
						<Button onPress={() => setAdding(false)}>{m("cancel")}</Button>
					</div>
				</section>
			)}
			<section className={styles.panel}>
				<h2>{m("connectedFolders")}</h2>
				<p className="my-2 text-xs text-[var(--muted)]">
					{m("sourceTableHelp")}
				</p>
				<div className={styles.tableWrap}>
					<table className={`${styles.table} table-fixed min-w-[1040px]`}>
						<colgroup>
							<col className="w-[14%]" />
							<col className="w-[26%]" />
							<col className="w-[22%]" />
							<col className="w-[12%]" />
							<col className="w-[12%]" />
							<col className="w-[14%]" />
						</colgroup>
						<thead>
							<tr>
								{[
									m("nameColumn"),
									m("pathColumn"),
									m("modeColumn"),
									m("availabilityColumn"),
									m("checkedColumn"),
									m("actionsColumn"),
								].map((label) => (
									<th key={label} scope="col" className="!whitespace-normal">
										{label}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{roots.map((root) => (
								<tr key={root.id}>
									<td>{root.name}</td>
									<td>
										<code className="block min-w-0 [overflow-wrap:anywhere]">
											{root.path}
										</code>
									</td>
									<td>
										<div className="grid min-w-0 gap-[5px] text-xs text-[var(--muted)]">
											<Select
												selectedKey={root.mode}
												aria-label={m("modeFor", { name: root.name })}
												onSelectionChange={(key) => {
													const next = String(key) as Mode;
													onRoots(
														roots.map((item) =>
															item.id === root.id
																? { ...item, mode: next }
																: item,
														),
													);
												}}
											>
												<Button>
													<SelectValue />
												</Button>
												<Popover>
													<ListBox>
														<ListBoxItem id="in_place">
															{m("inPlace")}
														</ListBoxItem>
														<ListBoxItem id="staged">{m("staged")}</ListBoxItem>
													</ListBox>
												</Popover>
											</Select>
										</div>
										{root.mode === "staged" && (
											<p>
												{m("workDirectory")}: {workDirectory || "—"} ·{" "}
												<a href="#settings">{m("configure")}</a>
											</p>
										)}
									</td>
									<td>
										{root.available === null
											? m("notChecked")
											: root.available
												? m("available")
												: m("unavailable")}
									</td>
									<td>{root.checked}</td>
									<td>
										<div className="flex flex-wrap items-center gap-2">
											{removed.has(root.id) ? (
												<span>{m("rootRemoved")}</span>
											) : (
												<>
													{root.available === true && (
														<a href="#review">{m("reviewGroups")}</a>
													)}
													<Button
														isDisabled={root.available !== true}
														onPress={() => scanAndProcess(root)}
													>
														{m("scan")}
													</Button>
													<Button
														aria-label={m("rootActionLabel", {
															name: root.name,
														})}
														onPress={() => requestRootRemoval(root)}
													>
														{m("removeRoot")}
													</Button>
													{root.available === false && (
														<p className="basis-full text-xs text-[var(--muted)]">
															{m("checkUnavailable")}
														</p>
													)}
												</>
											)}
										</div>
									</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			</section>
			<section
				className={styles.panel}
				aria-labelledby="source-processing-title"
			>
				<h2 id="source-processing-title">{m("processingTitle")}</h2>
				<p>{m("processingDescription")}</p>
				<p className="text-xs text-[var(--muted)]">{m("freshness")}</p>
				{processing.files.length === 0 ? (
					<p>{m("noFiles")}</p>
				) : (
					<ul className="m-0 grid gap-3 pl-5">
						{processing.files.map((file) => (
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
											: {statusLabel(step.status)}
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
				<h3>{m("retainedCopies")}</h3>
				<p>{m("copyOwnership")}</p>
				{processing.copies.length === 0 ? (
					<p>{m("noCopies")}</p>
				) : (
					<ul className="m-0 grid gap-2 pl-5">
						{processing.copies.map((copy) => {
							const eligible = eligibleSourceCopies(processing).some(
								(item) => item.id === copy.id,
							);
							return (
								<li key={copy.id}>
									{copy.fileId} ·{" "}
									{eligible ? m("copyEligible") : m("copyProtected")}
									{eligible && (
										<Button
											onPress={() => {
												updateProcessing(
													removeEligibleCopy(processing, copy.id),
												);
												notify("copyRemoved");
											}}
										>
											{m("removeCopy")}
										</Button>
									)}
								</li>
							);
						})}
					</ul>
				)}
			</section>
			<p>{m("unavailablePublications")}</p>
			<p>
				{m("workDirectory")}: {workDirectory || "—"} ·{" "}
				<a href="#settings">{m("configureInSettings")}</a>
			</p>
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
			<Modal
				isOpen={Boolean(removal)}
				onOpenChange={(open) => {
					if (!open) setRemoval(null);
				}}
				isDismissable
			>
				<Dialog aria-label={m("deleteDialogLabel")}>
					<Heading slot="title">
						{m("rootConfirmTitle", { path: removal?.path ?? "" })}
					</Heading>
					<p>
						<code>{removal?.path}</code>
						<br />
						{m("rootConfirmCount", { count: removal?.locations.length ?? 0 })}
					</p>
					<p>{m("rootConfirmScope")}</p>
					<p>{m("rootConfirmRecheck")}</p>
					<Button onPress={() => setRemoval(null)}>{m("cancel")}</Button>
					<Button
						onPress={() => {
							if (!removal) return;
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
							updateProcessing(
								removeRootProcessingState(processing, removal.id),
							);
							onRemove(removal.id);
							notify("rootRemoved");
							setRemoval(null);
						}}
					>
						{m("confirm")}
					</Button>
				</Dialog>
			</Modal>
		</section>
	);
}
