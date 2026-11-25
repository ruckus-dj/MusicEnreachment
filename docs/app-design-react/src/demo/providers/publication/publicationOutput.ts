import type { SourceFile } from "../library/catalogData";
import { demoPathInside } from "../sources/demoPathValidation";

export type PublicationOutputFormat = "mka" | "m4a" | "original";
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

/** Simulate the supported output codecs for the prototype's source fixtures. */
export function demoOutputCodec(
	sourceCodec: string,
	format: PublicationOutputFormat,
) {
	if (format !== "m4a") return sourceCodec;
	switch (sourceCodec.toUpperCase()) {
		case "ALAC":
		case "FLAC":
			return "ALAC";
		default:
			return "AAC";
	}
}

export function demoIsLosslessCodec(codec: string | undefined) {
	return codec === "FLAC" || codec === "ALAC";
}

function extensionOf(value: string) {
	const name = value.split(/[\\/]/).at(-1) ?? "";
	const dot = name.lastIndexOf(".");
	return dot > 0 ? name.slice(dot) : "";
}

function outputExtension(
	source: Pick<SourceFile, "path" | "name">,
	format: PublicationOutputFormat,
) {
	return format === "original"
		? extensionOf(source.path) || extensionOf(source.name)
		: `.${format}`;
}

export function retainedOutputIssue(
	current: { path: string; container: string },
	source: Pick<SourceFile, "path" | "name">,
	destination: PublicationDestination,
) {
	if (!demoPathInside(destination.outputDirectory, current.path))
		return "currentOutsideOutput";
	const expected = outputExtension(source, destination.format).toLowerCase();
	if (
		!expected ||
		extensionOf(current.path).toLowerCase() !== expected ||
		current.container.toLowerCase() !== expected.slice(1)
	)
		return "currentFormatIncompatible";
	return undefined;
}

/** Chooses a path for a newly created publication; existing publications are untouched. */
export function choosePublicationOutput(
	source: Pick<SourceFile, "path" | "name">,
	options: PublicationOutputOptions,
): PublicationOutput {
	const extension = outputExtension(source, options.format);
	const container = extension.slice(1).toUpperCase();
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
