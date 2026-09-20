package main

import (
	"reflect"
	"testing"
)

func TestParseConcurrencies(t *testing.T) {
	got, err := parseConcurrencies("4, 1,2,2", 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 2, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, err := parseConcurrencies("1,5", 4); err == nil {
		t.Fatal("expected max-active validation error")
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	values := make([]int64, 20)
	for index := range values {
		values[index] = int64(index + 1)
	}
	if got := percentile(values, 0.50); got != 10 {
		t.Fatalf("p50=%d", got)
	}
	if got := percentile(values, 0.95); got != 19 {
		t.Fatalf("p95=%d", got)
	}
	if got := percentile([]int64{1, 2}, 0.95); got != 2 {
		t.Fatalf("two-sample p95=%d", got)
	}
}
