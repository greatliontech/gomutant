package lib

func StatementBlocks(number int, channel chan int) {
	if number > 0 {
		sink()
		number++
	} else {
		sink()
	}
	switch number {
	case 0:
		sink()
	default:
	}
	switch value := any(number).(type) {
	case int:
		_ = value
		sink()
	}
	select {
	case channel <- number:
		sink()
	default:
	}
	for number > 0 {
		sink()
		number--
	}
}

func StatementKinds(channel chan int, function func(), number int) {
	sink()
	number++
	channel <- number
	go function()
	defer function()
	return
}

func StatementDropStores(values []int, next func() int) int {
	left, right := 0, 0
	left, right = next(), next()
	values[next()] += next()
	for left = next(); left < right; left = next() {
	}
	if left = next(); left > 0 {
	}
	switch left = next(); left {
	}
	return right
}

func StatementExcluded() int {
	var value int
	short := value
	return short
}

func StatementDeletionKinds(values []int) {
outer:
	for _, value := range values {
		if value < 0 {
			continue
		}
		break outer
	}
	{
		sink()
	}
}

func StatementLabeledAssignment(next func() int) int {
	value := 0
assignment:
	value = next()
	if value < 0 {
		goto assignment
	}
	return value
}

func StatementAssignmentBoundaries(channel chan int, next func() int) int {
	value := 0
	switch value = next(); any(value).(type) {
	default:
		value = next()
	}
	select {
	case value = <-channel:
	case <-channel:
		value = next()
	default:
	}
	return value
}
