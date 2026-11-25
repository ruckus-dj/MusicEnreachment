import { createHash } from "node:crypto";
import { lstatSync, readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { posix, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const prototypeSrc = new URL("../docs/app-design-react/src/", import.meta.url);
const applicationSrc = new URL("../frontend/src/", import.meta.url);
const prototypeUi = new URL("../docs/app-design-react/src/ui/", import.meta.url);
const applicationUi = new URL("../frontend/src/ui/", import.meta.url);
const prototypeLabel = "docs/app-design-react/src/ui";
const applicationLabel = "frontend/src/ui";
const prototypeHostLabel = "docs/app-design-react/src";
const applicationHostLabel = "frontend/src";

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
const OUTLET_OWNED_VIEWS = new Set([
  "ui/features/incoming/IncomingScreenView",
  "ui/features/library/ArtistScreenView",
  "ui/features/library/LibraryCatalogPageView",
  "ui/features/local-catalog/LocalCatalogScreenView",
  "ui/features/operations/OperationsScreenView",
  "ui/features/settings/SettingsScreenView",
]);
// Portable UI behavior is only equivalent at equal resolved versions. Keep the
// explicit shared runtime and build set; this is not a full resolver.
export const SHARED_DEPENDENCY_PACKAGES = [
  "react",
  "react-dom",
  "react-aria-components",
  "react-aria",
  "react-stately",
  "@react-types/shared",
  "@internationalized/date",
  "@internationalized/number",
  "@internationalized/string",
  "tailwindcss",
  "@tailwindcss/vite",
  "@tailwindcss/node",
  "@tailwindcss/oxide",
  "vite",
  "@vitejs/plugin-react",
];
const HOST_ALIASES = [
  /^\//,
  /^@\//,
  /^~\//,
  /^@(?:api|app|host|src|ui)\//,
  /^(?:src|app|api|features|components|styles)\//,
  /^(?:i18n|routes|navigation|metadata|infrastructure|fixtures|genres|test)\//,
];
// Hosts may only wire checked local views or these dependency-free contexts.
const HEADLESS_JSX = new Map([
  ["react", new Set(["Fragment", "StrictMode"])],
  ["@tanstack/react-query", new Set(["QueryClientProvider"])],
  ["react-aria-components", new Set(["I18nProvider", "SSRProvider"])],
]);
const FORBIDDEN_JSX_PROPS = new Set([
  "className",
  "style",
  "class",
  "dangerouslySetInnerHTML",
  "innerHTML",
  "outerHTML",
]);
const COPY_JSX_PROPS = new Set([
  "alt",
  "aria-description",
  "aria-label",
  "children",
  "description",
  "heading",
  "label",
  "message",
  "placeholder",
  "subtitle",
  "summary",
  "textValue",
  "title",
]);
const ELEMENT_FACTORIES = new Set(["createElement", "cloneElement", "jsx", "jsxs", "jsxDEV"]);

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

export function compareDependencyVersions(designLock, applicationLock) {
  const versionOf = (lock, name) => lock?.packages?.[`node_modules/${name}`]?.version;
  const errors = [];
  for (const name of SHARED_DEPENDENCY_PACKAGES) {
    const design = versionOf(designLock, name);
    const application = versionOf(applicationLock, name);
    if (!design || !application) {
      errors.push(`Missing shared dependency lock entry: ${name}`);
    } else if (design !== application) {
      errors.push(
        `Shared dependency drift: ${name} (design ${design}, application ${application})`,
      );
    }
  }
  return errors;
}

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

function isHostPath(path) {
  return path !== "ui" && !path.startsWith("ui/") &&
    !TEST_FILE.test(path) && !/(^|\/)(?:test|__tests__)(?:\/|$)/.test(path);
}

export function hostSourceFiles(files) {
  return files.filter(isHostPath);
}

function collectHostBindings(ts, source) {
  const bindings = new Map();
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteralLike(statement.moduleSpecifier)) {
      continue;
    }
    const clause = statement.importClause;
    if (!clause || clause.isTypeOnly) continue;
    const specifier = statement.moduleSpecifier.text;
    if (clause.name) {
      bindings.set(clause.name.text, { specifier, imported: "default" });
    }
    const named = clause.namedBindings;
    if (named && ts.isNamespaceImport(named)) {
      bindings.set(named.name.text, { specifier, imported: "*" });
    } else if (named && ts.isNamedImports(named)) {
      for (const element of named.elements) {
        if (element.isTypeOnly) continue;
        bindings.set(element.name.text, {
          specifier,
          imported: (element.propertyName ?? element.name).text,
        });
      }
    }
  }
  const declare = (node) => {
    if (ts.isFunctionDeclaration(node) && node.name) {
      bindings.set(node.name.text, { local: true });
    } else if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name)) {
      const value = unwrapHostExpression(ts, node.initializer);
      if (value && (ts.isArrowFunction(value) || ts.isFunctionExpression(value))) {
        bindings.set(node.name.text, { local: true });
      } else if (value && ts.isCallExpression(value)) {
        const callee = value.expression;
        const fromReact = ts.isIdentifier(callee)
          ? bindings.get(callee.text)?.imported === "createContext" &&
            bindings.get(callee.text)?.specifier === "react"
          : ts.isPropertyAccessExpression(callee) &&
            callee.name.text === "createContext" &&
            ts.isIdentifier(callee.expression) &&
            bindings.get(callee.expression.text)?.specifier === "react";
        if (fromReact) bindings.set(node.name.text, { local: true, context: true });
      }
    }
    ts.forEachChild(node, declare);
  };
  ts.forEachChild(source, declare);
  return bindings;
}

function headlessJsxMember(binding, member) {
  if (!binding?.specifier || binding.specifier.startsWith(".")) return false;
  return HEADLESS_JSX.get(basePackage(binding.specifier))?.has(member) ?? false;
}

function checkedLocalBinding(filePath, specifier) {
  const target = relativeTarget(filePath, specifier);
  return target !== ".." && !target.startsWith("../") && !posix.isAbsolute(target);
}

function hostJsxTagError(ts, label, filePath, tagName, bindings) {
  const location = `${label}/${filePath}`;
  if (ts.isJsxNamespacedName(tagName)) {
    return `${location}: Namespaced JSX element is not allowed: <${tagName.getText()}>`;
  }
  if (ts.isIdentifier(tagName)) {
    const name = tagName.text;
    if (!/^[A-Z]/.test(name) || name.includes("-")) {
      return `${location}: Native or SVG JSX element is not allowed: <${name}>`;
    }
    const binding = bindings.get(name);
    if (!binding) {
      return `${location}: Custom JSX element is not a checked local component: <${name}>`;
    }
    if (binding.local) return null;
    if (binding.specifier.startsWith(".")) {
      if (!checkedLocalBinding(filePath, binding.specifier)) {
        return `${location}: JSX component escapes the checked host source root: <${name}>`;
      }
      return null;
    }
    if (headlessJsxMember(binding, binding.imported)) return null;
    return `${location}: Visual third-party JSX is not allowed: <${name}> from ${binding.specifier}`;
  }
  if (ts.isPropertyAccessExpression(tagName)) {
    const member = tagName.name.text;
    const owner = tagName.expression;
    const rendered = tagName.getText();
    if (ts.isIdentifier(owner)) {
      const binding = bindings.get(owner.text);
      if (binding) {
        if (binding.local) {
          return binding.context && (member === "Provider" || member === "Consumer")
            ? null
            : `${location}: Custom JSX element is not a checked local component: <${rendered}>`;
        }
        if (binding.specifier.startsWith(".")) {
          if (!checkedLocalBinding(filePath, binding.specifier)) {
            return `${location}: JSX component escapes the checked host source root: <${rendered}>`;
          }
          return null;
        }
        if (headlessJsxMember(binding, member)) return null;
        return `${location}: Visual third-party JSX is not allowed: <${rendered}> from ${binding.specifier}`;
      }
      if (owner.text === "React" && HEADLESS_JSX.get("react").has(member)) return null;
    }
    return `${location}: Custom JSX element is not a checked local component: <${rendered}>`;
  }
  return `${location}: Unsupported JSX tag: <${tagName.getText()}>`;
}

function unwrapHostExpression(ts, expression) {
  let value = expression;
  while (
    value &&
    (ts.isParenthesizedExpression(value) ||
      ts.isAsExpression(value) ||
      ts.isSatisfiesExpression(value) ||
      ts.isNonNullExpression(value))
  ) {
    value = value.expression;
  }
  return value;
}

function hostCopyText(ts, expression) {
  const value = expression && unwrapHostExpression(ts, expression);
  if (!value) return null;
  if (ts.isStringLiteralLike(value)) return value.text;
  if (ts.isTemplateExpression(value)) return value.getText();
  return null;
}

function hostJsxAttributeErrors(ts, location, attributes, errors) {
  for (const attribute of attributes.properties) {
    if (ts.isJsxSpreadAttribute(attribute)) continue;
    if (!ts.isJsxAttribute(attribute)) continue;
    const name = ts.isIdentifier(attribute.name)
      ? attribute.name.text
      : attribute.name.getText();
    if (FORBIDDEN_JSX_PROPS.has(name)) {
      errors.push(`${location}: Host JSX must not set ${name}`);
    }
    if (COPY_JSX_PROPS.has(name) && attribute.initializer) {
      const expression = ts.isJsxExpression(attribute.initializer)
        ? attribute.initializer.expression
        : attribute.initializer;
      const copy = hostCopyText(ts, expression);
      if (copy !== null && copy.trim()) {
        errors.push(`${location}: Host JSX must not contain copy`);
      }
    }
  }
}

function hostJsxChildErrors(ts, location, children, errors) {
  for (const child of children) {
    if (ts.isJsxText(child)) {
      if (child.text.trim()) errors.push(`${location}: Host JSX must not contain copy`);
      continue;
    }
    if (ts.isJsxExpression(child)) {
      const copy = hostCopyText(ts, child.expression);
      if (copy !== null && copy.trim()) {
        errors.push(`${location}: Host JSX must not contain copy`);
      }
    }
  }
}

function hostJsxNodeErrors(ts, label, filePath, node, bindings, errors) {
  const location = `${label}/${filePath}`;
  if (ts.isJsxFragment(node)) {
    hostJsxChildErrors(ts, location, node.children, errors);
    return;
  }
  const opening = ts.isJsxElement(node) ? node.openingElement : node;
  const tagError = hostJsxTagError(ts, label, filePath, opening.tagName, bindings);
  if (tagError) errors.push(tagError);
  hostJsxAttributeErrors(ts, location, opening.attributes, errors);
  if (ts.isJsxElement(node)) {
    hostJsxChildErrors(ts, location, node.children, errors);
  }
}

function factoryMember(ts, callee) {
  if (ts.isPropertyAccessExpression(callee)) return callee.name.text;
  if (
    ts.isElementAccessExpression(callee) &&
    ts.isStringLiteralLike(callee.argumentExpression)
  ) {
    return callee.argumentExpression.text;
  }
  return undefined;
}

function hostElementFactory(ts, callee, bindings) {
  if (ts.isIdentifier(callee)) {
    const binding = bindings.get(callee.text);
    if (binding && ELEMENT_FACTORIES.has(binding.imported)) return binding.imported;
    return ELEMENT_FACTORIES.has(callee.text) ? callee.text : null;
  }
  const member = factoryMember(ts, callee);
  if (!member) return null;
  const owner = callee.expression;
  if (!ts.isIdentifier(owner)) return null;
  if (owner.text === "document" &&
      (member === "createElement" || member === "createElementNS" || member === "write"))
    return `document.${member}`;
  if (!ELEMENT_FACTORIES.has(member)) return null;
  const binding = bindings.get(owner.text);
  if (binding) return binding.specifier?.startsWith("react") ? member : null;
  return owner.text === "React" ? member : null;
}

function checkHostCss(label, filePath, text) {
  const location = `${label}/${filePath}`;
  const errors = [];
  const directives = text.replace(/("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')|\/\*[\s\S]*?\*\//g, (_, quoted) => quoted ?? "");
  for (const match of directives.matchAll(/@source\b([^;]*);/g)) {
    errors.push(`${location}: Host CSS has an unapproved @source: ${match[1].trim()}`);
  }
  for (const { kind, specifier } of parseCssReferences(directives)) {
    if (kind === "@source") continue;
    if (kind === "@import") {
      const target = relativeTarget(filePath, specifier);
      if (target === "ui" || target.startsWith("ui/")) continue;
      errors.push(`${location}: Host CSS may only import portable UI styles: ${specifier}`);
      continue;
    }
    errors.push(`${location}: Host CSS must not reference assets or styles: ${kind} ${specifier}`);
  }
  const residual = directives
    .replace(/@(import|source)\b[^;]*;/g, " ")
    .trim();
  if (residual) {
    const first = residual.split(/\s+/, 1)[0];
    errors.push(`${location}: Host CSS may only bootstrap UI styles, not declare styles: ${first}`);
  }
  return errors;
}

function isOutletOwnedView(path, specifier) {
  return specifier?.startsWith(".") && OUTLET_OWNED_VIEWS.has(
    relativeTarget(path, specifier).replace(/\.[cm]?[jt]sx?$/, ""),
  );
}

function importsRuntime(ts, clause) {
  if (!clause) return true;
  if (clause.isTypeOnly) return false;
  if (clause.name) return true;
  const bindings = clause.namedBindings;
  return bindings && (!ts.isNamedImports(bindings) ||
    bindings.elements.some((item) => !item.isTypeOnly));
}

export function checkHostBoundaries(label, files) {
  const ts = loadTypeScript();
  const errors = [];
  for (const { path, text } of files) {
    if (!isHostPath(path)) continue;
    if (path.endsWith(".css")) {
      errors.push(...checkHostCss(label, path, text));
      continue;
    }
    if (!CODE_FILE.test(path)) continue;
    const source = ts.createSourceFile(
      path,
      text,
      ts.ScriptTarget.Latest,
      true,
      scriptKind(ts, path),
    );
    const bindings = collectHostBindings(ts, source);
    const rejectOutletBypass = (specifier) => {
      if (isOutletOwnedView(path, specifier)) {
        errors.push(`${label}/${path}: Use the portable route outlet instead of ${specifier}`);
      }
    };
    for (const statement of source.statements) {
      if (ts.isImportDeclaration(statement) && importsRuntime(ts, statement.importClause)) {
        rejectOutletBypass(statement.moduleSpecifier.text);
      }
      if (!ts.isExportDeclaration(statement) || statement.isTypeOnly) continue;
      const specifier = statement.moduleSpecifier?.text;
      const named = statement.exportClause && ts.isNamedExports(statement.exportClause);
      const members = named
        ? statement.exportClause.elements
            .filter((item) => !item.isTypeOnly)
            .map((item) => (item.propertyName ?? item.name).text)
        : [];
      const external = specifier && !specifier.startsWith(".");
      if (!named || members.length) rejectOutletBypass(specifier);
      const forbidden = external
        ? !named || members.some((member) => !headlessJsxMember({ specifier }, member))
        : !specifier && members.some((member) => {
            const binding = bindings.get(member);
            return binding?.specifier && !binding.specifier.startsWith(".") &&
              !headlessJsxMember(binding, binding.imported);
          });
      if (forbidden) {
        errors.push(`${label}/${path}: Host must not re-export external runtime components`);
      }
    }
    const visit = (node) => {
      if (
        ts.isJsxElement(node) ||
        ts.isJsxSelfClosingElement(node) ||
        ts.isJsxFragment(node)
      ) {
        hostJsxNodeErrors(ts, label, path, node, bindings, errors);
      } else if (ts.isCallExpression(node)) {
        if (
          (node.expression.kind === ts.SyntaxKind.ImportKeyword ||
            (ts.isIdentifier(node.expression) && node.expression.text === "require")) &&
          node.arguments[0] && ts.isStringLiteralLike(node.arguments[0])
        ) {
          rejectOutletBypass(node.arguments[0].text);
        }
        const factory = hostElementFactory(ts, node.expression, bindings);
        if (factory) {
          errors.push(
            `${label}/${path}: Host JSX must not bypass the parser with an element factory: ${factory}`,
          );
        }
        const member = factoryMember(ts, node.expression);
        if (member === "focus" || member === "scrollIntoView" || member === "insertAdjacentHTML") {
          errors.push(`${label}/${path}: DOM presentation interaction belongs in UI: ${member}`);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  return [...new Set(errors)];
}

export function collectUiTree(root) {
  const dir = fileURLToPath(root);
  if (lstatSync(dir).isSymbolicLink()) {
    throw new Error(`Source root is a symlink: ${dir}`);
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

function hostTreeErrors(label, tree, root) {
  return [
    ...tree.symlinks
      .filter(isHostPath)
      .map((path) => `Cross host symlink is not allowed: ${label}/${path}`),
    ...checkHostBoundaries(label, textSources(root, hostSourceFiles(tree.files))),
  ];
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

function checkEntryStyleOwnership(html) {
  const classes = [...html.matchAll(/\bclass\s*=\s*(["'])(.*?)\1/gs)]
    .map((match) => match[2]);
  return classes.length === 2 &&
    classes.includes("__DOCUMENT_ROOT_CLASS__") &&
    classes.includes("__DOCUMENT_BODY_CLASS__") &&
    !/(?:<style\b|\sstyle\s*=)/i.test(html);
}

function main() {
  const receipts = JSON.parse(
    readFileSync(new URL("./ui-parity-prototype-leads.json", import.meta.url), "utf8"),
  );
  let prototypeTree;
  let applicationTree;
  let prototypeSourceTree;
  let applicationSourceTree;
  try {
    prototypeTree = collectUiTree(prototypeUi);
    applicationTree = collectUiTree(applicationUi);
    prototypeSourceTree = collectUiTree(prototypeSrc);
    applicationSourceTree = collectUiTree(applicationSrc);
  } catch (error) {
    console.error(`Cannot read source tree: ${error.message}`);
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
  let dependencyErrors;
  try {
    dependencyErrors = compareDependencyVersions(
      JSON.parse(
        readFileSync(
          new URL("../docs/app-design-react/package-lock.json", import.meta.url),
          "utf8",
        ),
      ),
      JSON.parse(
        readFileSync(new URL("../frontend/package-lock.json", import.meta.url), "utf8"),
      ),
    );
  } catch (error) {
    dependencyErrors = [`Cannot compare dependency locks: ${error.message}`];
  }
  const errors = [
    ...result.errors,
    ...dependencyErrors,
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
    ...hostTreeErrors(prototypeHostLabel, prototypeSourceTree, prototypeSrc),
    ...hostTreeErrors(applicationHostLabel, applicationSourceTree, applicationSrc),
  ];
  for (const [designPath, applicationPath] of [
    ["app/navigation/operationsLocation.ts", "app/navigation/operationsLocation.ts"],
    ["demo/providers/incoming/incomingGroupsRoute.ts", "providers/incoming/incomingGroupsRoute.ts"],
  ]) {
    const designCodec = readFileSync(new URL(designPath, prototypeSrc));
    const applicationCodec = readFileSync(new URL(applicationPath, applicationSrc));
    if (!designCodec.equals(applicationCodec)) {
      errors.push(`Different host route codec: ${applicationPath}`);
    }
  }
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
  if (![prototypeEntry, applicationEntry].every(checkEntryStyleOwnership)) {
    errors.push("Entry HTML must use portable document presentation placeholders");
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
