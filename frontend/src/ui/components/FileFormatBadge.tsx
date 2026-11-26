import { StatusBadge } from "./StatusBadge";

/**
 * A technical format value such as "MKA · FLAC" or "FLAC". The chip uses a
 * dedicated non-status tone so format categories never read as success,
 * warning, error, or informational state.
 */
export function FileFormatBadge({
  value,
  className = "",
}: {
  value?: string | null;
  className?: string;
}) {
  const text = value?.trim();
  if (!text) return null;
  return (
    <StatusBadge tone="format" className={className}>
      {text}
    </StatusBadge>
  );
}

/**
 * A compact technical measurement such as "24 bit · 48 kHz". Empty values and
 * the existing em-dash placeholder stay plain text instead of becoming chips.
 */
export function MetadataBadge({
  value,
  className = "",
}: {
  value?: string | null;
  className?: string;
}) {
  const text = value?.trim();
  if (!text || text === "—") return <>{text || "—"}</>;
  return (
    <StatusBadge tone="metadata" className={className}>
      {text}
    </StatusBadge>
  );
}
