package tools

import (
	"errors"
	"os"
)

// FileCommandRequest describes a command that reads an already-open file.
// Input is borrowed: neither request construction nor command configuration
// closes it. The caller owns its lifetime. A duplicated descriptor shares the
// underlying file offset; no offset isolation is promised.
type FileCommandRequest struct {
	Executable string
	Args       []string
	Input      *os.File
}

// BuildPublicationFileCommandRequest probes only the explicitly selected
// publication container. Demuxer names are a closed allowlist.
func BuildPublicationFileCommandRequest(executable, demuxer string, input *os.File) (FileCommandRequest, error) {
	switch demuxer {
	case "mp3", "flac", "wav", "ogg", "aac", "mov", "matroska":
	default:
		return FileCommandRequest{}, errors.New("publication probe demuxer is unsupported")
	}
	args := []string{"-v", "error", "-protocol_whitelist", "fd", "-fd", fileTransportDescriptor()}
	if demuxer == "mov" {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	args = append(args, "-f", demuxer, "-show_format", "-show_streams", "-show_chapters", "-of", "json", "fd:")
	return FileCommandRequest{Executable: executable, Args: args, Input: input}, nil
}
