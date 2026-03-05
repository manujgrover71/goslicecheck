package slice

// alreadyPreallocated3Args
func alreadyPreallocated3Args(src []int) []int {
	result := make([]int, 0, len(src))
	for _, v := range src {
		result = append(result, v)
	}
	return result
}

// alreadyPreallocated2Args
func alreadyPreallocated2Args(src []int) []int {
	result := make([]int, len(src))
	for _, v := range src {
		result = append(result, v)
	}
	return result
}

// noAppend
func noAppend(src []int) int {
	sum := 0
	for _, v := range src {
		sum += v
	}
	return sum
}

// mismatchedAppendTarget
func mismatchedAppendTarget(src []int) []int {
	other := []int{1, 2, 3}
	result := []int{}
	for _, v := range src {
		result = append(other, v)
	}
	return result
}

// mapAlreadyPreallocated — should NOT trigger
func mapAlreadyPreallocated(src map[string]int) []string {
	result := make([]string, 0, len(src))
	for k := range src {
		result = append(result, k)
	}
	return result
}
