package lib

func SemicolonNestedCondition() {
	for function := func() { println(); }; ; {
		_ = function
		break
	}
}
