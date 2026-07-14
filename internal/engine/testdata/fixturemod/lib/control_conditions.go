package lib

func IfCondition(n int) bool {
	if n > 0 {
		return true
	}
	return false
}

func ForCondition(n int) int {
	for n < 1 {
		return 1
	}
	return n
}

func Forever() int {
	n := 0
	for {
		n++
		break
	}
	return n
}

func MissingCondition() {
	for i := 0; ; i++ {
		break
	}
}

func SwitchTag(n int) {
	switch n > 0 {
	case true:
	}
}
