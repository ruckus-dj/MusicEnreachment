function russianCount(count: number, forms: [string, string, string]) {
  const lastTwoDigits = count % 100;
  if (lastTwoDigits >= 11 && lastTwoDigits <= 14) return forms[2];
  const lastDigit = count % 10;
  if (lastDigit === 1) return forms[0];
  if (lastDigit >= 2 && lastDigit <= 4) return forms[1];
  return forms[2];
}

const en = {
  positionsHeading: (title: string) => `Positions · ${title}`,
  positionCount: (count: number) =>
    `${count} ${count === 1 ? "position" : "positions"}`,
  completeTrackList: "Complete position list",
  incompleteTrackList: "Position list is marked incomplete",
  positionInstructions:
    "Drag a free file onto a position, or select a file in the source rail and add it.",
  positionLabel: (medium: number, number: string, title: string) =>
    `Disc ${medium} · track ${number} · ${title}`,
  disc: (medium: number) => `Disc ${medium}`,
  inspectPosition: (label: string) => `Inspect position ${label}`,
  noFilesAssigned: "No files assigned",
  assignedCount: (count: number) =>
    `${count} ${count === 1 ? "file assigned" : "files assigned"}`,
  addSelected: "Add",
  assignSelectedTo: (file: string, position: string) =>
    `Add ${file} to ${position}`,
  chooseSourceFirst: "Select a file in the source rail first",
  alreadyAssignedHere: (file: string, position: string) =>
    `${file} is already assigned to ${position}`,
  savedAutomatic: "Saved automatic",
  savedAutomaticShort: "auto",
  savedScore: (percent: string) => `score ${percent}`,
  savedScoreLong: (percent: string) => `saved score ${percent}`,
  removeFile: (file: string, position: string) =>
    `Remove ${file} from ${position}`,
  dragFile: (file: string) => `Drag ${file}`,
  dropHint: "Drop an available file here",
  dropTarget: (position: string) => `Add a file to ${position}`,
  sourcesHeading: (count: number) =>
    `${count} ${count === 1 ? "free source" : "free sources"}`,
  sourcesInstructions:
    "Select a free file, then add it to a position. Dragging works with a mouse or keyboard.",
  allAssigned: "Every group file is assigned.",
  selectSourceHint: "Select a file to add it to a position.",
  selectedSource: (file: string) =>
    `Selected ${file}. Choose Add on a position.`,
  unavailable: "Unavailable",
  inspectSource: (file: string) => `Inspect source ${file}`,
  selectSource: (file: string) => `Select ${file}`,
};

const ru: typeof en = {
  positionsHeading: (title) => `Позиции · ${title}`,
  positionCount: (count) =>
    `${count} ${russianCount(count, ["позиция", "позиции", "позиций"])}`,
  completeTrackList: "Полный список позиций",
  incompleteTrackList: "Список позиций отмечен как неполный",
  positionInstructions:
    "Перетащите свободный файл на позицию или выберите файл справа и добавьте его.",
  positionLabel: (medium, number, title) =>
    `Диск ${medium} · трек ${number} · ${title}`,
  disc: (medium) => `Диск ${medium}`,
  inspectPosition: (label) => `Открыть позицию ${label}`,
  noFilesAssigned: "Нет назначенных файлов",
  assignedCount: (count) =>
    `Назначено файлов: ${count} ${russianCount(count, ["файл", "файла", "файлов"])}`,
  addSelected: "Добавить",
  assignSelectedTo: (file, position) =>
    `Добавить ${file} к позиции ${position}`,
  chooseSourceFirst: "Сначала выберите файл в списке источников",
  alreadyAssignedHere: (file, position) =>
    `${file} уже назначен позиции ${position}`,
  savedAutomatic: "Сохранено автоматически",
  savedAutomaticShort: "авто",
  savedScore: (percent) => `оценка ${percent}`,
  savedScoreLong: (percent) => `сохранённая оценка ${percent}`,
  removeFile: (file, position) => `Снять ${file} с позиции ${position}`,
  dragFile: (file) => `Перетащить ${file}`,
  dropHint: "Отпустите доступный файл здесь",
  dropTarget: (position) => `Добавить файл к позиции ${position}`,
  sourcesHeading: (count) =>
    `${count} ${russianCount(count, ["свободный файл", "свободных файла", "свободных файлов"])}`,
  sourcesInstructions:
    "Выберите свободный файл и добавьте его к позиции. Перетаскивание работает мышью и с клавиатуры.",
  allAssigned: "Все файлы группы назначены.",
  selectSourceHint: "Выберите файл, чтобы добавить его к позиции.",
  selectedSource: (file) => `Выбран ${file}. Нажмите «Добавить» у позиции.`,
  unavailable: "Недоступен",
  inspectSource: (file) => `Открыть источник ${file}`,
  selectSource: (file) => `Выбрать ${file}`,
};

export const matchingBoardMessages = { en, ru };

export type MatchingBoardMessages = typeof en;
