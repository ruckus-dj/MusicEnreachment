/** Exact tag values: presence and value order matter, key insertion order does not. */
export function sameTagKeyValues(
  left: readonly string[] | null | undefined,
  right: readonly string[] | null | undefined,
) {
  const leftValues = left ?? [];
  const rightValues = right ?? [];
  return (
    leftValues.length === rightValues.length &&
    leftValues.every((value, index) => value === rightValues[index])
  );
}

export function sameTagValues(
  left: Readonly<
    Record<string, readonly string[] | null | undefined>
  > | null = null,
  right: Readonly<
    Record<string, readonly string[] | null | undefined>
  > | null = null,
) {
  const leftTags = left ?? {};
  const rightTags = right ?? {};
  const keys = new Set([...Object.keys(leftTags), ...Object.keys(rightTags)]);
  for (const key of keys) {
    const hasLeft = Object.hasOwn(leftTags, key);
    const hasRight = Object.hasOwn(rightTags, key);
    if (hasLeft !== hasRight) return false;
    if (!sameTagKeyValues(leftTags[key], rightTags[key])) return false;
  }
  return true;
}
