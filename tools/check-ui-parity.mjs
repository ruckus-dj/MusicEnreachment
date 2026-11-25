import { createHash } from "node:crypto";
import { lstatSync, readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { posix, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const prototypeUi = new URL("../docs/app-design-react/src/ui/", import.meta.url);
const applicationUi = new URL("../frontend/src/ui/", import.meta.url);
const prototypeLabel = "docs/app-design-react/src/ui";
const applicationLabel = "frontend/src/ui";

// Every portable UI module lives at the same path under src/ui/ in both trees.
// Leads use keys relative to that root. A prototype-first revision may lead the
// port only with exact bytes recorded in the receipt; applicationSHA256 is null
// until the first copy of a new module exists.
const CODE_FILE = /\.[cm]?[jt]sx?$/;
const TEST_FILE = /\.(?:test|spec)\.[cm]?[jt]sx?$/;
const MODULE_EXTENSIONS = [".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".json"];
// Only dependencies present in both package manifests may cross the UI boundary.
const RUNTIME_LIBRARIES = new Set(["react", "react-dom", "react-aria-components"]);
const TEST_LIBRARIES = new Set([
  "vitest",
  "@testing-library/react",
  "@testing-library/jest-dom",
]);
const CSS_LIBRARIES = new Set(["tailwindcss"]);
const HOST_ALIASES = [
  /^\//,
  /^@\//,
  /^~\//,
  /^@(?:api|app|host|src|ui)\//,
  /^(?:src|app|api|features|components|styles)\//,
  /^(?:i18n|routes|navigation|metadata|infrastructure|fixtures|genres|test)\//,
];

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

export function compareUiSources(pairs, receipts, readSource, readCopy) {
  const leads = new Map();
  const errors = [];
  for (const receipt of receipts) {
    if (
      typeof receipt?.from !== "string" ||
      typeof receipt.reason !== "string" ||
      !receipt.reason.trim() ||
      !/^[a-f0-9]{64}$/.test(receipt.prototypeSHA256) ||
      (receipt.applicationSHA256 !== null &&
        !/^[a-f0-9]{64}$/.test(receipt.applicationSHA256)) ||
      leads.has(receipt.from)
    ) {
      errors.push(`Invalid or duplicate prototype lead: ${receipt?.from}`);
      continue;
    }
    leads.set(receipt.from, receipt);
  }

  let identical = 0;
  const pending = [];
  for (const [from, to] of pairs) {
    const label = `${prototypeLabel}/${from} ↔ ${applicationLabel}/${to}`;
    const lead = leads.get(from);
    leads.delete(from);
    try {
      const source = readSource(from);
      let copy;
      try {
        copy = readCopy(to);
      } catch (error) {
        if (error.code !== "ENOENT") throw error;
        if (!lead) {
          errors.push(`UI source is not ported and has no prototype lead: ${from}`);
          continue;
        }
        if (lead.applicationSHA256 !== null) throw error;
        copy = null;
      }
      if (copy && source.equals(copy)) {
        identical++;
        if (lead) errors.push(`Remove the completed prototype lead: ${from}`);
      } else if (
        lead &&
        digest(source) === lead.prototypeSHA256 &&
        (copy ? digest(copy) : null) === lead.applicationSHA256
      ) {
        pending.push(`${label}: ${lead.reason}`);
      } else {
        errors.push(`Different source bytes: ${label}`);
      }
    } catch (error) {
      errors.push(`Cannot compare ${label}: ${error.code ?? error.message}`);
    }
  }

  for (const from of leads.keys()) {
    errors.push(`Prototype lead is not a registered UI source: ${from}`);
  }
  return { errors, identical, pending };
}

export function enumerateUiPairs(sourceFiles, copyFiles) {
  const source = new Set(sourceFiles);
  const copy = new Set(copyFiles);
  return {
    pairs: [...source].sort().map((path) => [path, path]),
    sourceOnly: [...source].filter((path) => !copy.has(path)).sort(),
    copyOnly: [...copy].filter((path) => !source.has(path)).sort(),
  };
}

export function compareUiDirectories(sourceFiles, copyFiles, receipts, readSource, readCopy) {
  const { pairs, sourceOnly, copyOnly } = enumerateUiPairs(sourceFiles, copyFiles);
  const result = compareUiSources(pairs, receipts, readSource, readCopy);
  for (const path of copyOnly) {
    result.errors.push(`Application-only UI source: ${path}`);
  }
  return { ...result, sourceOnly, copyOnly };
}

function basePackage(specifier) {
  const parts = specifier.split("/");
  return specifier.startsWith("@") ? parts.slice(0, 2).join("/") : parts[0];
}

function isHostAlias(specifier) {
  return HOST_ALIASES.some((pattern) => pattern.test(specifier));
}

function relativeTarget(filePath, specifier) {
  return posix.normalize(posix.join(posix.dirname(filePath), specifier));
}

function checkAsset(label, filePath, kind, specifier, exists) {
  const location = `${label}/${filePath}`;
  if (specifier.includes("\\") || isHostAlias(specifier)) {
    return `${location}: ${kind} uses a host/API alias: ${specifier}`;
  }
  const target = relativeTarget(filePath, specifier);
  if (target === ".." || target.startsWith("../") || posix.isAbsolute(target)) {
    return `${location}: ${kind} escapes the UI boundary: ${specifier}`;
  }
  if (kind === "@source") {
    // Globs are permitted inside the tree. After a pattern component, a parent
    // traversal is ambiguous to globbers and must not be used.
    if (/[*?{\[][\s\S]*\/\.\.(?:\/|$)/.test(specifier))
      return `${location}: ${kind} has a parent traversal after a glob: ${specifier}`;
    return null;
  }
  const asset = target.split(/[?#]/, 1)[0];
  const candidates = kind === "import"
    ? [asset, ...MODULE_EXTENSIONS.flatMap((ext) => [asset + ext, `${asset}/index${ext}`])]
    : [asset];
  if (exists && !candidates.some(exists)) {
    return `${location}: ${kind} references a missing UI asset: ${specifier}`;
  }
  return null;
}

function checkSpecifier(label, filePath, kind, specifier, exists) {
  const location = `${label}/${filePath}`;
  if (kind === "@source") return checkAsset(label, filePath, kind, specifier, exists);
  if (kind === "url") {
    if (/^(?:data|https?):/.test(specifier) || specifier.startsWith("#") || specifier === "") {
      return null;
    }
    if (isHostAlias(specifier)) {
      return `${location}: url uses a host/API alias: ${specifier}`;
    }
    // A CSS url() is an asset path even without a leading "./".
    return checkAsset(label, filePath, kind, specifier, exists);
  }
  if (specifier.startsWith(".")) {
    return checkAsset(label, filePath, kind, specifier, exists);
  }
  if (isHostAlias(specifier)) {
    return `${location}: ${kind} uses a host/API alias: ${specifier}`;
  }
  const approved = kind === "import"
    ? new Set([...RUNTIME_LIBRARIES, ...(TEST_FILE.test(filePath) ? TEST_LIBRARIES : [])])
    : CSS_LIBRARIES;
  if (!approved.has(basePackage(specifier))) {
    return `${location}: Nonapproved UI dependency: ${specifier}`;
  }
  return null;
}

let typeScript;

function loadTypeScript() {
  if (!typeScript) {
    try {
      const requireFrontend = createRequire(
        new URL("../frontend/package.json", import.meta.url),
      );
      typeScript = requireFrontend("typescript");
    } catch {
      throw new Error("Install frontend dependencies (task frontend:install) to parse UI imports");
    }
  }
  return typeScript;
}

function scriptKind(ts, fileName) {
  if (fileName.endsWith(".tsx")) return ts.ScriptKind.TSX;
  if (fileName.endsWith(".jsx")) return ts.ScriptKind.JSX;
  if (/\.[cm]?ts$/.test(fileName)) return ts.ScriptKind.TS;
  return ts.ScriptKind.JS;
}

export function parseModuleSpecifiers(fileName, text, reportUnsupported = () => undefined) {
  const ts = loadTypeScript();
  const source = ts.createSourceFile(
    fileName,
    text,
    ts.ScriptTarget.Latest,
    true,
    scriptKind(ts, fileName),
  );
  const specifiers = [];
  const push = (node) => {
    if (node && ts.isStringLiteralLike(node)) specifiers.push(node.text);
    else reportUnsupported("computed module reference");
  };
  const importMeta = (node) => ts.isMetaProperty(node) && node.keywordToken === ts.SyntaxKind.ImportKeyword;
  const visit = (node) => {
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
      if (node.moduleSpecifier) push(node.moduleSpecifier);
    } else if (
      ts.isImportEqualsDeclaration(node) &&
      ts.isExternalModuleReference(node.moduleReference)
    ) {
      push(node.moduleReference.expression);
    } else if (ts.isCallExpression(node)) {
      const callee = node.expression;
      if (
        (callee.kind === ts.SyntaxKind.ImportKeyword ||
          (ts.isIdentifier(callee) && callee.text === "require"))
      ) {
        push(node.arguments[0]);
      } else if (
        (ts.isPropertyAccessExpression(callee) || ts.isElementAccessExpression(callee)) &&
        importMeta(callee.expression)
      ) {
        reportUnsupported("module loader on import.meta");
      }
    } else if (ts.isNewExpression(node) &&
        ts.isIdentifier(node.expression) && node.expression.text === "URL" &&
        node.arguments?.[1] && (ts.isPropertyAccessExpression(node.arguments[1]) ||
          ts.isElementAccessExpression(node.arguments[1])) && importMeta(node.arguments[1].expression)) {
      push(node.arguments[0]);
    } else if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)) {
      push(node.argument.literal);
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return specifiers;
}

export function parseCssReferences(text) {
  const references = [];
  const importRanges = [];
  const importPattern = /@(import|reference|source)\s+(?:not\s+)?(?:url\(\s*)?(?:"([^"]+)"|'([^']+)'|([^'")\s;]+))/g;
  for (const match of text.matchAll(importPattern)) {
    const specifier = match[2] ?? match[3] ?? match[4];
    if (match[1] === "source" && specifier.startsWith("inline(")) continue;
    if (match[1] === "source" && specifier === "none") continue;
    references.push({ kind: `@${match[1]}`, specifier });
    importRanges.push([match.index, match.index + match[0].length]);
  }
  for (const match of text.matchAll(/url\(\s*(?:"([^"]*)"|'([^']*)'|([^)"']+?))\s*\)/g)) {
    if (importRanges.some(([start, end]) => match.index >= start && match.index < end)) continue;
    references.push({ kind: "url", specifier: (match[1] ?? match[2] ?? match[3]).trim() });
  }
  return references;
}

export function isUiTextFile(path) {
  return CODE_FILE.test(path) || path.endsWith(".css");
}

export function checkUiBoundaries(label, files, options = {}) {
  const errors = [];
  for (const { path, text } of files) {
    const references = CODE_FILE.test(path)
      ? parseModuleSpecifiers(path, text, (feature) => {
          errors.push(`${label}/${path}: Unsupported code in portable UI: ${feature}`);
        }).map((specifier) => ({ kind: "import", specifier }))
      : parseCssReferences(text);
    for (const { kind, specifier } of references) {
      const error = checkSpecifier(label, path, kind, specifier, options.exists);
      if (error && !errors.includes(error)) errors.push(error);
    }
  }
  return errors;
}

export function collectUiTree(root) {
  const dir = fileURLToPath(root);
  if (lstatSync(dir).isSymbolicLink()) {
    throw new Error(`UI source root is a symlink: ${dir}`);
  }
  const files = [];
  const symlinks = [];
  const visit = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = resolve(current, entry.name);
      const path = relative(dir, full).replaceAll("\\", "/");
      if (entry.isSymbolicLink()) {
        symlinks.push(path);
      } else if (entry.isDirectory()) {
        visit(full);
      } else if (entry.isFile() && entry.name !== ".DS_Store") {
        files.push(path);
      }
    }
  };
  visit(dir);
  return { files: files.sort(), symlinks: symlinks.sort() };
}

function textSources(root, files) {
  return files
    .filter(isUiTextFile)
    .map((path) => ({ path, text: readFileSync(new URL(path, root), "utf8") }));
}

function existsIn(files) {
  const paths = new Set(files);
  return (path) => paths.has(path);
}

const entryScript = 'src="/src/main.tsx"';

function normalizeEntry(html, prototype) {
  let normalized = html;
  if (prototype) {
    normalized = normalized.replace(
      "<title>Медиатека · стенд MeloTrove</title>",
      "<title>MeloTrove</title>",
    );
  }
  return normalized
    .replace('src="/src/app/main.tsx"', entryScript)
    .replace('src="./src/app/main.tsx"', entryScript)
    .replace('src="./src/main.tsx"', entryScript);
}

function main() {
  const receipts = JSON.parse(
    readFileSync(new URL("./ui-parity-prototype-leads.json", import.meta.url), "utf8"),
  );
  let prototypeTree;
  let applicationTree;
  try {
    prototypeTree = collectUiTree(prototypeUi);
    applicationTree = collectUiTree(applicationUi);
  } catch (error) {
    console.error(`Cannot read UI source tree: ${error.message}`);
    process.exitCode = 1;
    return;
  }
  const result = compareUiDirectories(
    prototypeTree.files,
    applicationTree.files,
    receipts,
    (path) => readFileSync(new URL(path, prototypeUi)),
    (path) => readFileSync(new URL(path, applicationUi)),
  );
  const errors = [
    ...result.errors,
    ...prototypeTree.symlinks.map(
      (path) => `Cross UI symlink is not allowed: ${prototypeLabel}/${path}`,
    ),
    ...applicationTree.symlinks.map(
      (path) => `Cross UI symlink is not allowed: ${applicationLabel}/${path}`,
    ),
    ...checkUiBoundaries(prototypeLabel, textSources(prototypeUi, prototypeTree.files), {
      exists: existsIn(prototypeTree.files),
    }),
    ...checkUiBoundaries(applicationLabel, textSources(applicationUi, applicationTree.files), {
      exists: existsIn(applicationTree.files),
    }),
  ];
  // The entry documents are host integration, not portable UI modules: compare
  // them only after the two expected prototype/application entry differences.
  const prototypeEntry = normalizeEntry(
    readFileSync(new URL("../../index.html", prototypeUi), "utf8"),
    true,
  );
  const applicationEntry = normalizeEntry(
    readFileSync(new URL("../../index.html", applicationUi), "utf8"),
    false,
  );
  if (prototypeEntry !== applicationEntry) {
    errors.push("Different application entry presentation: index.html");
  }
  for (const error of errors) console.error(error);
  for (const lead of result.pending) console.log(`Prototype awaiting port: ${lead}`);
  console.log(`Byte-identical UI source pairs: ${result.identical}/${prototypeTree.files.length}`);
  const inventory = [
    ...result.sourceOnly.map((path) => `${prototypeLabel}/${path}`),
    ...result.copyOnly.map((path) => `${applicationLabel}/${path}`),
  ].sort();
  console.log(`Unpaired UI source files: ${inventory.length}`);
  if (process.argv.includes("--inventory")) {
    console.log(`Portable UI tree (${prototypeTree.files.length} files):`);
    for (const path of prototypeTree.files) console.log(`  ${path}`);
    if (inventory.length) {
      console.log("Unpaired paths:");
      for (const path of inventory) console.log(`  ${path}`);
    }
  }
  if (errors.length > 0) process.exitCode = 1;
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  main();
}
