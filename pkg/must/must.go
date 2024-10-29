package must

// Do runs fn and panics if it returns an error.
// The panic value is the return value from fn.
func Do(fn func() error) {
	if err := fn(); err != nil {
		panic(err)
	}
}
