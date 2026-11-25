export type SourceInventoryStatus =
  | "audio"
  | "no_audio"
  | "not_analyzed"
  | "probe_error"
  | "unknown";

export type SourceInventoryRow = {
  id: string;
  path: string;
  href?: string;
  sizeBytes?: number;
  modifiedAt?: string;
  status: SourceInventoryStatus;
  notes?: readonly string[];
};

const labels = {
  en: {
    title: "Inventory files",
    path: "Path",
    size: "Size",
    modified: "Modified",
    check: "Audio check",
    status: {
      audio: "Audio",
      no_audio: "No audio track",
      not_analyzed: "Not checked",
      probe_error: "Check failed",
      unknown: "Unknown",
    },
    units: ["B", "KB", "MB", "GB"],
  },
  ru: {
    title: "Файлы инвентаря",
    path: "Путь",
    size: "Размер",
    modified: "Изменён",
    check: "Проверка аудио",
    status: {
      audio: "Аудио",
      no_audio: "Нет аудиодорожки",
      not_analyzed: "Не проверено",
      probe_error: "Ошибка проверки",
      unknown: "Неизвестно",
    },
    units: ["Б", "КБ", "МБ", "ГБ"],
  },
} as const;

const cell =
  "border-b border-[var(--line)] px-2 py-1.5 align-top wrap-anywhere";

export function sourceInventoryStatusLabel(
  locale: "en" | "ru",
  status: SourceInventoryStatus,
) {
  return labels[locale].status[status];
}

export function SourceInventoryView({
  locale,
  rows,
}: {
  locale: "en" | "ru";
  rows: readonly SourceInventoryRow[];
}) {
  const text = labels[locale];
  const number = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 });
  const date = new Intl.DateTimeFormat(locale, {
    dateStyle: "short",
    timeStyle: "short",
  });
  const size = (bytes?: number) => {
    if (bytes === undefined) return "—";
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < text.units.length - 1) {
      value /= 1024;
      unit += 1;
    }
    return `${number.format(value)} ${text.units[unit]}`;
  };
  return (
    <div className="min-w-0 overflow-x-auto">
      <table className="w-full min-w-[36rem] table-fixed border-collapse text-left text-sm">
        <caption className="sr-only">{text.title}</caption>
        <colgroup>
          <col className="w-[44%]" />
          <col className="w-[12%]" />
          <col className="w-[17%]" />
          <col className="w-[27%]" />
        </colgroup>
        <thead>
          <tr className="bg-[var(--raised)]">
            {[text.path, text.size, text.modified, text.check].map((label) => (
              <th key={label} scope="col" className={cell}>
                {label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.id}>
              <th scope="row" data-label={text.path} className={cell}>
                {row.href ? (
                  <a
                    className="text-[var(--accent)] underline underline-offset-2"
                    href={row.href}
                  >
                    <code>{row.path}</code>
                  </a>
                ) : (
                  <code>{row.path}</code>
                )}
              </th>
              <td data-label={text.size} className={cell}>
                {size(row.sizeBytes)}
              </td>
              <td data-label={text.modified} className={cell}>
                {row.modifiedAt &&
                Number.isFinite(Date.parse(row.modifiedAt)) ? (
                  <time dateTime={row.modifiedAt}>
                    {date.format(new Date(row.modifiedAt))}
                  </time>
                ) : (
                  "—"
                )}
              </td>
              <td data-label={text.check} className={cell}>
                <span
                  className="inline-block rounded-full border border-[var(--line)] px-2 py-0.5 text-xs data-[status=probe_error]:text-[var(--error)]"
                  data-status={row.status}
                >
                  {sourceInventoryStatusLabel(locale, row.status)}
                </span>
                {row.notes?.map((note) => (
                  <span
                    key={note}
                    className="mt-1 block text-xs text-[var(--muted)]"
                  >
                    {note}
                  </span>
                ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
