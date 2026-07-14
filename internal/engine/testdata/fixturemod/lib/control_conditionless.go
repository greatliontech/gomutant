package lib

func SemicolonStringCondition() {
	for value := ";"; ; value = "" {
		_ = value
		break
	}
}

func SemicolonCommentCondition() {
	for value := 0; ; /* ; */ value++ {
		break
	}
}

func ConditionlessOnly() {
	for {
		return
	}
}
