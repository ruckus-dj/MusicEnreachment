export function russianCountNoun(
  count: number,
  [one, few, many]: readonly [string, string, string],
): string {
  const absolute = Math.abs(count);
  const lastTwo = absolute % 100;
  if (lastTwo >= 11 && lastTwo <= 14) return many;
  const last = absolute % 10;
  if (last === 1) return one;
  if (last >= 2 && last <= 4) return few;
  return many;
}

export function formatRussianCount(
  count: number,
  forms: readonly [string, string, string],
): string {
  return `${count} ${russianCountNoun(count, forms)}`;
}
