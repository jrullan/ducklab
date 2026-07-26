package fixture

func Add(a, b int) int {
	return a - b // BUG: should be a + b
}
