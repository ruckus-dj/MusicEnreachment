import { useEffect, useState } from "react";
import { Button, Checkbox, Dialog, Heading, Modal, StatusBadge } from "./ui";
import { formStyles as styles } from "./formStyles";
import {
	originalReleases,
	originalFiles,
	recoveryFixture,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";

type DeletionState = "queued" | "failed" | "stale" | "succeeded";
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
	files = originalFiles,
}: {
	releases: OriginalRelease[];
	deletions: DeletionOperation[];
	onRecover: (snapshot: RecoveredSnapshot) => boolean | void;
	onCompleteDeletion?: (operation: DeletionOperation) => boolean;
	onDismissDeletion?: (operation: DeletionOperation) => void;
	initialState?: OperationsDemoState;
	onState?: (state: OperationsDemoState) => void;
	files?: SourceFiles;
}) {
	const [open, setOpen] = useState(false);
	const [notice, setNotice] = useState("");
	const [retryState, setRetryState] = useState(initialState.retryState);
	const [dismissedOperations, setDismissedOperations] = useState(
		initialState.dismissedOperations,
	);
	const [recoveryRetryQueued, setRecoveryRetryQueued] = useState(
		initialState.recoveryRetryQueued ?? false,
	);
	useEffect(() => {
		onState?.({ retryState, dismissedOperations, recoveryRetryQueued });
	}, [retryState, dismissedOperations, recoveryRetryQueued, onState]);
	const [validationFailed, setValidationFailed] = useState(false);
	const [deletionStates, setDeletionStates] = useState(
		() => new Map<DeletionOperation, DeletionState>(),
	);
	const track: OriginalTrack | undefined = releases
		.flatMap((item) => item.tracks)
		.find((item) => item.id === recoveryFixture.trackId);
	const setDeletionState = (
		operation: DeletionOperation,
		state: DeletionState,
	) => setDeletionStates((previous) => new Map(previous).set(operation, state));
	const dismissOperation = (id: string) =>
		setDismissedOperations((previous) => new Set(previous).add(id));
	const baselinePublished = originalReleases
		.flatMap((release) => release.tracks)
		.find((item) => item.id === recoveryFixture.trackId)?.published;
	const publicationForRecovery = baselinePublished;
	const recoveredSnapshot: RecoveredSnapshot | null = publicationForRecovery
		? {
				...structuredClone(publicationForRecovery),
				path: recoveryFixture.targetPath,
			}
		: null;
	return (
		<section className={styles.page}>
			<header className={styles.pageHeader}>
				<div>
					<h1 className={styles.pageTitle}>Текущие операции</h1>
				</div>
				<p className={styles.pageDescription}>
					Незавершённая работа; завершённой истории здесь нет.
				</p>
			</header>
			{notice && (
				<p className={styles.formNotice} role="status" aria-live="polite">
					{notice}
				</p>
			)}
			<section className={styles.panel}>
				<div className={styles.panelHeading}>
					<h2>Требует внимания</h2>
					<StatusBadge tone="error">
						{Number(
							!dismissedOperations.has("signals") && retryState !== "succeeded",
						) +
							Number(
								!track?.recovered && !dismissedOperations.has("recovery"),
							)}{" "}
						текущие операции
					</StatusBadge>
				</div>
				<div className={styles.tableWrap}>
					<table className={styles.table}>
						<thead>
							<tr>
								{[
									"Объект",
									"Операция",
									"Фаза",
									"Причина",
									"Последствие",
									"Действие",
								].map((label) => (
									<th scope="col" key={label}>
										{label}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{!dismissedOperations.has("signals") &&
								retryState !== "succeeded" && (
									<tr>
										<td>Signals · позиция 06</td>
										<td>Публикация набора позиций</td>
										<td>
											{retryState === "queued"
												? "Повтор в очереди · демо"
												: "Ошибка"}
										</td>
										<td className={styles.operationError}>
											{retryState === "queued"
												? "Ожидает демонстрационного выполнения"
												: "EACCES: нет записи во временном каталоге"}
										</td>
										<td className={styles.operationActions}>
											Обычный повтор полностью подготавливает результат заново;
											он не является принудительной перепубликацией.
										</td>
										<td>
											{retryState === "queued" ? (
												<Button
													onPress={() => {
														setRetryState("succeeded");
														setNotice(
															"Демо: повтор завершился; текущая ошибка удалена из списка без сохранения истории.",
														);
													}}
												>
													Завершить повтор · демо
												</Button>
											) : (
												<>
													<Button
														onPress={() => {
															setRetryState("queued");
															setNotice(
																"Демо: обычный повтор поставлен в очередь; он заново подготавливает результат и не меняет публикацию до успеха.",
															);
														}}
													>
														Повторить
													</Button>
													<Button onPress={() => dismissOperation("signals")}>
														Скрыть ошибку · демо
													</Button>
												</>
											)}
										</td>
									</tr>
								)}
							{!track?.recovered && !dismissedOperations.has("recovery") && (
								<tr>
									<td>Northern Lines · позиция 01</td>
									<td>Прерванная публикация</td>
									<td>
										{recoveryRetryQueued
											? "Полный повтор в очереди · демо"
											: "Результат не зарегистрирован"}
									</td>
									<td className={styles.operationError}>
										Известный целевой файл установлен, но запись результата
										отсутствует
									</td>
									<td className={styles.operationActions}>
										Проверить известный target; при несовпадении — полный
										повтор.
									</td>
									<td>
										{recoveryRetryQueued ? (
											<Button
												onPress={() => {
													const frozenSource = recoveredSnapshot?.sourceId
														? files[recoveredSnapshot.sourceId]
														: undefined;
													if (
														!frozenSource?.available ||
														!frozenSource.locations?.length ||
														!recoveredSnapshot
													) {
														setNotice(
															"Полный повтор невозможен: исходник зафиксированной операции недоступен. Восстановите доступ и повторите действие.",
														);
														return;
													}
													if (onRecover(recoveredSnapshot) === false) {
														setNotice(
															"Полный повтор отклонён: текущая область изменилась.",
														);
														return;
													}
													dismissOperation("recovery");
													setNotice(
														"Демо: полный повтор завершён; подготовка заново представлена тестовым результатом, файлы не читались и не записывались.",
													);
												}}
											>
												Завершить полный повтор · демо
											</Button>
										) : (
											<Button onPress={() => setOpen(true)}>
												Проверить результат
											</Button>
										)}
										<Button onPress={() => dismissOperation("recovery")}>
											Скрыть ошибку · демо
										</Button>
									</td>
								</tr>
							)}
						</tbody>
					</table>
				</div>
			</section>
			<section className={styles.panel}>
				<h2>Удаление из медиатеки · демонстрационные запросы</h2>
				<p>
					Очередь и результаты существуют только в памяти макета. Демо не
					удаляет файлы; успешное завершение меняет только согласованные
					сущности макета.
				</p>
				<div className={styles.tableWrap}>
					<table className={styles.table}>
						<thead>
							<tr>
								{[
									"Объект",
									"Операция",
									"Состояние",
									"Подтверждённая область",
									"Файлы / публикации",
									"Результат",
								].map((label) => (
									<th scope="col" key={label}>
										{label}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{deletions.map((operation, index) => {
								const state = deletionStates.get(operation) ?? "queued";
								const currentOperation = operation;
								const execute = () => {
									const completed =
										onCompleteDeletion?.(currentOperation) ?? false;
									setDeletionState(
										operation,
										completed ? "succeeded" : "stale",
									);
									setNotice(
										completed
											? "Демо: удалена только подтверждённая область в памяти макета; физические файлы не тронуты."
											: "Удаление остановлено: подтверждённая область устарела или обработчик не подключён. Повторно подтвердите актуальную область.",
									);
								};
								if (state === "succeeded") return null;
								return (
									<tr key={`${operation.target}:${index}`}>
										<td>{operation.target}</td>
										<td>
											{operation.kind === "publication"
												? "Удаление публикаций"
												: operation.kind === "artist"
													? "Удаление артиста"
													: "Удаление медиатеки"}
										</td>
										<td>
											{state === "stale"
												? "Область устарела"
												: state === "failed"
													? "Ошибка · демо"
													: "В очереди · демо"}
										</td>
										<td>{operation.scope}</td>
										<td>
											{operation.publications} строк публикаций;{" "}
											{operation.paths.length} принадлежащих приложению файлов в
											подтверждённой области
										</td>
										<td>
											{state === "queued" && (
												<Button onPress={execute}>
													Выполнить удаление · демо
												</Button>
											)}
											{state !== "queued" && (
												<Button onPress={execute}>
													Повторить выполнение · демо
												</Button>
											)}
											{state === "queued" && (
												<Button
													onPress={() => setDeletionState(operation, "failed")}
												>
													Смоделировать отказ
												</Button>
											)}
											{state === "stale" && (
												<a href="#library">
													Вернуться в медиатеку и подтвердить удаление заново
												</a>
											)}
											{onDismissDeletion && (
												<Button
													onPress={() => onDismissDeletion(currentOperation)}
												>
													Скрыть · демо
												</Button>
											)}
										</td>
									</tr>
								);
							})}
							{deletions.length === 0 && (
								<tr>
									<td colSpan={6}>Нет текущих запросов.</td>
								</tr>
							)}
						</tbody>
					</table>
				</div>
			</section>
			<p>
				Восстановление проверяет известный target по тегам, техническим
				параметрам и актуальному разрешению; доказательство совпадения с
				исходником не требуется. Демо не читает реальный файл и не подтверждает
				filesystem-гарантии.
			</p>
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog aria-labelledby="recovery-title">
					<Heading slot="title" id="recovery-title">
						Проверить установленный результат?
					</Heading>
					<p>
						Известный зафиксированный путь:{" "}
						<code>{recoveryFixture.targetPath}</code>
					</p>
					<p>
						По текущему fixture совпадение tags:{" "}
						{recoveryFixture.tagsMatch ? "да" : "нет"}; технических параметров:{" "}
						{recoveryFixture.technicalMatch ? "да" : "нет"}; разрешение
						актуально: {recoveryFixture.authorityCurrent ? "да" : "нет"}.
						Физический файл не проверяется в макете.
					</p>
					<Checkbox
						isSelected={validationFailed}
						onChange={setValidationFailed}
					>
						Смоделировать непрохождение проверки
					</Checkbox>
					{validationFailed && (
						<p role="alert">
							Проверка не пройдена: результат не регистрируется. Требуется явный
							полный повтор подготовки из выбранного источника.
						</p>
					)}
					<Button onPress={() => setOpen(false)}>Отмена</Button>
					{validationFailed ? (
						<Button
							onPress={() => {
								setOpen(false);
								setRecoveryRetryQueued(true);
								setNotice(
									"Демо: запрошен полный повтор из выбранного исходника; восстановления регистрации не было.",
								);
							}}
						>
							Полный повтор · демо
						</Button>
					) : (
						<Button
							onPress={() => {
								if (
									!recoveredSnapshot ||
									!recoveryFixture.tagsMatch ||
									!recoveryFixture.technicalMatch ||
									!recoveryFixture.authorityCurrent
								) {
									setNotice(
										"Проверка восстановления не пройдена; нужен обычный полный повтор.",
									);
								} else {
									const recovered = onRecover(recoveredSnapshot);
									if (recovered !== false) dismissOperation("recovery");
									setNotice(
										recovered !== false
											? "Демо: результат зарегистрирован по проверенному snapshot; история не сохраняется."
											: "Восстановление отклонено: текущий объект несовместим с проверенным результатом.",
									);
								}
								setOpen(false);
							}}
						>
							Зарегистрировать результат
						</Button>
					)}
				</Dialog>
			</Modal>
		</section>
	);
}
