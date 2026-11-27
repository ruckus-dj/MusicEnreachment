export const setupSteps = [
  "directories",
  "tools",
  "publication",
  "summary",
] as const;

const setupPath = "#/setup";

export function isSetupHash(hash: string): boolean {
  return hash === setupPath || hash.startsWith(`${setupPath}?`);
}

export function setupStepIndex(hash: string): number | undefined {
  if (!isSetupHash(hash)) return undefined;
  const query = new URLSearchParams(hash.slice(setupPath.length + 1));
  const step = query.get("step");
  const index = (setupSteps as readonly string[]).indexOf(step ?? "");
  return index === -1 ? undefined : index;
}

export function setupStepHref(step: number): string {
  return `${setupPath}?step=${setupSteps[step] ?? setupSteps[0]}`;
}
