package lib

func InlineAssertCondition(v any) bool {
	if c, ok := v.(interface{ Close() }); ok {
		c.Close()
		return true
	}
	return false
}
