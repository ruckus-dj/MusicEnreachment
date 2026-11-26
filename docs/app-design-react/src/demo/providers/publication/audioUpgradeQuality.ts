import type { SourceFile } from "../library/catalogData";
import {
	demoIsLosslessCodec,
	type PublicationDestination,
} from "./publicationOutput";

type AudioFacts = {
	lossless: boolean | null;
	codec: string;
	bits: number | null;
	sampleRate: number | null;
	bitrate: number | null;
};

type QualityComparison =
	| { better: true }
	| {
			better: false;
			notice:
				| "matchingReceipt.qualityNotBetter"
				| "matchingReceipt.qualityUnavailable"
				| "matchingReceipt.transcodeNotProven";
	  };

const better: QualityComparison = { better: true };
const notBetter: QualityComparison = {
	better: false,
	notice: "matchingReceipt.qualityNotBetter",
};
const unmeasured: QualityComparison = {
	better: false,
	notice: "matchingReceipt.qualityUnavailable",
};

function measured(value: number | null | undefined): number | null {
	return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function audioFacts(value: string, source?: SourceFile): AudioFacts {
	const codec = value.trim().toUpperCase();
	return {
		codec,
		lossless: codec ? demoIsLosslessCodec(codec) : null,
		bits: measured(source?.bits),
		sampleRate: measured(source?.sampleRate),
		bitrate: measured(source?.bitrate),
	};
}

function compare(
	candidate: AudioFacts,
	current: AudioFacts,
): QualityComparison {
	if (candidate.lossless === null || current.lossless === null)
		return unmeasured;
	if (candidate.lossless !== current.lossless)
		return candidate.lossless ? better : notBetter;
	if (candidate.lossless) {
		if (candidate.bits === null || current.bits === null) return unmeasured;
		if (candidate.bits !== current.bits)
			return candidate.bits > current.bits ? better : notBetter;
		if (candidate.sampleRate === null || current.sampleRate === null)
			return unmeasured;
		return candidate.sampleRate > current.sampleRate ? better : notBetter;
	}
	if (
		candidate.codec !== current.codec ||
		candidate.bitrate === null ||
		current.bitrate === null
	)
		return unmeasured;
	return candidate.bitrate > current.bitrate ? better : notBetter;
}

/** Both the analyzed source and the planned audio must beat CURRENT. */
export function compareDemoAudioUpgrade(
	source: SourceFile,
	currentCodec: string,
	currentSource: SourceFile | undefined,
	format: PublicationDestination["format"],
): QualityComparison {
	const input = audioFacts(source.codec, source);
	const current = audioFacts(currentCodec, currentSource);
	if (format === "m4a" && input.lossless === false)
		return { better: false, notice: "matchingReceipt.transcodeNotProven" };
	const sourceComparison = compare(input, current);
	if (!sourceComparison.better || format !== "m4a") return sourceComparison;
	// Lossless ALAC conversion preserves measured depth and sample rate.
	return compare({ ...input, codec: "ALAC", bitrate: null }, current);
}
