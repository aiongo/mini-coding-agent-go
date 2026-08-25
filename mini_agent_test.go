package main

import (
	"slices"
	"testing"
)

func TestRemember(t *testing.T) {
	tests := []struct {
		name   string
		bucket []string
		item   string
		limit  int
		want   []string
	}{
		{"add new to empty", []string{}, "a", 3, []string{"a"}},
		{"add new", []string{"a", "b"}, "c", 3, []string{"a", "b", "c"}},
		{"duplicate moves to end", []string{"a", "b"}, "a", 3, []string{"b", "a"}},
		{"duplicate single element stays", []string{"a"}, "a", 3, []string{"a"}},
		{"re-add after at capacity moves to end", []string{"a", "b", "c"}, "a", 3, []string{"b", "c", "a"}},
		{"limit trims oldest when at capacity", []string{"a", "b", "c"}, "d", 3, []string{"b", "c", "d"}},
		{"limit trims when over capacity", []string{"a", "b", "c", "d"}, "e", 3, []string{"c", "d", "e"}},
		{"limit larger than bucket keeps all", []string{"a", "b"}, "c", 5, []string{"a", "b", "c"}},
		{"limit one keeps only newest", []string{"a", "b"}, "c", 1, []string{"c"}},
		{"empty item is a no-op", []string{"a", "b"}, "", 3, []string{"a", "b"}},
		{"empty item on empty bucket", []string{}, "", 3, []string{}},
		{"order is most-recent-last", []string{"a"}, "b", 3, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Remember(tt.bucket, tt.item, tt.limit)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Remember(%v, %q, %d) = %v, want %v", tt.bucket, tt.item, tt.limit, got, tt.want)
			}
		})
	}
}
