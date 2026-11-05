//go:build integration

package service_test

// A tools-root move only excludes work that reads managed tools. A SHA retry is
// tool-free and remains admissible; it carries no installation hold to pin.
