package metadata

import (
	"errors"
	"fmt"
	"strings"

	"go.senan.xyz/taglib"
)

// TagLib's MPEG normalized writer serializes these five fields as comma-joined
// TIPL values. Store them as individual TXXX descriptions instead, which the
// normalized reader exposes as distinct keys and values.
var mpegContributorAliases = map[string]string{
	"ARRANGER": "TIPL:ARRANGER",
	"ENGINEER": "TIPL:ENGINEER",
	"PRODUCER": "TIPL:PRODUCER",
	"DJMIXER":  "TIPL:DJ-MIX",
	"MIXER":    "TIPL:MIX",
}

func normalizeContributorTags(tags map[string][]string) (map[string][]string, error) {
	result := cloneTags(tags)
	for canonical, alias := range mpegContributorAliases {
		if _, aliasPresent := result[alias]; aliasPresent {
			if _, canonicalPresent := result[canonical]; canonicalPresent {
				return nil, fmt.Errorf("metadata tag %q conflicts with its native alias", canonical)
			}
		}
		values, aliasPresent := result[alias]
		if aliasPresent {
			result[canonical] = append([]string{}, values...)
			delete(result, alias)
		}
	}
	return result, nil
}

func validateContributorAliases(tags map[string][]string) error {
	for canonical, alias := range mpegContributorAliases {
		if _, canonicalPresent := tags[canonical]; canonicalPresent {
			if _, aliasPresent := tags[alias]; aliasPresent {
				return fmt.Errorf("metadata tag %q conflicts with its native alias", canonical)
			}
		}
	}
	return nil
}

func validateTagTransport(tags map[string][]string) error {
	if err := validateContributorAliases(tags); err != nil {
		return err
	}
	for key, values := range tags {
		if strings.ContainsAny(key, "\x00\v") {
			return errors.New("metadata tag key contains unsupported control characters")
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\x00\v") {
				return fmt.Errorf("metadata tag %q contains unsupported control characters", key)
			}
		}
	}
	return nil
}

func writeNormalizedTagLib(path string, tags map[string][]string, option taglib.WriteOption) error {
	if err := validateTagTransport(tags); err != nil {
		return err
	}
	file, err := taglib.OpenReadOnly(path)
	if err != nil {
		return fmt.Errorf("detect audio format for metadata write: %w", err)
	}
	format := file.Format()
	if err := file.Close(); err != nil {
		return fmt.Errorf("close audio format probe: %w", err)
	}
	if format != taglib.FormatMPEG {
		return taglib.WriteTags(path, tags, option)
	}

	translated := cloneTags(tags)
	for canonical, alias := range mpegContributorAliases {
		values, present := translated[canonical]
		if !present {
			continue
		}
		delete(translated, canonical)
		translated[alias] = append([]string{}, values...)
	}
	return taglib.WriteTags(path, translated, option)
}
