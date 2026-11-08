export function parseDuration(value: string): number | undefined {
  if (!value.trim()) return undefined;
  const match = /^(?:(\d+):([0-5]\d)|(\d+))$/.exec(value.trim());
  if (!match) return Number.NaN;
  const seconds = match[3]
    ? Number(match[3])
    : Number(match[1]) * 60 + Number(match[2]);
  const milliseconds = seconds * 1000;
  return Number.isSafeInteger(milliseconds) ? milliseconds : Number.NaN;
}

export function formatDuration(milliseconds: number): string {
  const seconds = Math.floor(milliseconds / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

export function durationForSave(
  originalMilliseconds: number | null | undefined,
  edited: boolean,
  input: string,
): number | undefined {
  return edited ? parseDuration(input) : (originalMilliseconds ?? undefined);
}
