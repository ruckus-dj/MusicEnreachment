package service

import (
	"reflect"
	"testing"
)

func TestBoundedPublicationOutputCollectorDrainsAndCopiesSnapshot(t *testing.T) {
	if _, err := NewBoundedOutputCollector(0); err == nil {
		t.Fatal("zero output limit accepted")
	}
	collector, err := NewBoundedOutputCollector(3)
	if err != nil {
		t.Fatal(err)
	}
	written, err := collector.Write([]byte("abcdef"))
	if err != nil || written != 6 {
		t.Fatalf("write = %d, %v", written, err)
	}
	output, total, overflow := collector.Snapshot()
	if !reflect.DeepEqual(output, []byte("abc")) || total != 6 || !overflow {
		t.Fatalf("snapshot = %q, %d, %v", output, total, overflow)
	}
	output[0] = 'z'
	second, _, _ := collector.Snapshot()
	if string(second) != "abc" {
		t.Fatalf("snapshot aliases collector: %q", second)
	}
}
