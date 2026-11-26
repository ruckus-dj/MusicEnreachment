/**
 * Prototype-only publication template data and validation. This mirrors the
 * restricted grammar of backend/internal/publication/template.go: relative path
 * components, balanced braces, known tokens, an optional `:02` numeric modifier
 * and quoted alternatives. It performs no filesystem access and no server call.
 */
export const defaultPublicationTemplate = `{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}`;

const knownTokens = new Set([
  "ALBUM_ARTIST",
  "TRACK_ARTIST",
  "ALBUM_ARTIST_ID",
  "TRACK_ARTIST_ID",
  "ALBUM_ARTIST_MBID",
  "TRACK_ARTIST_MBID",
  "ALBUM_ARTIST_MBID_PART",
  "TRACK_ARTIST_MBID_PART",
  "ALBUM_PRIMARY_ARTIST",
  "TRACK_PRIMARY_ARTIST",
  "ALBUM_PRIMARY_ARTIST_ID",
  "TRACK_PRIMARY_ARTIST_ID",
  "ALBUM_PRIMARY_ARTIST_MBID",
  "TRACK_PRIMARY_ARTIST_MBID",
  "ALBUM_PRIMARY_ARTIST_MBID_PART",
  "TRACK_PRIMARY_ARTIST_MBID_PART",
  "ALBUM_NAME",
  "TRACK_NAME",
  "ALBUM_YEAR",
  "DISC_NUMBER",
  "TRACK_NUMBER",
  "ALBUM_TYPE",
  "ALBUM_PRIMARY_TYPE",
  "ALBUM_SECONDARY_TYPES",
  "ARTIST_ID",
  "ALBUM_ID",
  "ALBUM_MBID",
  "ALBUM_MBID_PART",
  "POSITION_ID",
  "EXT",
]);

const numericTokens = new Set(["DISC_NUMBER", "TRACK_NUMBER", "ALBUM_YEAR"]);

/** Returns the index of the closing brace, or -1 when the expression is unclosed. */
function expressionEnd(source: string, start: number): number {
  let quoted = false;
  let escaped = false;
  for (let i = start; i < source.length; i++) {
    const char = source[i];
    if (!quoted) {
      if (char === "}") return i;
      if (char === '"') quoted = true;
      continue;
    }
    if (escaped) {
      escaped = false;
    } else if (char === "\\") {
      escaped = true;
    } else if (char === '"') {
      quoted = false;
    }
  }
  return -1;
}

/** A quoted alternative must be one terminated literal; inner quotes are escaped. */
function validQuotedLiteral(term: string): boolean {
  if (term.length < 2 || !term.startsWith('"') || !term.endsWith('"')) {
    return false;
  }
  let escaped = false;
  for (let i = 1; i < term.length; i++) {
    const char = term[i];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (char === "\\") {
      escaped = true;
      continue;
    }
    if (char === "\n") return false;
    if (char === '"') return i === term.length - 1;
  }
  return false;
}

function validAlternative(term: string): boolean {
  if (term === "") return false;
  if (term.startsWith('"')) return validQuotedLiteral(term);
  const cut = term.indexOf(":");
  const name = cut < 0 ? term : term.slice(0, cut);
  if (!knownTokens.has(name)) return false;
  if (cut < 0) return true;
  return term.slice(cut + 1) === "02" && numericTokens.has(name);
}

function parseAlternatives(source: string): boolean {
  let start = 0;
  let quoted = false;
  let escaped = false;
  for (let i = 0; i <= source.length; i++) {
    if (i < source.length) {
      const char = source[i];
      if (quoted) {
        if (escaped) escaped = false;
        else if (char === "\\") escaped = true;
        else if (char === '"') quoted = false;
        continue;
      }
      if (char === '"') {
        quoted = true;
        continue;
      }
      if (char !== "|") continue;
    }
    if (!validAlternative(source.slice(start, i).trim())) return false;
    start = i + 1;
  }
  return !quoted;
}

/** Basic client-side check for a relative publication template. */
export function validDemoPublicationTemplate(value: string): boolean {
  if (value === "" || value.startsWith("/") || value.startsWith("\\")) {
    return false;
  }
  let componentHasContent = false;
  for (let i = 0; i < value.length; ) {
    const char = value[i];
    if (char === "/") {
      if (!componentHasContent) return false;
      componentHasContent = false;
      i++;
      continue;
    }
    if (char === "\\" || char === "}") return false;
    if (char === "{") {
      const end = expressionEnd(value, i + 1);
      if (end < 0 || !parseAlternatives(value.slice(i + 1, end))) {
        return false;
      }
      componentHasContent = true;
      i = end + 1;
      continue;
    }
    componentHasContent = true;
    i++;
  }
  return componentHasContent;
}
