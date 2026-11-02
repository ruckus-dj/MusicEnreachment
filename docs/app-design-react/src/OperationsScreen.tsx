import { useState } from "react";
import { Button, Dialog, Heading, Modal } from "react-aria-components";
import {
	recoveryFixture,
	type OriginalRelease,
	type OriginalTrack,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";

export function OperationsScreen({
	releases,
	deletions,
	onRecover,
}: {
	releases: OriginalRelease[];
	deletions: DeletionOperation[];
	onRecover: () => void;
}) {
	const [open, setOpen] = useState(false);
	const [notice, setNotice] = useState("");
	const track: OriginalTrack | undefined = releases
		.flatMap((item) => item.tracks)
		.find((item) => item.id === recoveryFixture.trackId);
	return (
		<section>
			<h1>Текущие операции</h1>
			<p>Незавершённая работа; завершённой истории здесь нет.</p>
			{notice && <p role="status">{notice}</p>}
			<section className="demo-panel">
				<h2>Требует внимания</h2>
				<div className="demo-preview-table">
					<table>
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
							<tr>
								<td>Signals · позиция 06</td>
								<td>Публикация набора позиций</td>
								<td>Ошибка</td>
								<td>EACCES: нет записи во временном каталоге</td>
								<td>
									Обычный повтор заново подготавливает публикацию; это не
									принудительная перепубликация
								</td>
								<td>
									<Button
										onPress={() =>
											setNotice(
												"Демо: обычный повтор повторяет полную подготовку после восстановления прав. Он не является принудительной перепубликацией.",
											)
										}
									>
										Повторить
									</Button>
								</td>
							</tr>
							{!track?.recovered && (
								<tr>
									<td>Northern Lines · позиция 01</td>
									<td>Прерванная публикация</td>
									<td>Прервана</td>
									<td>Файл установлен, результат не зарегистрирован</td>
									<td>
										Сверить теги, технические параметры и разрешение операции
									</td>
									<td>
										<Button onPress={() => setOpen(true)}>
											Восстановить регистрацию
										</Button>
									</td>
								</tr>
							)}
						</tbody>
					</table>
				</div>
			</section>
			<section className="demo-panel">
				<h2>Удаление из медиатеки · демонстрационные запросы</h2>
				<div className="demo-preview-table">
					<table>
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
							{deletions.map((operation, index) => (
								<tr key={`${operation.target}:${index}`}>
									<td>{operation.target}</td>
									<td>Удаление медиатеки</td>
									<td>В очереди · демо</td>
									<td>{operation.scope}</td>
									<td>
										{operation.publications} строк публикаций;{" "}
										{operation.paths.length} принадлежащих приложению файлов в
										подтверждённой области
									</td>
									<td>Ожидает · запрос не меняет демонстрационные данные</td>
								</tr>
							))}
							{!deletions.length && (
								<tr>
									<td colSpan={6}>Нет результатов.</td>
								</tr>
							)}
						</tbody>
					</table>
				</div>
			</section>
			<p>
				Восстановление может зарегистрировать подходящий текущий файл по тегам,
				техническим параметрам и актуальному разрешению; совпадение с исходником
				не проверяется. Отдельное действие «Явно перепубликовать» полностью
				подготавливает файл из выбранного исходника, даже если данные не
				изменились. Здесь только демонстрация, реальные файловые операции и
				гарантии безопасности не проверяются.
			</p>
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog aria-labelledby="recovery-title">
					<Heading slot="title" id="recovery-title">
						Зарегистрировать восстановленный результат?
					</Heading>
					<p>
						Известный зафиксированный путь:{" "}
						<code>{recoveryFixture.targetPath}</code>
					</p>
					<p>
						Проверки демо-сценария: теги{" "}
						{recoveryFixture.tagsMatch ? "совпадают" : "не совпадают"},
						технические параметры{" "}
						{recoveryFixture.technicalMatch ? "совпадают" : "не совпадают"},
						актуальность разрешения{" "}
						{recoveryFixture.authorityCurrent ? "актуальна" : "изменилась"}.
						Доказательство совпадения исходника: не требуется.
					</p>
					<p>
						Если любая обязательная проверка не пройдена, результат не
						регистрируют и выполняют обычный полный повтор.
					</p>
					<p>
						Демо не читает реальный файл и не подтверждает гарантии работы с
						файловой системой и безопасности.
					</p>
					<Button onPress={() => setOpen(false)}>Отмена</Button>
					<Button
						onPress={() => {
							if (
								!recoveryFixture.tagsMatch ||
								!recoveryFixture.technicalMatch ||
								!recoveryFixture.authorityCurrent ||
								!track?.published
							)
								setNotice(
									"Проверка восстановления не пройдена; требуется обычный повтор.",
								);
							else {
								onRecover();
								setNotice(
									"Демо: текущая публикация зарегистрирована после восстановления; снимок метаданных заменён без сохранения истории.",
								);
							}
							setOpen(false);
						}}
					>
						Подтвердить
					</Button>
				</Dialog>
			</Modal>
		</section>
	);
}
