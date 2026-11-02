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
			<section
				className="min-w-0 p-4 max-[600px]:p-3"
				id="content"
				tabIndex={-1}
			>
				<h1 className="m-0 text-xl leading-[1.25] text-[var(--ink)]">
					Компоненты · плотный интерфейс
				</h1>
				<p className="p-[18px] text-[var(--muted)]">
					Нет файла для назначения.
				</p>
			</section>
		);
	return (
		<section className="min-w-0 p-4 max-[600px]:p-3" id="content" tabIndex={-1}>
			<PageHeader className="mb-3 flex min-w-0 items-center justify-between gap-4 max-[600px]:items-start max-[600px]:flex-col">
				<div className="min-w-0">
					<p className="mb-[3px] text-[10px] tracking-[.06em] text-[var(--muted)]">
						СИСТЕМА · БИБЛИОТЕКА КОМПОНЕНТОВ
					</p>
					<h1 className="m-0 text-xl leading-[1.25] text-[var(--ink)] [text-wrap:balance]">
						Компоненты · плотный интерфейс
					</h1>
					<p className="mt-[3px] mb-0 text-xs text-[var(--muted)]">
						Обе темы используют одинаковую геометрию и семантические состояния.
					</p>
				</div>
				<span className="flex-none rounded-[3px] border border-[var(--line)] bg-[var(--raised)] px-[7px] py-1 font-mono text-[10px] text-[var(--muted)]">
					RAC 1.22.1
				</span>
			</PageHeader>
			<Panel className="mb-[10px]" aria-labelledby="actions-heading">
				<header className="flex min-h-10 items-center justify-between gap-2 border-b border-[var(--line)] px-[10px] py-[7px]">
					<div>
						<h2 className="m-0 text-[13px] font-semibold" id="actions-heading">
							Состояния и действия
						</h2>
						<span className="mt-0.5 block text-[10px] text-[var(--muted)]">
							Цвет всегда сопровождается подписью
						</span>
					</div>
				</header>
				<Toolbar
					aria-label="Примеры действий"
					className="flex flex-wrap items-center gap-[7px] px-[10px] py-[9px]"
				>
					<StatusBadge tone="success">Высокая уверенность</StatusBadge>
					<StatusBadge tone="warning">Есть отличия</StatusBadge>
					<StatusBadge tone="error">Ошибка</StatusBadge>
					<StatusBadge>Без связи с провайдером</StatusBadge>
					<Button className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90">
						Основное действие
					</Button>
					<Button>Вторичное действие</Button>
					<Button isDisabled>Нет назначения</Button>
					<DialogTrigger>
						<Button>Предпросмотр диалога</Button>
						<Modal>
							<Dialog>
								{({ close }) => (
									<>
										<Heading slot="title">Проверка публикации</Heading>
										<p>Диалог сохраняет фокус и закрывается клавишей Escape.</p>
										<Button onPress={close}>Закрыть</Button>
									</>
								)}
							</Dialog>
						</Modal>
					</DialogTrigger>
				</Toolbar>
			</Panel>
			<Panel className="mb-[10px]" aria-labelledby="file-heading">
				<header className="flex min-h-10 items-center justify-between gap-2 border-b border-[var(--line)] px-[10px] py-[7px]">
					<h2 className="m-0 text-[13px] font-semibold" id="file-heading">
						Файл для назначения
					</h2>
				</header>
				<div className="max-w-[650px] px-[10px] py-2">
					<FileChip
						file={file}
						selected={selectedFile === file.id}
						onSelect={selectFile}
					/>
				</div>
			</Panel>
			<Panel className="mb-[10px]" aria-labelledby="table-heading">
				<header className="flex min-h-10 items-center justify-between gap-2 border-b border-[var(--line)] px-[10px] py-[7px]">
					<h2 className="m-0 text-[13px] font-semibold" id="table-heading">
						Техническая таблица
					</h2>
				</header>
				<div className="overflow-x-auto">
					<Table
						aria-label="Сравнение исходных и провайдерских данных"
						className="w-full border-collapse text-[11px] [font-variant-numeric:tabular-nums] [&_th]:border-b [&_th]:border-[var(--line)] [&_th]:px-2 [&_th]:py-1.5 [&_th]:text-left [&_th]:align-middle [&_td]:border-b [&_td]:border-[var(--line)] [&_td]:px-2 [&_td]:py-1.5 [&_td]:text-left [&_td]:align-middle [&_thead_th]:bg-[var(--raised)] [&_thead_th]:font-semibold [&_thead_th]:text-[var(--muted)] [&_tbody_th]:bg-transparent [&_tbody_th]:font-semibold [&_tbody_th]:text-[var(--ink)] [&_tbody_tr:last-child_td]:border-b-0"
					>
						<TableHeader>
							<Column
								id="field"
								isRowHeader
								className="border-b border-[var(--line)] px-2 py-1.5 text-left align-middle"
							>
								Поле
							</Column>
							<Column
								id="source"
								className="border-b border-[var(--line)] px-2 py-1.5 text-left align-middle"
							>
								Исходник
							</Column>
							<Column
								id="provider"
								className="border-b border-[var(--line)] px-2 py-1.5 text-left align-middle"
							>
								Провайдер
							</Column>
							<Column
								id="difference"
								className="border-b border-[var(--line)] px-2 py-1.5 text-left align-middle"
							>
								Различие
							</Column>
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
			<Panel className="mb-[10px]" aria-labelledby="controls-heading">
				<header className="flex min-h-10 items-center justify-between gap-2 border-b border-[var(--line)] px-[10px] py-[7px]">
					<div>
						<h2 className="m-0 text-[13px] font-semibold" id="controls-heading">
							Поля и выбор
						</h2>
						<span className="mt-0.5 block text-[10px] text-[var(--muted)]">
							React Aria управляет клавиатурой, фокусом и состоянием
						</span>
					</div>
				</header>
				<Tabs
					defaultSelectedKey="fields"
					className="min-w-0 px-[10px] pb-[10px]"
				>
					<TabList aria-label="Примеры компонентов">
						<Tab id="fields">Поля</Tab>
						<Tab id="selection">Выбор</Tab>
					</TabList>
					<TabPanel
						id="fields"
						className="grid grid-cols-[repeat(3,minmax(160px,1fr))] items-start gap-3 px-0 pt-[10px] pb-0.5 max-[900px]:grid-cols-2 max-[600px]:grid-cols-1"
					>
						<TextField
							name="demo-search"
							autoComplete="off"
							className="min-w-0"
						>
							<Label>Поиск в библиотеке</Label>
							<Input placeholder="Например, Northern Lines…" />
						</TextField>
						<SearchField
							name="file-search"
							autoComplete="off"
							className="pt-[19px] max-[600px]:pt-0"
						>
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
					<TabPanel
						id="selection"
						className="grid grid-cols-[repeat(3,minmax(160px,1fr))] items-start gap-3 px-0 pt-[10px] pb-0.5 max-[900px]:grid-cols-2 max-[600px]:grid-cols-1"
					>
						<Checkbox defaultSelected>Показывать только различия</Checkbox>
						<RadioGroup defaultValue="tags">
							<Label>Способ поиска</Label>
							<Radio value="tags">По тегам</Radio>
							<Radio value="audio">По аудиоотпечатку</Radio>
						</RadioGroup>
						<Disclosure className="self-start">
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
			<p className="my-2 border-l-[3px] border-[var(--accent)] bg-[var(--raised)] px-[10px] py-2 text-[11px] text-[var(--muted)]">
				Политика matching-rapidfuzz-v1 утверждена; этот макет не рассчитывает
				уверенность или её вклады. Демонстрационный вердикт подписан явно. Цвет
				не заменяет текст.
			</p>
			<footer className="flex flex-wrap items-center justify-between gap-2 text-[10px] text-[var(--muted)] max-[600px]:items-start max-[600px]:flex-col">
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
				className="flex min-h-12 w-full cursor-grab justify-start gap-2 p-[7px] text-left data-[selected=true]:border-[var(--accent)] data-[selected=true]:bg-[var(--accent-soft)]"
				data-selected={selected || undefined}
				aria-pressed={selected}
				onPress={() => onSelect(file.id)}
			>
				<span className="text-[var(--muted)]" aria-hidden="true">
					⠿
				</span>
				<span>
					<strong className="block text-[11px] font-semibold wrap-anywhere">
						{file.name}
					</strong>
					<span className="block text-[10px] text-[var(--muted)]">
						{file.codec} · {duration(file.duration)} ·{" "}
						{file.codec === "FLAC" ? "24 бит / 48 кГц" : "320 кбит/с"} ·{" "}
						{file.size}
					</span>
				</span>
			</Button>
		</div>
	);
}
