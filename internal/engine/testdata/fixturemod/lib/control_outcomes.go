package lib

func ConditionlessOutcome(done *bool) {
	for {
		*done = true
		return
	}
}

func BreakValue() int {
	total := 0
	for _, n := range []int{1, 2} {
		if n == 1 {
			break
		}
		total += n
	}
	return total
}

func ContinueValue() int {
	total := 0
	for _, n := range []int{1, 2} {
		if n == 1 {
			continue
		}
		total += n
	}
	return total
}
