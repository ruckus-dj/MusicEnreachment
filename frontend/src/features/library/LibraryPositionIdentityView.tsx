const messages = {
  en: {
    position: "Position",
    title: "Position title",
    duration: "Duration",
    composition: "Recording",
    unspecified: "Not specified",
    unspecifiedDuration: "Not specified",
    facts: "Position details",
  },
  ru: {
    position: "Позиция",
    title: "Название позиции",
    duration: "Длительность",
    composition: "Композиция",
    unspecified: "Не указано",
    unspecifiedDuration: "Не указана",
    facts: "Сведения о позиции",
  },
} as const;

function formatDuration(duration: number) {
  const seconds = Math.floor(duration / 1000);
  const milliseconds = duration % 1000;
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}${milliseconds ? `.${String(milliseconds).padStart(3, "0")}` : ""}`;
}

export function LibraryPositionIdentityView({
  locale,
  position,
  title,
  durationMs,
  recordingTitle,
}: {
  locale: "en" | "ru";
  position: string;
  title?: string;
  durationMs: number | null;
  recordingTitle?: string | null;
}) {
  const copy = messages[locale];
  const rows = [
    [copy.position, position],
    [copy.title, title || copy.unspecified],
    [
      copy.duration,
      durationMs === null
        ? copy.unspecifiedDuration
        : formatDuration(durationMs),
    ],
    [copy.composition, recordingTitle || copy.unspecified],
  ];
  return (
    <dl
      className="m-0 grid min-w-0 gap-2 rounded border border-[var(--line)] bg-[var(--raised)] p-3 text-sm sm:grid-cols-2 lg:grid-cols-4"
      aria-label={`${copy.facts} ${position}`}
    >
      {rows.map(([label, value]) => (
        <div key={label} className="min-w-0">
          <dt className="text-xs text-[var(--muted)]">{label}</dt>
          <dd className="m-0 wrap-anywhere">{value}</dd>
        </div>
      ))}
    </dl>
  );
}
