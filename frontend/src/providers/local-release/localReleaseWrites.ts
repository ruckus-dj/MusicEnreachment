import type { LocalProviderItem } from "../../api/generated/client.schemas";

/**
 * Keep uncertain or still-pending writes fenced across picker/route remounts.
 * The API assigns a fresh ID to each create, so a lost reply is not retryable.
 */
const unresolvedWrites = new Set<string>();

export function catalogCreateIdentity(
  kind: "artist" | "recording" | "release",
  titleOrName: string,
) {
  return `${kind}:new:${titleOrName.trim().normalize("NFC").toLowerCase()}`;
}

export async function writeCatalogRecord(
  identity: string,
  request: () => Promise<{ status: number; data: unknown }>,
): Promise<LocalProviderItem> {
  const uncertain = () =>
    Object.assign(new Error("catalog write outcome unknown"), {
      outcomeUncertain: true,
    });
  if (unresolvedWrites.has(identity)) throw uncertain();
  unresolvedWrites.add(identity);
  let response: Awaited<ReturnType<typeof request>>;
  try {
    response = await request();
  } catch {
    throw uncertain();
  }
  if (response.status >= 500) throw uncertain();
  if (response.status !== 200) {
    unresolvedWrites.delete(identity);
    throw new Error("catalog write rejected");
  }
  const item = response.data as Partial<LocalProviderItem>;
  if (!item?.id || item.provider_code !== "local") throw uncertain();
  unresolvedWrites.delete(identity);
  return item as LocalProviderItem;
}
