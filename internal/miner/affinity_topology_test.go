package miner

import (
	"reflect"
	"testing"
)

func TestPinOrderForThreadsIsAllOrNothing(t *testing.T) {
	available := []int{1, 5}
	if got := pinOrderForThreads(available, 3); got != nil {
		t.Fatalf("pinOrderForThreads() = %v, want nil for partial pin", got)
	}
	if got := pinOrderForThreads(available, 1); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("pinOrderForThreads() = %v, want lower-thread run pinned", got)
	}
}

func TestLinuxPinOrder(t *testing.T) {
	tests := []struct {
		name     string
		allowed  []int
		siblings map[int]string
		want     []int
	}{
		{
			name:    "physical cores before SMT",
			allowed: []int{13, 1, 9, 5},
			siblings: map[int]string{
				1: "1,9\n", 9: "1,9", 5: "5,13", 13: "5,13",
			},
			want: []int{1, 5, 9, 13},
		},
		{
			name:    "restricted cpuset filters unavailable siblings",
			allowed: []int{13, 9},
			siblings: map[int]string{
				9: "1,9", 13: "5,13",
			},
			want: []int{9, 13},
		},
		{
			name:    "asymmetric cores",
			allowed: []int{10, 6, 2},
			siblings: map[int]string{
				2: "2,10", 6: "6", 10: "2,10",
			},
			want: []int{2, 6, 10},
		},
		{
			name:    "incomplete topology falls back",
			allowed: []int{9, 1, 5},
			siblings: map[int]string{
				1: "1,9", 9: "1,9",
			},
			want: []int{1, 5, 9},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linuxPinOrder(tt.allowed, tt.siblings); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("linuxPinOrder() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCPUList(t *testing.T) {
	got, ok := parseCPUList("0-2, 7, 12-14,13\n")
	want := []int{0, 1, 2, 7, 12, 13, 14}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCPUList() = %v, %v, want %v, true", got, ok, want)
	}
	for _, bad := range []string{"", "3-1", "1,,2", "x", "-1"} {
		if got, ok := parseCPUList(bad); ok {
			t.Errorf("parseCPUList(%q) = %v, true, want failure", bad, got)
		}
	}
}
