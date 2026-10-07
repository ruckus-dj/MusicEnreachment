package service

import "errors"

// ErrSourceAnalysisToolUnavailable reports that a managed tool selected by a
// source analysis cannot be used.
var ErrSourceAnalysisToolUnavailable = errors.New("the selected managed ffmpeg installation is unavailable")
