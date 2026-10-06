package service

import "errors"

// ErrSourceAnalysisToolUnavailable reports the managed FFmpeg installation an
// analysis snapshot pins cannot be used: it is missing, not ready, built for
// another platform, carries an invalid managed path, or fails its version query.
var ErrSourceAnalysisToolUnavailable = errors.New("the selected managed ffmpeg installation is unavailable")
