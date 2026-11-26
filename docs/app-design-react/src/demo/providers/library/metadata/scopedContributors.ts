import type { MetadataValues } from "./projection";
import { navidromeFields } from "./registry";

export type ScopedContributorRow = {
  name: string;
  role?: string;
  instrument?: string;
  artist_id?: string;
  mbid?: string;
};

export type ScopedContributors =
  | { state: "absent" }
  | { state: "explicit_empty" }
  | { state: "value"; rows: ScopedContributorRow[] };

export function registryContributorRoles() {
  return navidromeFields.flatMap(({ kind, role }) =>
    kind === "contributors" && role ? [role] : [],
  );
}

const roles = new Set(registryContributorRoles());
function roleTag(role: string) {
  return role.toUpperCase();
}
function idTag(role: string) {
  return `MUSICBRAINZ_${roleTag(role)}ID`;
}

export function contributorTagKeys(...snapshots: MetadataValues[]) {
  return [
    ...[...roles].flatMap((role) => [roleTag(role), idTag(role)]),
    ...snapshots.flatMap((tags) =>
      Object.keys(tags).filter(
        (key) =>
          key.startsWith("PERFORMER:") ||
          key.startsWith("MUSICBRAINZ_PERFORMERID:"),
      ),
    ),
  ];
}

export function contributorProjectionIntent(
  position: ScopedContributors | undefined,
  recording: ScopedContributors | undefined,
  purpose: "comparison" | "publication",
) {
  return [position, recording].find(
    (entry) =>
      entry?.state === "explicit_empty" ||
      (entry?.state === "value" &&
        (purpose === "comparison" || entry.rows.length)),
  );
}

export function retainUnspecifiedContributorTags(
  tags: MetadataValues,
  current: MetadataValues,
  position?: ScopedContributors,
  recording?: ScopedContributors,
) {
  if (contributorProjectionIntent(position, recording, "publication")) return;
  for (const key of contributorTagKeys(current)) {
    if (!Object.hasOwn(tags, key) && Object.hasOwn(current, key))
      tags[key] = [...current[key]];
  }
}

export function omitsStructuredPerformer(
  intent: ScopedContributors | undefined,
  outputContainer: string | undefined,
): boolean {
  const container = outputContainer?.toUpperCase();
  return (
    (container === "MP3" || container === "M4A") &&
    intent?.state === "value" &&
    intent.rows.some(({ role }) => role?.trim().toLowerCase() === "performer")
  );
}

function alignIDs(rows: ScopedContributorRow[], publication: boolean): boolean {
  if (rows.some((row) => !row.mbid?.trim())) return false;
  if (!publication) return true;
  const byName = new Map<string, string>();
  const byID = new Map<string, string>();
  for (const row of rows) {
    const name = row.name.trim();
    const id = row.mbid?.trim().toLowerCase() ?? "";
    if (!name || /[;/]/.test(name) || name.length > 1024) return false;
    if (
      (byName.has(name) && byName.get(name) !== id) ||
      (byID.has(id) && byID.get(id) !== name)
    )
      return false;
    byName.set(name, id);
    byID.set(id, name);
  }
  return true;
}

export function projectScopedContributorTags(
  intent: ScopedContributors | undefined,
  outputContainer?: string,
): MetadataValues {
  if (!intent || intent.state === "absent") return {};
  if (intent.state === "explicit_empty")
    return Object.fromEntries(contributorTagKeys().map((key) => [key, []]));

  const container = outputContainer?.toUpperCase();
  const groups = new Map<
    string,
    { ids: string; performer: boolean; rows: ScopedContributorRow[] }
  >();
  for (const row of intent.rows) {
    const role = row.role?.trim().toLowerCase() ?? "";
    if (!roles.has(role)) continue;
    const performer = role === "performer";
    if (performer && container && container !== "FLAC" && container !== "MKA")
      continue;
    const names = roleTag(role);
    const ids = idTag(role);
    const qualify = (tag: string) =>
      performer && container
        ? `${tag}:${row.instrument ? row.instrument.toUpperCase() : tag}`
        : tag;
    const key = qualify(names);
    const group = groups.get(key) ?? { ids: qualify(ids), rows: [], performer };
    group.rows.push(row);
    groups.set(key, group);
  }
  const tags: MetadataValues = {};
  const performerIDsAligned =
    !container ||
    [...groups.values()]
      .filter(({ performer }) => performer)
      .every(
        ({ rows }) =>
          alignIDs(rows, true) &&
          new Set(rows.map((row) => row.instrument ?? "")).size === 1 &&
          !rows.some((row) =>
            /^(PERFORMER|MUSICBRAINZ_PERFORMERID)$/i.test(row.instrument ?? ""),
          ),
      );
  for (const [key, group] of groups) {
    tags[key] = group.rows.map(({ name }) => name);
    if (
      (!group.performer || performerIDsAligned) &&
      alignIDs(group.rows, Boolean(container))
    ) {
      tags[group.ids] = group.rows.map(({ mbid }) => mbid ?? "");
    }
  }
  return tags;
}
