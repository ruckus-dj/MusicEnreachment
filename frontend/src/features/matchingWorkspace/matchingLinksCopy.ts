export const matchingLinksCopy = {
  en: {
    preview: "Preview assignments",
    save: "Save matching links",
    assignedFiles: (count: number) => `Assigned files: ${count}`,
    remainingFiles: (count: number) => `Files remaining in Incoming: ${count}`,
  },
  ru: {
    preview: "Предпросмотр назначений",
    save: "Сохранить связи",
    assignedFiles: (count: number) => `Назначено файлов: ${count}`,
    remainingFiles: (count: number) => `Во входящих останутся файлы: ${count}`,
  },
} as const;
