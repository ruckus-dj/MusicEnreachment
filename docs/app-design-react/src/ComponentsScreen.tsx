import { useState } from "react";
import {
	Button,
	Cell,
	Checkbox,
	Column,
	Dialog,
	DialogTrigger,
	Disclosure,
	DisclosurePanel,
	Heading,
	Input,
	Label,
	Link,
	ListBox,
	ListBoxItem,
	Modal,
	ModalOverlay,
	PageHeader,
	Panel,
	Popover,
	Radio,
	RadioGroup,
	Row,
	SearchField,
	Select,
	SelectValue,
	StatusBadge,
	Tab,
	TabList,
	TabPanel,
	Table,
	TableBody,
	TableHeader,
	Tabs,
	TextField,
	Toolbar,
} from "./ui";
import {
	originalFiles,
	type SourceFile,
	type SourceFiles,
} from "./catalogData";

const duration = (seconds: number) => {
	if (!Number.isFinite(seconds) || seconds < 0) return "—";
	const total = Math.round(seconds);
	if (!Number.isSafeInteger(total)) return "—";
	return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
};

export function ComponentsScreen({
	files = originalFiles,
	fileId = "north-f3",
	selectedFile: controlledSelectedFile,
	onSelectFile,
}: {
	files?: SourceFiles;
	fileId?: string;
	selectedFile?: string | null;
	onSelectFile?: (id: string) => void;
}) {
	const [localSelectedFile, setLocalSelectedFile] = useState<string | null>(
		null,
	);
	const selectedFile =
		controlledSelectedFile === undefined
			? localSelectedFile
			: controlledSelectedFile;
	const file = files[fileId] ?? Object.values(files)[0];
	const selectFile = (id: string) => {
		setLocalSelectedFile(id);
		onSelectFile?.(id);
	};
	if (!file)
		return (
			<section className="components-screen" id="content" tabIndex={-1}>
				<h1>Компоненты · плотный интерфейс</h1>
				<p className="ui-empty">Нет файла для назначения.</p>
			</section>
		);
	return (
		<section className="components-screen" id="content" tabIndex={-1}>
			<PageHeader className="components-header">
				<div>
					<p className="demo-eyebrow">СИСТЕМА · БИБЛИОТЕКА КОМПОНЕНТОВ</p>
					<h1>Компоненты · плотный интерфейс</h1>
					<p>
						Обе темы используют одинаковую геометрию и семантические состояния.
					</p>
				</div>
				<span className="components-version">RAC 1.22.1</span>
			</PageHeader>
			<Panel className="components-panel" aria-labelledby="actions-heading">
				<header className="ui-panel-heading">
					<div>
						<h2 id="actions-heading">Состояния и действия</h2>
						<span className="incoming-section-meta">
							Цвет всегда сопровождается подписью
						</span>
					</div>
				</header>
				<Toolbar aria-label="Примеры действий" className="components-toolbar">
					<StatusBadge tone="success">Высокая уверенность</StatusBadge>
					<StatusBadge tone="warning">Есть отличия</StatusBadge>
					<StatusBadge tone="error">Ошибка</StatusBadge>
					<StatusBadge>Без связи с провайдером</StatusBadge>
					<Button className="ui-button--primary">Основное действие</Button>
					<Button>Вторичное действие</Button>
					<Button isDisabled>Нет назначения</Button>
					<DialogTrigger>
						<Button>Предпросмотр диалога</Button>
						<ModalOverlay>
							<Modal>
								<Dialog>
									{({ close }) => (
										<>
											<Heading slot="title">Проверка публикации</Heading>
											<p>
												Диалог сохраняет фокус и закрывается клавишей Escape.
											</p>
											<Button onPress={close}>Закрыть</Button>
										</>
									)}
								</Dialog>
							</Modal>
						</ModalOverlay>
					</DialogTrigger>
				</Toolbar>
			</Panel>
			<Panel className="components-panel" aria-labelledby="file-heading">
				<header className="ui-panel-heading">
					<h2 id="file-heading">Файл для назначения</h2>
				</header>
				<div className="components-file">
					<FileChip
						file={file}
						selected={selectedFile === file.id}
						onSelect={selectFile}
					/>
				</div>
			</Panel>
			<Panel className="components-panel" aria-labelledby="table-heading">
				<header className="ui-panel-heading">
					<h2 id="table-heading">Техническая таблица</h2>
				</header>
				<div className="components-table-wrap">
					<Table
						aria-label="Сравнение исходных и провайдерских данных"
						className="components-table"
					>
						<TableHeader>
							<Column id="field" isRowHeader>
								Поле
							</Column>
							<Column id="source">Исходник</Column>
							<Column id="provider">Провайдер</Column>
							<Column id="difference">Различие</Column>
						</TableHeader>
						<TableBody>
							<Row id="title">
								<Cell>TITLE</Cell>
								<Cell>Northbound (live?)</Cell>
								<Cell>Northbound</Cell>
								<Cell>
									<StatusBadge tone="warning">
										Версия записи не подтверждена
									</StatusBadge>
								</Cell>
							</Row>
							<Row id="duration">
								<Cell>DURATION</Cell>
								<Cell>3:58</Cell>
								<Cell>3:46</Cell>
								<Cell>+12 с</Cell>
							</Row>
						</TableBody>
					</Table>
				</div>
			</Panel>
			<Panel className="components-panel" aria-labelledby="controls-heading">
				<header className="ui-panel-heading">
					<div>
						<h2 id="controls-heading">Поля и выбор</h2>
						<span className="incoming-section-meta">
							React Aria управляет клавиатурой, фокусом и состоянием
						</span>
					</div>
				</header>
				<Tabs defaultSelectedKey="fields" className="components-tabs">
					<TabList aria-label="Примеры компонентов">
						<Tab id="fields">Поля</Tab>
						<Tab id="selection">Выбор</Tab>
					</TabList>
					<TabPanel id="fields" className="components-control-grid">
						<TextField name="demo-search" autoComplete="off">
							<Label>Поиск в библиотеке</Label>
							<Input placeholder="Например, Northern Lines…" />
						</TextField>
						<SearchField name="file-search" autoComplete="off">
							<Label>Поиск файлов</Label>
							<Input placeholder="Имя или путь файла…" />
							<Button aria-label="Очистить поиск" slot="clear">
								×
							</Button>
						</SearchField>
						<Select defaultSelectedKey="manual">
							<Label>Режим назначения</Label>
							<Button>
								<SelectValue />
								<span aria-hidden="true">⌄</span>
							</Button>
							<Popover>
								<ListBox>
									<ListBoxItem id="auto">Автоматически</ListBoxItem>
									<ListBoxItem id="manual">Ручная обработка</ListBoxItem>
								</ListBox>
							</Popover>
						</Select>
					</TabPanel>
					<TabPanel id="selection" className="components-control-grid">
						<Checkbox defaultSelected>Показывать только различия</Checkbox>
						<RadioGroup defaultValue="tags">
							<Label>Способ поиска</Label>
							<Radio value="tags">По тегам</Radio>
							<Radio value="audio">По аудиоотпечатку</Radio>
						</RadioGroup>
						<Disclosure>
							<Button slot="trigger">Подробнее о доступности</Button>
							<DisclosurePanel>
								<p>
									Состояния доступны с клавиатуры и озвучиваются
									вспомогательными технологиями.
								</p>
							</DisclosurePanel>
						</Disclosure>
					</TabPanel>
				</Tabs>
			</Panel>
			<p className="demo-migration-note">
				Политика matching-rapidfuzz-v1 утверждена; этот макет не рассчитывает
				уверенность или её вклады. Демонстрационный вердикт подписан явно. Цвет
				не заменяет текст.
			</p>
			<footer className="components-footer">
				<Link href="#review">Вернуться во входящие</Link>
				<span>
					Семантические элементы · адаптивная плотная сетка · светлая и тёмная
					темы
				</span>
			</footer>
		</section>
	);
}

function FileChip({
	file,
	selected,
	onSelect,
}: {
	file: SourceFile;
	selected: boolean;
	onSelect: (id: string) => void;
}) {
	return (
		<div
			draggable
			title="Выбрать или перетащить файл"
			onDragStart={(event) => event.dataTransfer.setData("text/plain", file.id)}
		>
			<Button
				className={`file-chip ${selected ? "selected" : ""}`}
				data-selected={selected || undefined}
				aria-pressed={selected}
				onPress={() => onSelect(file.id)}
			>
				<span className="grip" aria-hidden="true">
					⠿
				</span>
				<span>
					<strong>{file.name}</strong>
					<span className="sub">
						{file.codec} · {duration(file.duration)} ·{" "}
						{file.codec === "FLAC" ? "24 бит / 48 кГц" : "320 кбит/с"} ·{" "}
						{file.size}
					</span>
				</span>
			</Button>
		</div>
	);
}
