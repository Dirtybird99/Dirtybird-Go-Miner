package miner

import (
	"sort"
	"strconv"
	"strings"
)

// pinOrderForThreads returns an all-worker order or nil. Mixing pinned and
// unpinned workers makes a requested pin mode topology-dependent and noisy.
func pinOrderForThreads(order []int, threads int) []int {
	if threads <= 0 || len(order) < threads {
		return nil
	}
	return order[:threads]
}

// linuxPinOrder puts one allowed logical CPU from each physical core first,
// followed by the remaining SMT siblings. Incomplete or inconsistent topology
// falls back to the allowed CPUs in numeric order.
func linuxPinOrder(allowed []int, siblingLists map[int]string) []int {
	allowed = sortedUniqueCPUs(allowed)
	if len(allowed) == 0 {
		return nil
	}

	allowedSet := make(map[int]bool, len(allowed))
	for _, cpu := range allowed {
		allowedSet[cpu] = true
	}

	seenGroups := make(map[string]bool, len(allowed))
	groups := make([][]int, 0, len(allowed))
	for _, cpu := range allowed {
		list, ok := siblingLists[cpu]
		if !ok {
			return allowed
		}
		siblings, ok := parseCPUList(list)
		if !ok {
			return allowed
		}
		filtered := siblings[:0]
		containsCPU := false
		for _, sibling := range siblings {
			if allowedSet[sibling] {
				filtered = append(filtered, sibling)
				containsCPU = containsCPU || sibling == cpu
			}
		}
		if !containsCPU {
			return allowed
		}
		key := cpuListKey(filtered)
		if !seenGroups[key] {
			seenGroups[key] = true
			groups = append(groups, append([]int(nil), filtered...))
		}
	}

	// Reject inconsistent sibling files that overlap or omit an allowed CPU.
	membership := make(map[int]int, len(allowed))
	for _, group := range groups {
		for _, cpu := range group {
			membership[cpu]++
		}
	}
	for _, cpu := range allowed {
		if membership[cpu] != 1 {
			return allowed
		}
	}

	order := make([]int, 0, len(allowed))
	for _, group := range groups {
		order = append(order, group[0])
	}
	for _, group := range groups {
		order = append(order, group[1:]...)
	}
	return order
}

func parseCPUList(list string) ([]int, bool) {
	seen := make(map[int]bool)
	for _, field := range strings.Split(strings.TrimSpace(list), ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, false
		}
		loText, hiText, isRange := strings.Cut(field, "-")
		lo, err := strconv.Atoi(strings.TrimSpace(loText))
		if err != nil || lo < 0 {
			return nil, false
		}
		hi := lo
		if isRange {
			if strings.Contains(hiText, "-") {
				return nil, false
			}
			hi, err = strconv.Atoi(strings.TrimSpace(hiText))
			if err != nil || hi < lo {
				return nil, false
			}
		}
		for cpu := lo; ; cpu++ {
			seen[cpu] = true
			if cpu == hi {
				break
			}
		}
	}
	if len(seen) == 0 {
		return nil, false
	}
	cpus := make([]int, 0, len(seen))
	for cpu := range seen {
		cpus = append(cpus, cpu)
	}
	sort.Ints(cpus)
	return cpus, true
}

func sortedUniqueCPUs(cpus []int) []int {
	seen := make(map[int]bool, len(cpus))
	out := make([]int, 0, len(cpus))
	for _, cpu := range cpus {
		if cpu >= 0 && !seen[cpu] {
			seen[cpu] = true
			out = append(out, cpu)
		}
	}
	sort.Ints(out)
	return out
}

func cpuListKey(cpus []int) string {
	var b strings.Builder
	for _, cpu := range cpus {
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(cpu))
	}
	return b.String()
}
