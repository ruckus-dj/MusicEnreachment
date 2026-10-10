package textsimilarity

/*
#cgo CXXFLAGS: -std=c++17 -I${SRCDIR}/third_party
#cgo linux LDFLAGS: -lstdc++
#cgo darwin LDFLAGS: -lc++
#cgo windows LDFLAGS: -Wl,-Bstatic -lstdc++ -lwinpthread -Wl,-Bdynamic -static-libgcc
#include <stdint.h>
#include <stdlib.h>

int rapidfuzz_token_set_ratio(const uint32_t* left, size_t left_len,
                              const uint32_t* right, size_t right_len,
                              double* score);
*/
import "C"

import (
	"errors"
	"math"
)

// TokenSetSimilarity returns RapidFuzz's token_set_ratio normalized to [0, 1].
// Inputs are Unicode code points, not UTF-8 bytes.
func TokenSetSimilarity(left, right []rune) (float64, error) {
	if len(left) == 0 && len(right) == 0 {
		return 0, nil
	}

	leftNative := make([]C.uint32_t, len(left))
	for i, character := range left {
		leftNative[i] = C.uint32_t(character)
	}
	rightNative := make([]C.uint32_t, len(right))
	for i, character := range right {
		rightNative[i] = C.uint32_t(character)
	}

	var score C.double
	var leftPointer, rightPointer *C.uint32_t
	if len(leftNative) > 0 {
		leftPointer = &leftNative[0]
	}
	if len(rightNative) > 0 {
		rightPointer = &rightNative[0]
	}
	status := C.rapidfuzz_token_set_ratio(leftPointer, C.size_t(len(leftNative)), rightPointer, C.size_t(len(rightNative)), &score)
	if status != 0 {
		return 0, errors.New("rapidfuzz token-set comparison failed")
	}
	value := float64(score) / 100
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return 0, errors.New("rapidfuzz token-set comparison returned an invalid score")
	}
	return value, nil
}
