import {
	type Dispatch,
	type SetStateAction,
	useEffect,
	useRef,
	useState,
} from "react";
import { type Locale, useI18n } from "../../../app/i18n";
import { useViewFocus } from "../../../ui/components/useViewFocus";
import {
	type SourceRegistrationErrorKey,
	sourceRegistrationMessage,
	useSourceRegistrationFocus,
} from "../../../ui/features/sources/SourceRegistrationView";
import type { SourceRootListRow } from "../../../ui/features/sources/SourceRootList";
import type { SourceRootChange } from "../../../ui/features/sources/SourceRootSettingsView";
import {
	type SourceMessageKey,
	sourceMessage,
} from "../../../ui/features/sources/sourceInventoryMessages";
import { sourceMessage as sharedSourceMessage } from "../../../ui/features/sources/sourceMessages";
import {
	sourceDetailHref,
	sourceLocationHref,
	sourcesHref,
} from "../../../ui/features/sources/sourcesRoute";
import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "../../fixtures/directoryBrowseDemo";
import { incomingGroupsListHref } from "../incoming/incomingGroupsRoute";
import type { SourceFiles } from "../library/catalogData";
import { overlapsAnyDemoPath } from "./demoPathValidation";
import {
	type SourceArtifactCleanupDemoExecution,
	useSourceArtifactCleanupDemoProvider,
} from "./sourceArtifactCleanupDemoProvider";
import { sourceInspectorDemoProvider } from "./sourceInspectorDemoProvider";
import {
	inventoryPathStale,
	retainedInventoryLocations,
	rootLocations,
} from "./sourceInventory";
import {
	canRemoveSourceRoot,
	removeRootProcessingState,
	type SourceProcessingState,
} from "./sourceProcessingModel";
import { SourcesBinder, SourcesOutlet } from "./sourcesOutlet";
import type { SourceScanDemoProvider } from "./useSourceScanDemoProvider";

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

function countLabel(locale: Locale, count: number) {
	const language = locale === "ru" ? "ru-RU" : "en-US";
	const category = new Intl.PluralRules(language).select(count);
	const key =
		category === "one"
			? "fileCountOne"
			: locale !== "ru"
				? "fileCountOther"
				: category === "few"
					? "fileCountFew"
					: "fileCountMany";
	return sharedSourceMessage(locale, key, {
		count: new Intl.NumberFormat(language).format(count),
	});
}

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
	cleanupExecution,
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
	/** Host-owned execution so a running cleanup survives other routes. */
	cleanupExecution: SourceArtifactCleanupDemoExecution;
}) {
	const { locale } = useI18n();
	const m = (key: SourceMessageKey, values?: Record<string, string | number>) =>
		sourceMessage(locale, key, values);
	const [adding, setAdding] = useState(false);
	const rootHeading = useRef<HTMLHeadingElement>(null);
	useViewFocus(rootHeading, selectedLocationId ? undefined : selectedRootId);
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
	useEffect(() => {
		setRemoval((current) =>
			current?.id === selectedRootId && !selectedLocationId ? current : null,
		);
	}, [selectedRootId, selectedLocationId]);
	const root = selectedRootId
		? roots.find((item) => item.id === selectedRootId)
		: undefined;
	const demoScanState = root ? scans.states[root.id] : undefined;
	const inventoryLocations = root
		? retainedInventoryLocations(files, root)
		: [];
	const inventoryPath = root?.inventoryPath ?? root?.path;
	const inventoryPrefix = inventoryPath
		? `${inventoryPath.replace(/\/+$/, "")}/`
		: undefined;
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
		execution: cleanupExecution,
	});
	const hasActiveRootWork = (rootId: string) =>
		scans.active(rootId) ||
		cleanupExecution.blocksRoot(rootId) ||
		!canRemoveSourceRoot(processing, rootId);
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
		window.location.hash = sourceDetailHref(id);
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
		: sharedSourceMessage(locale, "neverScanned");
	const inventoryLabel = root?.lastSuccessfulScanAt
		? countLabel(locale, root.inventoryCount ?? 0)
		: sharedSourceMessage(locale, "neverScanned");

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
					label: countLabel(locale, item.inventoryCount ?? 0),
					notes: item.inventoryCount === 0 ? [m("noInventory")] : undefined,
				}
			: { label: sharedSourceMessage(locale, "neverScanned") },
		href: sourceDetailHref(item.id),
	}));

	const noticeMessage = notice ? m(notice.key, notice.values) : undefined;

	if (selectedRootId && selectedLocationId)
		return (
			<SourcesBinder
				key={`${selectedRootId}:${selectedLocationId}`}
				page={{
					view: "location",
					...sourceInspectorDemoProvider({
						locale,
						rootId: selectedRootId,
						locationId: selectedLocationId,
						root,
						files,
						processing,
						updateProcessing,
						workDirectory,
						scanning: scans.active(selectedRootId),
					}),
				}}
			>
				<SourcesOutlet />
			</SourcesBinder>
		);

	return (
		<>
			{selectedRootId ? (
				root ? (
					<SourcesBinder
						key={root.id}
						page={{
							view: "detail",
							detail: "root",
							locale,
							headingId: "source-title",
							heading: {
								title: root.name,
								path: root.path,
								headingId: "source-title",
								headingRef: rootHeading,
								sourcesHref: sourcesHref,
								incomingHref:
									root.available === true
										? incomingGroupsListHref()
										: undefined,
							},
							feedback: noticeMessage,
							scan: {
								state:
									demoScanState?.kind === "fixtureFailure"
										? { kind: "failed", message: m("scanFailedFixture") }
										: (demoScanState ?? { kind: "idle" }),
								onStart: () => startScan(root),
								onRetry: () => startScan(root),
							},
							inventory: {
								path: inventoryPath,
								unpublished: !root.lastSuccessfulScanAt,
								rows: inventoryLocations.map((location) => {
									const observed = rootFiles.find(
										(item) => item.id === `${root.id}:${location.id}`,
									);
									const step = observed?.steps.find(
										(item) => item.id === "audio_inspection",
									);
									return {
										id: location.id,
										path:
											inventoryPrefix &&
											location.path.startsWith(inventoryPrefix)
												? location.path.slice(inventoryPrefix.length)
												: location.path,
										href: sourceLocationHref(root.id, location.id),
										sizeBytes: location.size,
										modifiedAt: location.mtime,
										status:
											step?.status === "succeeded"
												? "audio"
												: step?.status === "failed"
													? "probe_error"
													: "not_analyzed",
									};
								}),
							},
							overview: {
								name: root.name,
								path: root.path,
								mode: root.mode,
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
								},
								onRemove: () => requestRootRemoval(root),
							},
							settings: {
								name: root.name,
								path: root.path,
								mode: root.mode,
								browse: browseDemoDirectory,
								resolveInitialPath: demoDirectoryStart,
								error:
									settingsError?.rootId === root.id
										? settingsError.key === "pathOverlap"
											? sourceRegistrationMessage(locale, "pathOverlap")
											: m(settingsError.key)
										: undefined,
								onSave: saveRoot,
							},
							removal:
								removal?.id === root.id
									? {
											path: removal.path,
											count: removal.locations.length,
											onCancel: () => setRemoval(null),
											onConfirm: () => {
												if (hasActiveRootWork(removal.id)) {
													notify("rootActive");
													setRemoval(null);
													return;
												}
												const currentLocations = rootLocations(
													files,
													removal.id,
												)
													.map((location) => location.id)
													.sort();
												if (
													currentLocations.join("|") !==
													removal.locations.join("|")
												) {
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
												window.location.hash = sourcesHref;
											},
										}
									: undefined,
						}}
					>
						<SourcesOutlet />
					</SourcesBinder>
				) : (
					<SourcesBinder
						page={{
							view: "detail",
							detail: "missing",
							title: m("sourceNotFound"),
							id: selectedRootId,
							back: { href: sourcesHref, label: m("backToSources") },
						}}
					>
						<SourcesOutlet />
					</SourcesBinder>
				)
			) : (
				<SourcesBinder
					page={{
						view: "list",
						locale,
						heading: {
							registration: adding
								? undefined
								: {
										id: registrationFocus.triggerId,
										onPress: () => {
											setAdding(true);
											setName("");
											setPath("/mnt/music");
											setMode("");
											setRegistrationError(null);
										},
									},
						},
						registration: adding
							? {
									name,
									path,
									mode,
									error: registrationError
										? sourceRegistrationMessage(locale, registrationError)
										: undefined,
									browse: browseDemoDirectory,
									resolveInitialPath: demoDirectoryStart,
									onNameChange: setName,
									onPathChange: setPath,
									onModeChange: setMode,
									onSubmit: register,
									onCancel: () => {
										registrationFocus.restoreOnClose();
										setAdding(false);
										setRegistrationError(null);
									},
								}
							: undefined,
						feedback: noticeMessage,
						rows,
						onScan: (id) => {
							const source = roots.find((item) => item.id === id);
							if (source) startScan(source);
						},
						loaded: true,
						cleanup,
					}}
				>
					<SourcesOutlet />
				</SourcesBinder>
			)}
		</>
	);
}
