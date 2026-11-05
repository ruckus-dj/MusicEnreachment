//go:build integration

package persistence_test

import (
	"encoding/json"
	"reflect"
)

// A cover observation outranks tag-only captures regardless of arrival order
// or observation time; a later tag-only result cannot erase the complete capture.

// Equal observation instants are resolved by the greatest capture UUID. Check
// the result identity explicitly: timestamps alone cannot distinguish captures
// after database timestamp precision normalization.

func sameJSON(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
