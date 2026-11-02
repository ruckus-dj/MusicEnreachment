import { useState } from "react";
import type { CatalogArtist } from "./catalogData";
import {
	createManualReleaseDemoDraft,
	emptyManualMedium,
	emptyManualPosition,
	manualCovers,
	manualRecordings,
	manualSources,
	type ManualArtistDraft,
	type ManualMediumDraft,
	type ManualPositionDraft,
	type ManualReleaseDraft,
} from "./manualReleaseModel";

type ListKind = "artists" | "media" | "positions";

export function ManualReleaseScreen({
	draft,
	artists,
	onDraft,
	onCreate,
	onReset,
}: {
	draft: ManualReleaseDraft;
	artists: CatalogArtist[];
	onDraft: (draft: ManualReleaseDraft) => void;
	onCreate: () => void;
	onReset: () => void;
}) {
	const [replaceTarget, setReplaceTarget] = useState<{
		medium: number;
		position: number;
		sourceId: string;
	} | null>(null);
	const update = (next: ManualReleaseDraft) => onDraft(next);
	const updateArtist = (index: number, values: Partial<ManualArtistDraft>) =>
		update({
			...draft,
			artists: draft.artists.map((artist, item) =>
				item === index ? { ...artist, ...values } : artist,
			),
		});
	const updateMedium = (index: number, values: Partial<ManualMediumDraft>) =>
		update({
			...draft,
			media: draft.media.map((medium, item) =>
				item === index ? { ...medium, ...values } : medium,
			),
		});
	const updatePosition = (
		mediumIndex: number,
		positionIndex: number,
		values: Partial<ManualPositionDraft>,
	) =>
		updateMedium(mediumIndex, {
			positions: draft.media[mediumIndex].positions.map((position, item) =>
				item === positionIndex ? { ...position, ...values } : position,
			),
		});
	const move = (
		kind: ListKind,
		mediumIndex: number,
		index: number,
		direction: -1 | 1,
	) => {
		const values =
			kind === "artists"
				? [...draft.artists]
				: kind === "media"
					? [...draft.media]
					: [...draft.media[mediumIndex].positions];
		const target = index + direction;
		if (target < 0 || target >= values.length) return;
		[values[index], values[target]] = [values[target], values[index]];
		if (kind === "artists")
			update({ ...draft, artists: values as ManualArtistDraft[] });
		else if (kind === "media")
			update({ ...draft, media: values as ManualMediumDraft[] });
		else
			updateMedium(mediumIndex, { positions: values as ManualPositionDraft[] });
	};
	const remove = (kind: ListKind, mediumIndex: number, index: number) => {
		if (kind === "artists")
			update({
				...draft,
				artists: draft.artists.filter((_, item) => item !== index),
			});
		else if (kind === "media")
			update({
				...draft,
				media: draft.media.filter((_, item) => item !== index),
			});
		else
			updateMedium(mediumIndex, {
				positions: draft.media[mediumIndex].positions.filter(
					(_, item) => item !== index,
				),
			});
	};
	const actions = (
		kind: ListKind,
		mediumIndex: number,
		index: number,
		length: number,
		label: string,
	) => (
		<div className="demo-object-actions">
			<button
				type="button"
				aria-label={`Переместить ${label} выше`}
				disabled={index === 0}
				onClick={() => move(kind, mediumIndex, index, -1)}
			>
				↑
			</button>
			<button
				type="button"
				aria-label={`Переместить ${label} ниже`}
				disabled={index === length - 1}
				onClick={() => move(kind, mediumIndex, index, 1)}
			>
				↓
			</button>
			<button type="button" onClick={() => remove(kind, mediumIndex, index)}>
				Удалить
			</button>
		</div>
	);
	const sourceCount = draft.media.reduce(
		(count, medium) =>
			count +
			medium.positions.reduce(
				(sum, position) => sum + position.sourceIds.length,
				0,
			),
		0,
	);
	const selectedCover = manualCovers.find(
		(cover) => cover.id === draft.coverId,
	);

	return (
		<section aria-labelledby="manual-release-title">
			<header className="demo-object-header">
				<div>
					<h1 id="manual-release-title">Создать локальный релиз</h1>
					<p>
						Интерактивный прототип на демонстрационных данных. Изменения
						остаются в памяти этой вкладки.
					</p>
				</div>
			</header>
			<div className="demo-panel">
				<strong>Только демо.</strong> Создание добавляет релиз в текущую
				медиатеку в памяти вкладки. Никаких запросов, сохранения на сервере или
				публикации нет.
			</div>
			<section className="demo-panel" aria-labelledby="manual-release-meta">
				<div className="panel-heading">
					<h2 id="manual-release-meta">Релиз</h2>
					<span>Поля необязательны; неполный релиз допустим</span>
				</div>
				<div className="demo-object-columns">
					<Field
						label="Название релиза"
						value={draft.title}
						onChange={(title) => update({ ...draft, title })}
					/>
					<Field
						label="Дата релиза"
						value={draft.metadata.date}
						onChange={(date) =>
							update({ ...draft, metadata: { ...draft.metadata, date } })
						}
					/>
					<Field
						label="Страна"
						value={draft.metadata.country}
						onChange={(country) =>
							update({ ...draft, metadata: { ...draft.metadata, country } })
						}
					/>
					<Field
						label="Лейбл"
						value={draft.metadata.label}
						onChange={(label) =>
							update({ ...draft, metadata: { ...draft.metadata, label } })
						}
					/>
					<Field
						label="Номер каталога"
						value={draft.metadata.catalogNumber}
						onChange={(catalogNumber) =>
							update({
								...draft,
								metadata: { ...draft.metadata, catalogNumber },
							})
						}
					/>
					<Field
						label="Тип / уточнение"
						value={draft.metadata.type}
						onChange={(type) =>
							update({ ...draft, metadata: { ...draft.metadata, type } })
						}
					/>
				</div>
			</section>
			<section className="demo-panel">
				<div className="panel-heading">
					<div>
						<h2>Артисты релиза</h2>
						<span>
							Выберите существующего артиста или укажите новое имя; авторство
							можно уточнить.
						</span>
					</div>
					<button
						type="button"
						onClick={() =>
							update({
								...draft,
								artists: [
									...draft.artists,
									{ name: "", credit: "", role: "", artistId: null },
								],
							})
						}
					>
						Добавить авторство
					</button>
				</div>
				{draft.artists.map((artist, index) => {
					const matched = artists.find(
						(item) =>
							item.name.toLocaleLowerCase() ===
							artist.name.trim().toLocaleLowerCase(),
					);
					return (
						<div className="demo-panel" key={`artist-${index}`}>
							<div className="panel-heading">
								<h3>Артист {index + 1}</h3>
								{actions("artists", -1, index, draft.artists.length, "артиста")}
							</div>
							<div className="demo-object-columns">
								<label>
									Артист (поиск / новое имя)
									<input
										list="manual-artist-options"
										value={artist.name}
										placeholder="Начните вводить имя"
										autoComplete="off"
										onChange={(event) => {
											const name = event.currentTarget.value;
											const exact = artists.find(
												(item) =>
													item.name.toLocaleLowerCase() ===
													name.trim().toLocaleLowerCase(),
											);
											updateArtist(index, {
												name,
												artistId: exact?.id ?? null,
												credit:
													exact && !artist.credit ? exact.name : artist.credit,
											});
										}}
									/>
								</label>
								<Field
									label="Отображаемое имя"
									value={artist.credit}
									onChange={(credit) => updateArtist(index, { credit })}
									placeholder="Имя в этом релизе"
								/>
								<Field
									label="Уточнение роли (если нужно)"
									value={artist.role}
									onChange={(role) => updateArtist(index, { role })}
									placeholder="Описательный текст"
								/>
							</div>
							<p className="demo-secondary">
								{matched
									? `Выбран существующий артист: ${matched.name} · демонстрационный каталог`
									: artist.name
										? "Новое имя артиста · будет частью только этого демо-релиза"
										: "Выберите артиста или введите новое имя"}
							</p>
							<p className="demo-secondary">
								Это описание — метаданные, не отдельная связь с артистом. Имена
								композиторов/авторов текста в других метаданных не добавляются в
								состав артистов.
							</p>
						</div>
					);
				})}
				<datalist id="manual-artist-options">
					{artists.map((artist) => (
						<option key={artist.id} value={artist.name} />
					))}
				</datalist>
				{!draft.artists.length && <p>Артисты не добавлены.</p>}
			</section>
			<section className="demo-panel">
				<div className="panel-heading">
					<div>
						<h2>Носители и позиции</h2>
						<span>Порядок носителей и позиций изменяется независимо.</span>
					</div>
					<button
						type="button"
						onClick={() =>
							update({ ...draft, media: [...draft.media, emptyManualMedium()] })
						}
					>
						Добавить носитель
					</button>
				</div>
				{draft.media.map((medium, mediumIndex) => (
					<div className="demo-panel" key={`medium-${mediumIndex}`}>
						<div className="panel-heading">
							<h3>Носитель {mediumIndex + 1}</h3>
							{actions(
								"media",
								-1,
								mediumIndex,
								draft.media.length,
								"носитель",
							)}
						</div>
						<div className="demo-object-columns">
							<Field
								label="Название носителя (если нужно)"
								value={medium.title}
								onChange={(title) => updateMedium(mediumIndex, { title })}
							/>
							<Field
								label="Тип / формат (если известно)"
								value={medium.format}
								onChange={(format) => updateMedium(mediumIndex, { format })}
							/>
						</div>
						<div className="panel-heading">
							<h3>Позиции</h3>
							<button
								type="button"
								onClick={() =>
									updateMedium(mediumIndex, {
										positions: [...medium.positions, emptyManualPosition()],
									})
								}
							>
								Добавить позицию
							</button>
						</div>
						{medium.positions.map((position, positionIndex) => (
							<div
								className="demo-panel"
								key={`position-${mediumIndex}-${positionIndex}`}
							>
								<div className="panel-heading">
									<h3>Позиция {positionIndex + 1}</h3>
									{actions(
										"positions",
										mediumIndex,
										positionIndex,
										medium.positions.length,
										"позицию",
									)}
								</div>
								<div className="demo-object-columns">
									<Field
										label="Отображаемый номер"
										value={position.number}
										onChange={(number) =>
											updatePosition(mediumIndex, positionIndex, { number })
										}
										placeholder="Например, A1"
									/>
									<Field
										label="Название в этом релизе"
										value={position.title}
										onChange={(title) =>
											updatePosition(mediumIndex, positionIndex, { title })
										}
									/>
									<Field
										label="Длительность (если известна)"
										value={position.duration}
										onChange={(duration) =>
											updatePosition(mediumIndex, positionIndex, { duration })
										}
										placeholder="Необязательно"
									/>
								</div>
								<fieldset>
									<legend>Композиция</legend>
									<label>
										<input
											type="radio"
											name={`recording-mode-${mediumIndex}-${positionIndex}`}
											checked={position.recordingMode === "new"}
											onChange={() =>
												updatePosition(mediumIndex, positionIndex, {
													recordingMode: "new",
												})
											}
										/>{" "}
										Новая композиция
									</label>
									<label>
										<input
											type="radio"
											name={`recording-mode-${mediumIndex}-${positionIndex}`}
											checked={position.recordingMode === "existing"}
											onChange={() =>
												updatePosition(mediumIndex, positionIndex, {
													recordingMode: "existing",
												})
											}
										/>{" "}
										Существующая композиция
									</label>
								</fieldset>
								{position.recordingMode === "new" ? (
									<div>
										<Field
											label="Название композиции"
											value={position.newRecordingTitle}
											onChange={(newRecordingTitle) =>
												updatePosition(mediumIndex, positionIndex, {
													newRecordingTitle,
												})
											}
											placeholder="Можно оставить пустым"
										/>
										<p className="demo-secondary">
											Новая композиция будет показана как черновая сущность, не
											сохранённая в приложении.
										</p>
									</div>
								) : (
									<RecordingPicker
										position={position}
										onChange={(values) =>
											updatePosition(mediumIndex, positionIndex, values)
										}
									/>
								)}
								<div className="demo-panel">
									<div className="panel-heading">
										<h3>Связанные исходники</h3>
										<span>
											Связи «многие ко многим» · можно использовать повторно
										</span>
									</div>
									{position.sourceIds.length ? (
										position.sourceIds.map((id) => {
											const source = manualSources.find(
												(item) => item.id === id,
											);
											return source ? (
												<div className="demo-source" key={id}>
													<label>
														<input
															type="radio"
															name={`publish-source-${mediumIndex}-${positionIndex}`}
															checked={position.selectedSourceId === id}
															onChange={() =>
																updatePosition(mediumIndex, positionIndex, {
																	selectedSourceId: id,
																})
															}
														/>
														<strong>{source.name}</strong>
														<span className="demo-secondary">
															{source.kind} · {source.path}
														</span>
													</label>
													<button
														type="button"
														aria-label={`Снять ${source.name}`}
														onClick={() =>
															updatePosition(mediumIndex, positionIndex, {
																sourceIds: position.sourceIds.filter(
																	(item) => item !== id,
																),
																selectedSourceId:
																	position.selectedSourceId === id
																		? ""
																		: position.selectedSourceId,
															})
														}
													>
														Снять
													</button>
												</div>
											) : null;
										})
									) : (
										<p>Исходники не выбраны для этой позиции.</p>
									)}
									<div className="demo-object-actions">
										<select
											aria-label="Выбрать вариант исходника"
											value={position.pendingSourceId ?? ""}
											onChange={(event) =>
												updatePosition(mediumIndex, positionIndex, {
													pendingSourceId: event.currentTarget.value,
												})
											}
										>
											<option value="">Выберите вариант…</option>
											{manualSources.map((source) => (
												<option key={source.id} value={source.id}>
													{source.name} · {source.kind}
												</option>
											))}
										</select>
										<button
											type="button"
											onClick={() => {
												const id = position.pendingSourceId;
												if (!id) return;
												updatePosition(mediumIndex, positionIndex, {
													sourceIds: [...new Set([...position.sourceIds, id])],
													pendingSourceId: "",
												});
											}}
										>
											Добавить к позиции
										</button>
										<button
											type="button"
											onClick={() => {
												const id = position.pendingSourceId;
												if (!id) return;
												setReplaceTarget({
													medium: mediumIndex,
													position: positionIndex,
													sourceId: id,
												});
											}}
										>
											Заменить набор…
										</button>
									</div>
								</div>
							</div>
						))}
						{!medium.positions.length && <p>Позиции не добавлены.</p>}
					</div>
				))}
				{!draft.media.length && <p>Носители не добавлены.</p>}
			</section>
			<section className="demo-panel">
				<div className="panel-heading">
					<div>
						<h2>Источники и обложка</h2>
						<span>
							Демонстрационные исходники; один вариант можно связать с
							несколькими позициями.
						</span>
					</div>
				</div>
				<p>
					Назначение исходника не публикует позицию. Можно связать несколько
					вариантов; замена набора подтверждается отдельно.
				</p>
				<p role="status">
					{sourceCount} назначений исходников в черновике ·{" "}
					{selectedCover
						? `выбрана обложка «${selectedCover.title}»`
						: "обложка не выбрана"}
					. Всё остаётся локальным демо-состоянием.
				</p>
				<div className="panel-heading">
					<h3>Обложка (необязательно)</h3>
					<button
						type="button"
						onClick={() => update({ ...draft, coverId: "" })}
					>
						Снять выбор
					</button>
				</div>
				<div className="demo-object-columns">
					{manualCovers.map((cover) => (
						<label className="demo-panel" key={cover.id}>
							<input
								type="radio"
								name="manual-cover"
								value={cover.id}
								checked={draft.coverId === cover.id}
								onChange={() => update({ ...draft, coverId: cover.id })}
							/>
							<span className={`manual-cover-art ${cover.style}`}>
								{cover.mark}
							</span>
							<strong>{cover.title}</strong>
							<span className="demo-secondary">{cover.source}</span>
						</label>
					))}
				</div>
			</section>
			<section className="demo-panel">
				<div className="demo-object-actions">
					<button
						type="button"
						onClick={() => {
							onReset();
							setReplaceTarget(null);
						}}
					>
						Восстановить демо-черновик
					</button>
					<button type="button" className="primary" onClick={onCreate}>
						Создать демо-релиз
					</button>
				</div>
			</section>
			<p className="demo-secondary">
				«Доступно» означает только опубликованные позиции. У локального релиза
				нет общего числа позиций провайдера; знаменатель не показывается.
			</p>
			{replaceTarget && (
				<Modal
					isOpen
					onOpenChange={(open) => {
						if (!open) setReplaceTarget(null);
					}}
					isDismissable
				>
					<Dialog
						role="alertdialog"
						aria-labelledby="replace-source-title"
						className="demo-panel"
					>
						<Heading slot="title" id="replace-source-title">
							Заменить связанные исходники?
						</Heading>
						<p>
							Заменить{" "}
							{
								draft.media[replaceTarget.medium].positions[
									replaceTarget.position
								].sourceIds.length
							}{" "}
							уже связанных вариантов на «
							{
								manualSources.find(
									(source) => source.id === replaceTarget.sourceId,
								)?.name
							}
							»? Публикации не изменятся; выбранный исходник будет снят, если
							его больше нет в наборе.
						</p>
						<button
							type="button"
							onClick={() => {
								updatePosition(replaceTarget.medium, replaceTarget.position, {
									sourceIds: [replaceTarget.sourceId],
									selectedSourceId:
										draft.media[replaceTarget.medium].positions[
											replaceTarget.position
										].selectedSourceId === replaceTarget.sourceId
											? replaceTarget.sourceId
											: "",
								});
								setReplaceTarget(null);
							}}
						>
							Заменить набор
						</button>
						<button type="button" onClick={() => setReplaceTarget(null)}>
							Отмена
						</button>
					</Dialog>
				</Modal>
			)}
		</section>
	);
}

function Field({
	label,
	value,
	onChange,
	placeholder,
}: {
	label: string;
	value: string;
	onChange: (value: string) => void;
	placeholder?: string;
}) {
	return (
		<label>
			{label}
			<input
				value={value}
				placeholder={placeholder}
				autoComplete="off"
				onChange={(event) => onChange(event.currentTarget.value)}
			/>
		</label>
	);
}

function RecordingPicker({
	position,
	onChange,
}: {
	position: ManualPositionDraft;
	onChange: (values: Partial<ManualPositionDraft>) => void;
}) {
	const [query, setQuery] = useState("");
	const matching = manualRecordings.filter((recording) =>
		`${recording.title} ${recording.artists}`
			.toLocaleLowerCase()
			.includes(query.toLocaleLowerCase()),
	);
	const selected = manualRecordings.find(
		(recording) => recording.id === position.recordingId,
	);
	return (
		<div className="demo-object-columns">
			<Field
				label="Поиск по демонстрационному каталогу"
				value={query}
				onChange={setQuery}
				placeholder="Фильтр названий / артистов"
			/>
			<label>
				Существующая композиция
				<select
					aria-label="Существующая композиция"
					value={position.recordingId}
					onChange={(event) =>
						onChange({ recordingId: event.currentTarget.value })
					}
				>
					<option value="">Выберите композицию…</option>
					{matching.map((recording) => (
						<option key={recording.id} value={recording.id}>
							{recording.title} — {recording.artists} ({recording.duration})
						</option>
					))}
				</select>
			</label>
			<span className="demo-secondary">
				Выбрана:{" "}
				{selected ? `${selected.title} · ${selected.artists}` : "не выбрана"}
			</span>
		</div>
	);
}
import { Modal, Dialog, Heading } from "react-aria-components";
