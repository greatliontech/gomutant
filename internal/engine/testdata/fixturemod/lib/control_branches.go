package lib

func BreakLoop() {
	for {
		break
	}
}

func BreakSwitch(n int) {
	switch n {
	case 0:
		break
	}
}

func BreakSwitchInLoop(n int) {
	for {
		switch n {
		case 0:
			break
		}
		break
	}
}

func BreakSelect() {
	select {
	default:
		break
	}
}

func BreakSelectInLoop() {
	for {
		select {
		default:
			break
		}
		break
	}
}

func BreakLabeledLoop() {
Loop:
	for {
		break Loop
	}
}

func BreakLabeledSwitch(n int) {
Switch:
	switch n {
	case 0:
		break Switch
	}
}

func ContinueLoop(n int) {
	for n > 0 {
		n--
		continue
	}
}

func ContinueLabeled(n int) {
Loop:
	for n > 0 {
		n--
		continue Loop
	}
}

func BreakAcrossFuncBoundary() {
	for {
		_ = func() {
			switch {
			default:
				break
			}
		}
		break
	}
}
