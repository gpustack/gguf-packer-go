package mapx

import (
	"strings"
)

// FilterFunc filters a map by a custom function,
// the function should return a boolean indicating if the key should be included.
func FilterFunc[K comparable, V any](oldMap map[K]V, cmp func(key K, val V) (ok bool)) (newMap map[K]V) {
	newMap = make(map[K]V)
	for k, v := range oldMap {
		if cmp(k, v) {
			newMap[k] = v
		}
	}
	return newMap
}

// FilterWithPrefix filters a map by a prefix,
// only keys with the prefix will be included.
func FilterWithPrefix[V any](oldMap map[string]V, prefix string) (newMap map[string]V) {
	return FilterFunc(oldMap, func(k string, v V) bool {
		return strings.HasPrefix(k, prefix)
	})
}

// ProjectFunc projects a map by a custom function,
// the function should return the new key and a boolean indicating if the key should be included.
func ProjectFunc[K comparable, V any](oldMap map[K]V, proj func(oldKey K, val V) (newKey K, ok bool)) (newMap map[K]V) {
	newMap = make(map[K]V)
	for k, v := range oldMap {
		if newKey, ok := proj(k, v); ok {
			newMap[newKey] = v
		}
	}
	return newMap
}

// ProjectWithPrefix projects a map by a prefix,
// only keys with the prefix will be included,
// and the prefix will be removed from the keys.
func ProjectWithPrefix[V any](oldMap map[string]V, prefix string) (newMap map[string]V) {
	return ProjectFunc(oldMap, func(k string, v V) (_k string, ok bool) {
		_k = strings.TrimPrefix(k, prefix)
		return _k, _k != k
	})
}
