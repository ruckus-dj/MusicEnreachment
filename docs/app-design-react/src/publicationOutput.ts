import type { SourceFile } from "./catalogData";

export type PublicationOutputFormat = "mka" | "original";
export type PublicationDestination = {
	outputDirectory: string;
	format: PublicationOutputFormat;
};
/** Configured fixture values, not application defaults. */
export const fixturePublicationDestination: PublicationDestination = {
	outputDirectory: "/srv/music",
	format: "mka",
};

export type PublicationOutputOptions = {
	outputDirectory: string;
	format: PublicationOutputFormat;
	artist: string;
	year: string;
	release: string;
	position: number;
	title: string;
};

export type PublicationOutput = {
	path: string;
	container: string;
};

/** Chooses a path for a newly created publication; existing publications are untouched. */
export function choosePublicationOutput(
	source: Pick<SourceFile, "path" | "name">,
	options: PublicationOutputOptions,
): PublicationOutput {
	const extensionOf = (value: string) => {
		const name = value.split(/[\\/]/).at(-1) ?? "";
		const dot = name.lastIndexOf(".");
		return dot > 0 ? name.slice(dot) : "";
	};
	const sourceExtension = extensionOf(source.path) || extensionOf(source.name);
	const extension = options.format === "mka" ? ".mka" : sourceExtension;
	const container =
		options.format === "mka" ? "MKA" : sourceExtension.slice(1).toUpperCase();
	const releaseDirectory = `${options.year} - ${options.release}`;
	const fileName = `${String(options.position).padStart(2, "0")} - ${options.title}${extension}`;
	const separator = options.outputDirectory.includes("\\") ? "\\" : "/";

	return {
		path: [
			options.outputDirectory.replace(/[\\/]+$/, ""),
			options.artist,
			releaseDirectory,
			fileName,
		].join(separator),
		container,
	};
}
