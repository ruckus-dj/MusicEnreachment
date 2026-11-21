export type SourceInventoryRow = {
  id: string;
  path: string;
  href?: string;
  size: string;
  modified?: string;
  status: string;
  statusCode?: string;
  notes?: readonly string[];
};

const labels = {
  en: {
    title: "Inventory files",
    path: "Path",
    size: "Size",
    modified: "Modified",
    check: "Audio check",
  },
  ru: {
    title: "Файлы инвентаря",
    path: "Путь",
    size: "Размер",
    modified: "Изменён",
    check: "Проверка аудио",
  },
} as const;

const cell =
  "border-b border-[var(--line)] px-2 py-1.5 align-top wrap-anywhere";

export function SourceInventoryView({
  locale,
  caption,
  rows,
}: {
  locale: "en" | "ru";
  caption?: string;
  rows: readonly SourceInventoryRow[];
}) {
  const text = labels[locale];
  return (
    <div className="min-w-0 overflow-x-auto">
      <table className="w-full min-w-[36rem] table-fixed border-collapse text-left text-sm">
        <caption className="sr-only">{caption ?? text.title}</caption>
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
                {row.size}
              </td>
              <td data-label={text.modified} className={cell}>
                {row.modified ?? "—"}
              </td>
              <td data-label={text.check} className={cell}>
                <span
                  className="inline-block rounded-full border border-[var(--line)] px-2 py-0.5 text-xs data-[status=probe_error]:text-[var(--error)]"
                  data-status={row.statusCode}
                >
                  {row.status}
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
