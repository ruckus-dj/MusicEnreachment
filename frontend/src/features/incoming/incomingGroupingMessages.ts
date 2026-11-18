import type { Locale } from "../../i18n/locale";
import {
  type IncomingGroupName,
  incomingGroupNameText,
} from "./incomingGroupNames";

export type IncomingGroupingMessage =
  | {
      key:
        | "snapshotChanged"
        | "actionAdded"
        | "previewError"
        | "confirmError"
        | "refreshError"
        | "draftCancelled"
        | "saved"
        | "snapshotRefreshed"
        | "previewReady"
        | "previewRequired";
    }
  | {
      key: "httpError";
      action: "previewError" | "confirmError" | "refreshError";
      status: number;
    }
  | { key: "raw"; message: string };

export type IncomingGroupingActionPresentation =
  | { kind: "merge"; groups: (IncomingGroupName | undefined)[] }
  | { kind: "split"; group: IncomingGroupName | undefined; fileCount: number }
  | { kind: "move"; group: IncomingGroupName | undefined; fileCount: number };

const messages = {
  en: {
    heading: "Correct incoming groups",
    intro: "Changes stay as a page draft until confirmed separately.",
    file: (index: number) => `File ${index + 1}`,
    selectFileFallback: (index: number) => `Select file ${index + 1}`,
    selectGroup: (group: string) => `Select ${group} to merge`,
    merge: "Merge",
    selectFile: (file: string) => `Select file ${file}`,
    notReady: "Not ready",
    ready: "Ready",
    split: "Separate selected files",
    moveLabel: "Move selected files to",
    targetGroup: "Target group",
    chooseGroup: "Choose a group",
    move: "Move files",
    mergeSelected: "Merge selected groups",
    draftActionsLabel: "Draft actions",
    draftActions: (count: string) => `Draft actions (${count})`,
    mergeAction: (groups: string) => `Merge ${groups}`,
    splitAction: (count: string, group: string) =>
      `Separate ${count} from group “${group}”`,
    moveAction: (count: string, group: string) =>
      `Move ${count} to group “${group}”`,
    cancelDraft: "Cancel draft",
    preview: "Preview",
    confirm: "Confirm changes",
    refresh: "Refresh snapshot",
    previewError: "Preview failed.",
    confirmError: "Confirmation failed.",
    refreshError: "Refresh failed.",
    snapshotChanged:
      "The snapshot changed. Review the draft and create a new preview.",
    actionAdded: "Action added. Create a preview before continuing.",
    draftCancelled: "Draft cancelled.",
    saved: "Changes saved.",
    snapshotRefreshed:
      "Snapshot refreshed. Review the saved action list and create a new preview.",
    previewReady: "Preview is ready; confirm separately to save changes.",
    previewRequired:
      "Create a successful preview before adding another action.",
  },
  ru: {
    heading: "Коррекция входящих групп",
    intro:
      "Изменения остаются черновиком страницы до отдельного подтверждения.",
    file: (index: number) => `Файл ${index + 1}`,
    selectFileFallback: (index: number) => `Выбрать файл ${index + 1}`,
    selectGroup: (group: string) => `Выбрать ${group} для объединения`,
    merge: "Объединить",
    selectFile: (file: string) => `Выбрать файл ${file}`,
    notReady: "Не готов",
    ready: "Готов",
    split: "Отделить выбранные файлы",
    moveLabel: "Переместить выбранные файлы в",
    targetGroup: "Целевая группа",
    chooseGroup: "Выберите группу",
    move: "Переместить файлы",
    mergeSelected: "Объединить выбранные группы",
    draftActionsLabel: "Действия черновика",
    draftActions: (count: string) => `Действия черновика (${count})`,
    mergeAction: (groups: string) => `Объединить: ${groups}`,
    splitAction: (count: string, group: string) =>
      `Отделить ${count} из группы «${group}»`,
    moveAction: (count: string, group: string) =>
      `Переместить ${count} в группу «${group}»`,
    cancelDraft: "Отменить черновик",
    preview: "Предпросмотр",
    confirm: "Подтвердить изменения",
    refresh: "Обновить снимок",
    previewError: "Предпросмотр не выполнен.",
    confirmError: "Подтверждение не выполнено.",
    refreshError: "Обновление не выполнено.",
    snapshotChanged:
      "Снимок изменился. Проверьте черновик и создайте новый предпросмотр.",
    actionAdded:
      "Действие добавлено. Создайте предпросмотр, прежде чем продолжать.",
    draftCancelled: "Черновик отменён.",
    saved: "Изменения сохранены.",
    snapshotRefreshed:
      "Снимок обновлён. Проверьте сохранённый список действий и создайте новый предпросмотр.",
    previewReady:
      "Предпросмотр готов; для сохранения требуется отдельное подтверждение.",
    previewRequired:
      "Создайте успешный предпросмотр, прежде чем добавлять следующее действие.",
  },
} as const;

export function incomingGroupingMessage(
  locale: Locale,
  message: IncomingGroupingMessage,
): string {
  if (message.key === "raw") return message.message;
  if (message.key === "httpError") {
    const copy = messages[locale][message.action].replace(/\.$/, "");
    return `${copy} (HTTP ${message.status}).`;
  }
  return messages[locale][message.key];
}

export function incomingGroupingFileCount(
  count: number,
  locale: Locale,
): string {
  const formattedCount = new Intl.NumberFormat(locale).format(count);
  if (locale === "en")
    return `${formattedCount} ${count === 1 ? "file" : "files"}`;
  const plural = new Intl.PluralRules(locale).select(count);
  const label =
    plural === "one" ? "файл" : plural === "few" ? "файла" : "файлов";
  return `${formattedCount} ${label}`;
}

export function incomingGroupingPathCount(
  count: number,
  locale: Locale,
): string {
  const formattedCount = new Intl.NumberFormat(locale).format(count);
  if (locale === "en")
    return `${formattedCount} ${count === 1 ? "unique path" : "unique paths"}`;
  const plural = new Intl.PluralRules(locale).select(count);
  const label =
    plural === "one"
      ? "уникальный путь"
      : plural === "few"
        ? "уникальных пути"
        : "уникальных путей";
  return `${formattedCount} ${label}`;
}

export function incomingGroupingNumber(count: number, locale: Locale): string {
  return new Intl.NumberFormat(locale).format(count);
}

export function incomingGroupingActionSummary(
  locale: Locale,
  action: IncomingGroupingActionPresentation,
): string {
  const copy = messages[locale];
  const groupName = (name: IncomingGroupName | undefined) =>
    incomingGroupNameText(name, locale);

  if (action.kind === "merge") {
    return copy.mergeAction(action.groups.map(groupName).join(", "));
  }

  const count = incomingGroupingFileCount(action.fileCount, locale);
  const group = groupName(action.group);
  return action.kind === "split"
    ? copy.splitAction(count, group)
    : copy.moveAction(count, group);
}

export const incomingGroupingMessages = messages;
