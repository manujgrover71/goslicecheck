package slice

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
