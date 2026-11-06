type ParsedPath = { root: string; parts: string[]; caseSensitive: boolean };

function parsePath(path: string): ParsedPath | null {
	const value = path.trim().replace(/\\/g, "/");
	let root = "";
	let remainder = value;
	let caseSensitive = true;
	const drive = /^([a-z]:)(?:\/|$)/i.exec(value);
	const unc = /^\/\/([^/]+)\/([^/]+)(?:\/|$)/.exec(value);
	if (drive) {
		root = drive[1].toLowerCase();
		remainder = value.slice(drive[0].length);
		caseSensitive = false;
	} else if (unc) {
		root = `//${unc[1].toLowerCase()}/${unc[2].toLowerCase()}`;
		remainder = value.slice(unc[0].length);
		caseSensitive = false;
	} else if (value.startsWith("/")) {
		root = "/";
		remainder = value.replace(/^\/+/, "");
	} else {
		return null;
	}

	const parts: string[] = [];
	for (const part of remainder.split("/")) {
		if (!part || part === ".") continue;
		if (part === "..") parts.pop();
		else parts.push(caseSensitive ? part : part.toLowerCase());
	}
	return { root, parts, caseSensitive };
}

function contains(parent: string[], child: string[]) {
	return (
		parent.length <= child.length &&
		parent.every((part, i) => part === child[i])
	);
}

/** Lexical demo-fixture comparison only; this does not resolve symlinks or touch a filesystem. */
export function demoPathsOverlap(first: string, second: string): boolean {
	const a = parsePath(first);
	const b = parsePath(second);
	if (!a || !b || a.root !== b.root) return false;
	return contains(a.parts, b.parts) || contains(b.parts, a.parts);
}

export function overlapsAnyDemoPath(path: string, others: string[]): boolean {
	return (
		Boolean(path.trim()) &&
		others.some((other) => demoPathsOverlap(path, other))
	);
}
