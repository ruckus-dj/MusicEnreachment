package publication

import "strings"

var sourceEncoderTagNames = []string{"ENCODEDBY", "ENCODING", "ENCODERSETTINGS"}

// ProjectSourceEncoderTags retains the selected source's encoder descriptions
// only when its audio stream is copied. Existing descriptions are replaced so
// a source republish cannot accidentally retain descriptions from older audio.
func ProjectSourceEncoderTags(tags, sourceTags map[string][]string, action AudioCodecAction) map[string][]string {
	result := clonePublicationTags(tags)
	for key := range result {
		if isSourceEncoderTag(key) {
			delete(result, key)
		}
	}
	if action != AudioCodecCopy {
		return result
	}
	for key, values := range sourceTags {
		if canonical, ok := canonicalSourceEncoderTag(key); ok {
			result[canonical] = append([]string{}, values...)
		}
	}
	return result
}

func clonePublicationTags(tags map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(tags))
	for key, values := range tags {
		cloned[key] = append([]string{}, values...)
	}
	return cloned
}

func isSourceEncoderTag(key string) bool {
	_, ok := canonicalSourceEncoderTag(key)
	return ok
}

func canonicalSourceEncoderTag(key string) (string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	for _, name := range sourceEncoderTagNames {
		if normalized == name {
			return name, true
		}
	}
	return "", false
}
