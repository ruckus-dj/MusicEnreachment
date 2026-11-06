import { useEffect, useState } from "react";
import { useI18n } from "./i18n";
import { Button, Dialog, Heading, Modal, StatusBadge } from "./ui";
import { formStyles as styles } from "./formStyles";
import {
	originalReleases,
	recoveryFixture,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import {
	operationMessage as message,
	type OperationMessage,
} from "./operationMessages";
import type { ManagedPublicationDeletionResult } from "./managedPublicationDeletionModel";

type DeletionState = "queued" | "stale" | "retry" | "conflict" | "succeeded";
type RecoveredSnapshot = NonNullable<OriginalTrack["published"]>;
export interface OperationsDemoState {
	retryState: "failed" | "queued" | "succeeded";
	dismissedOperations: Set<string>;
	recoveryRetryQueued?: boolean;
}
export const initialOperationsDemoState: OperationsDemoState = {
	retryState: "failed",
	dismissedOperations: new Set(),
};

export function OperationsScreen({
	releases,
	deletions,
	onRecover,
	onCompleteDeletion,
	onDismissDeletion,
	initialState = initialOperationsDemoState,
	onState,
}: {
	releases: OriginalRelease[];
	deletions: DeletionOperation[];
	onRecover: (snapshot: RecoveredSnapshot) => boolean | void;
	onCompleteDeletion?: (
		operation: DeletionOperation,
	) => boolean | ManagedPublicationDeletionResult;
	onDismissDeletion?: (operation: DeletionOperation) => void;
	initialState?: OperationsDemoState;
	onState?: (state: OperationsDemoState) => void;
	files?: SourceFiles;
}) {
	const { locale } = useI18n();
	const [open, setOpen] = useState(false);
	const [notice, setNotice] = useState<OperationMessage | "">("");
	const [retryState, setRetryState] = useState(initialState.retryState);
	const [dismissedOperations, setDismissedOperations] = useState(
		initialState.dismissedOperations,
	);
	const [deletionStates, setDeletionStates] = useState(
		() => new Map<DeletionOperation, DeletionState>(),
	);
	useEffect(() => {
		onState?.({
			retryState,
			dismissedOperations,
			recoveryRetryQueued: initialState.recoveryRetryQueued ?? false,
		});
	}, [
		retryState,
		dismissedOperations,
		initialState.recoveryRetryQueued,
		onState,
	]);

	const track = releases
		.flatMap((release) => release.tracks)
		.find((item) => item.id === recoveryFixture.trackId);
	const frozenPublished = originalReleases
		.flatMap((release) => release.tracks)
		.find((item) => item.id === recoveryFixture.trackId)?.published;
	const recoveredSnapshot: RecoveredSnapshot | null = frozenPublished
		? { ...structuredClone(frozenPublished), path: recoveryFixture.targetPath }
		: null;
	const signalVisible =
		!dismissedOperations.has("signals") && retryState !== "succeeded";
	const recoveryVisible =
		!track?.recovered && !dismissedOperations.has("recovery");
	const setDeletionState = (
		operation: DeletionOperation,
		state: DeletionState,
	) => setDeletionStates((previous) => new Map(previous).set(operation, state));
	const dismissOperation = (id: string) =>
		setDismissedOperations((previous) => new Set(previous).add(id));

	return (
		<section className={styles.page}>
			<header className={styles.pageHeader}>
				<h1 className={styles.pageTitle}>{message(locale, "title")}</h1>
			</header>
			{notice && (
				<p className={styles.formNotice} role="status" aria-live="polite">
					{message(locale, notice)}
				</p>
			)}
			<section className={styles.panel}>
				<div className={styles.panelHeading}>
					<h2>{message(locale, "attention")}</h2>
					<StatusBadge tone="error">
						{Number(signalVisible) + Number(recoveryVisible)}{" "}
						{message(locale, "currentOperations")}
					</StatusBadge>
				</div>
				<div className={styles.tableWrap}>
					<table className={styles.table}>
						<thead>
							<tr>
								{(
									[
										"object",
										"operation",
										"phase",
										"reason",
										"consequence",
										"action",
									] as const
								).map((key) => (
									<th scope="col" key={key}>
										{message(locale, key)}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{signalVisible && (
								<tr>
									<td>{message(locale, "signalsTrack")}</td>
									<td>{message(locale, "publishPositions")}</td>
									<td>
										{retryState === "queued"
											? message(locale, "retryQueued")
											: message(locale, "failed")}
									</td>
									<td className={styles.operationError}>
										{message(locale, "writeAccessFailure")}
									</td>
									<td className={styles.operationActions}>
										{message(locale, "prepareAgain")}
									</td>
									<td>
										<Button
											onPress={() => {
												setRetryState("succeeded");
												setNotice("retrySucceeded");
											}}
										>
											{message(locale, "retry")}
										</Button>
										<Button
											onPress={() => {
												dismissOperation("signals");
												setNotice("dismissed");
											}}
										>
											{message(locale, "dismiss")}
										</Button>
									</td>
								</tr>
							)}
							{recoveryVisible && (
								<tr>
									<td>{message(locale, "recoveryTrack")}</td>
									<td>{message(locale, "interruptedPublication")}</td>
									<td>{message(locale, "resultNotRegistered")}</td>
									<td className={styles.operationError}>
										{message(locale, "knownTargetMissingResult")}
									</td>
									<td className={styles.operationActions}>
										{message(locale, "checkKnownTarget")}
									</td>
									<td>
										<Button onPress={() => setOpen(true)}>
											{message(locale, "checkResult")}
										</Button>
										<Button
											onPress={() => {
												dismissOperation("recovery");
												setNotice("dismissed");
											}}
										>
											{message(locale, "dismiss")}
										</Button>
									</td>
								</tr>
							)}
							{!signalVisible && !recoveryVisible && (
								<tr>
									<td colSpan={6}>{message(locale, "noCurrentOperations")}</td>
								</tr>
							)}
						</tbody>
					</table>
				</div>
			</section>
			<section className={styles.panel}>
				<h2>{message(locale, "deletionTitle")}</h2>
				<p>{message(locale, "deletionDescription")}</p>
				<div className={styles.tableWrap}>
					<table className={styles.table}>
						<thead>
							<tr>
								{(
									[
										"object",
										"operation",
										"state",
										"confirmedScope",
										"filesAndPublications",
										"result",
									] as const
								).map((key) => (
									<th scope="col" key={key}>
										{message(locale, key)}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{deletions.map((operation, index) => {
								const state = deletionStates.get(operation) ?? "queued";
								if (state === "succeeded") return null;
								const execute = () => {
									const result = onCompleteDeletion?.(operation) ?? false;
									const nextState: DeletionState =
										typeof result === "string"
											? result === "completed"
												? "succeeded"
												: result
											: result
												? "succeeded"
												: "stale";
									setDeletionState(operation, nextState);
									setNotice(
										nextState === "succeeded"
											? "removalSucceeded"
											: nextState === "retry"
												? "removalRetry"
												: nextState === "conflict"
													? "removalConflict"
													: "removalStale",
									);
								};
								const kind =
									operation.kind === "publication"
										? "deleteKindPublication"
										: operation.kind === "artist"
											? "deleteKindArtist"
											: "deleteKindLibrary";
								const stateMessage =
									state === "queued"
										? "queued"
										: state === "stale"
											? "staleScope"
											: state === "retry"
												? "removalStateRetry"
												: "removalStateConflict";
								return (
									<tr key={`${operation.target}:${index}`}>
										<td>
											{operation.target || message(locale, "unknownTarget")}
										</td>
										<td>{message(locale, kind)}</td>
										<td>{message(locale, stateMessage)}</td>
										<td>{operation.scope}</td>
										<td>
											{message(locale, "publicationCount", {
												count: operation.publications,
												files: operation.paths.length,
											})}
										</td>
										<td>
											<Button onPress={execute}>
												{state === "queued"
													? message(locale, "completeRemoval")
													: message(locale, "retryRemoval")}
											</Button>
											{state === "stale" && (
												<a href="#library">
													{message(locale, "returnAndConfirm")}
												</a>
											)}
											{state === "conflict" && (
												<a href="#library">
													{message(locale, "returnAndConfirm")}
												</a>
											)}
											{onDismissDeletion && (
												<Button onPress={() => onDismissDeletion(operation)}>
													{message(locale, "dismiss")}
												</Button>
											)}
										</td>
									</tr>
								);
							})}
							{deletions.every(
								(operation) => deletionStates.get(operation) === "succeeded",
							) && (
								<tr>
									<td colSpan={6}>{message(locale, "currentRequestsEmpty")}</td>
								</tr>
							)}
						</tbody>
					</table>
				</div>
			</section>
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog aria-labelledby="recovery-title">
					<Heading slot="title" id="recovery-title">
						{message(locale, "checkResult")}
					</Heading>
					<p>
						{message(locale, "checkSnapshot")}
						<code>{recoveryFixture.targetPath}</code>
					</p>
					<ul>
						<li>
							{message(locale, "tagsMatch")}:{" "}
							{message(locale, recoveryFixture.tagsMatch ? "yes" : "no")}
						</li>
						<li>
							{message(locale, "technicalMatch")}:{" "}
							{message(locale, recoveryFixture.technicalMatch ? "yes" : "no")}
						</li>
						<li>
							{message(locale, "authorityCurrent")}:{" "}
							{message(locale, recoveryFixture.authorityCurrent ? "yes" : "no")}
						</li>
					</ul>
					<div className="mt-4 flex flex-wrap justify-end gap-2">
						<Button onPress={() => setOpen(false)}>
							{message(locale, "cancel")}
						</Button>
						<Button
							onPress={() => {
								const valid =
									recoveredSnapshot &&
									recoveryFixture.tagsMatch &&
									recoveryFixture.technicalMatch &&
									recoveryFixture.authorityCurrent;
								if (!valid) setNotice("recoveryFailed");
								else if (onRecover(recoveredSnapshot) === false)
									setNotice("recoveryRejected");
								else {
									dismissOperation("recovery");
									setNotice("recoveryRegistered");
								}
								setOpen(false);
							}}
						>
							{message(locale, "confirmResult")}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</section>
	);
}
