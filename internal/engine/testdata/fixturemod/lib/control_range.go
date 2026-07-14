package lib

func RangeOnce(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func RangeEmpty(values []int) {
	for range values {
	}
}
