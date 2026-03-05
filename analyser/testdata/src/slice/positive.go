package slice

// slice cases:
// shortDecl
func shortDecl(src []int) []int {
	result := []int{}
	for _, v := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, v*2)
	}
	return result
}

// varDecl
func varDecl(src []int) []int {
	var result []int
	for _, v := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, v+1)
	}
	return result
}

// makeNoCapacity
func makeNoCapacity(src []int) []int {
	result := make([]int, 0)
	for _, v := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, v)
	}
	return result
}

// multipleLoops
func multipleLoops(src []int) ([]int, []int) {
	result1 := []int{}
	for _, v := range src { // want `slice 'result1' can be preallocated with capacity len\(src\)`
		result1 = append(result1, v)
	}

	result2 := []int{}
	for _, v := range result1 { // want `slice 'result2' can be preallocated with capacity len\(result1\)`
		result2 = append(result2, v*2)
	}

	return result1, result2
}

// Map cases:
// mapRangeKey
func mapRangeKey(src map[string]int) []string {
	result := []string{}
	for k := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, k)
	}
	return result
}

// mapRangeValue
func mapRangeValue(src map[string]int) []int {
	result := []int{}
	for _, v := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, v)
	}
	return result
}

// mapRangeKeyValue
func mapRangeKeyValue(src map[string]int) []string {
	result := []string{}
	for k, _ := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, k)
	}
	return result
}

// mapVarDecl
func mapVarDecl(src map[string]int) []string {
	var result []string
	for k := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, k)
	}
	return result
}

// mapNamedType
type UserMap map[string]int

func mapNamedType(src UserMap) []string {
	result := []string{}
	for k := range src { // want `slice 'result' can be preallocated with capacity len\(src\)`
		result = append(result, k)
	}
	return result
}
