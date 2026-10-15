package service

import "time"

// sourceWalkExtensions holds the exact 13 accepted audio extensions. Extension
// matching is case-insensitive; relative paths retain the spelling on disk.
var sourceWalkExtensions = map[string]struct{}{
	".flac": {}, ".wav": {}, ".aif": {}, ".aiff": {}, ".ape": {}, ".wv": {},
	".mp3": {}, ".m4a": {}, ".aac": {}, ".ogg": {}, ".opus": {}, ".wma": {}, ".mka": {},
}

// SourceWalkEntry is one approved regular file observed during enumeration.
type SourceWalkEntry struct {
	RelativePath string
	SizeBytes    int64
	Mtime        time.Time
}

type SourceWalkVisitor func(SourceWalkEntry) error

const sourceWalkReadBatch = 64
