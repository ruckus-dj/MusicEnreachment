/**
 * Formats an operation creation time for the shared Operations list.
 *
 * The application and the demo prototype must present one instant with the
 * same locale precision. Live data supplies RFC3339 strings and demo fixtures
 * supply millisecond timestamps.
 */
export function formatOperationTimestamp(
  value: string | number | null | undefined,
  locale: string,
): string {
  if (value === undefined || value === null || value === "") return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString(locale);
}
