import type { SourceRootResponse } from "../../api/generated/client.schemas";

export type SourceAction = "list" | "read" | "create" | "edit" | "delete";

export function message(reason: unknown): string {
  return reason instanceof Error
    ? reason.message
    : "Не удалось выполнить запрос.";
}

export function sourceFailure(
  action: SourceAction,
  response: { status: number; data?: { detail?: string } },
): Error {
  if (response.status === 404) {
    return new Error("Каталог не найден: возможно, его удалили в другом окне.");
  }
  if (response.status === 409) {
    return new Error(
      "Каталог участвует в активном сканировании. Изменение пути, отключение и удаление отклонены сервером: повторите после завершения сканирования.",
    );
  }
  if (response.status === 400 && action === "delete") {
    return new Error(
      "Подтверждение удаления не совпало с текущим каталогом: обновите страницу и повторите.",
    );
  }
  if (response.status === 400 && (action === "create" || action === "edit")) {
    return new Error(
      "Сервер отклонил каталог: путь должен быть существующим читаемым каталогом на сервере, не пересекаться с каталогами MeloTrove и не повторять уже зарегистрированный путь.",
    );
  }
  return new Error(
    response.data?.detail || `Сервер вернул ошибку ${response.status}.`,
  );
}

export function statusLabel(status: SourceRootResponse["status"]): string {
  if (status === "available") return "Доступен";
  if (status === "unavailable") return "Недоступен";
  return "Не проверен";
}

// Russian plural forms do not follow the count alone: 1 файл, 2 файла, 11 файлов.
export function fileCount(count: number): string {
  const mod100 = count % 100;
  const mod10 = count % 10;
  if (mod100 >= 11 && mod100 <= 14) return `${count} файлов`;
  if (mod10 === 1) return `${count} файл`;
  if (mod10 >= 2 && mod10 <= 4) return `${count} файла`;
  return `${count} файлов`;
}

export function lastScanLabel(root: SourceRootResponse): string {
  return root.last_successful_scan_at
    ? new Date(root.last_successful_scan_at).toLocaleString()
    : "Не выполнялось";
}

// Generation 0 records that no successful traversal was ever applied.
export function inventoryLabel(root: SourceRootResponse): string {
  return root.scan_generation === 0
    ? "Не сканировался"
    : fileCount(root.location_count);
}

export function inventoryAvailabilityNote(root: SourceRootResponse): string {
  return root.status === "unavailable" && root.scan_generation > 0
    ? "Каталог недоступен: показан инвентарь последнего успешного сканирования."
    : "";
}

export function inventoryScopeNote(root: SourceRootResponse): string {
  return root.stale && root.inventory_path
    ? `Инвентарь относится к прежнему пути ${root.inventory_path}.`
    : "";
}

export function inventoryEmptyNote(root: SourceRootResponse): string {
  return root.status === "available" &&
    root.scan_generation > 0 &&
    root.location_count === 0
    ? "Последнее успешное сканирование не нашло файлов с поддерживаемым аудиорасширением."
    : "";
}

export function filesSummary(root: SourceRootResponse): string {
  if (root.scan_generation === 0) {
    return "Инвентаря нет: каталог ещё ни разу не сканировался успешно.";
  }
  if (root.location_count === 0) {
    return "Последнее успешное сканирование не нашло ни одного файла с поддерживаемым аудиорасширением.";
  }
  return `В инвентаре последнего успешного сканирования ${fileCount(root.location_count)}.`;
}
