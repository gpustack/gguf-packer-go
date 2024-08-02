package ptr

func To[T any](v T) *T {
	return &v
}

func From[T any](ptr *T, def T) T {
	if ptr != nil {
		return *ptr
	}
	return def
}

func Equal[T comparable](a, b *T) bool {
	if a != nil && b != nil {
		return *a == *b
	}
	return false
}
