package hmr

import (
	"reflect"
	"unsafe"
)

// estimateSize estimates the bytes a props value retains: its own size plus
// the strings, slices, maps, and pointed-to values it reaches. Shared values
// are counted once; functions and channels count as a pointer.
func estimateSize(value any) int {
	seen := make(map[uintptr]bool)
	return sizeOf(reflect.ValueOf(value), seen, 0)
}

const maxSizeDepth = 32

func sizeOf(value reflect.Value, seen map[uintptr]bool, depth int) int {
	if !value.IsValid() || depth > maxSizeDepth {
		return 0
	}
	size := int(value.Type().Size())
	return size + indirectSize(value, seen, depth)
}

// indirectSize is what value reaches beyond its own inline bytes.
func indirectSize(value reflect.Value, seen map[uintptr]bool, depth int) int {
	if depth > maxSizeDepth {
		return 0
	}
	switch value.Kind() {
	case reflect.String:
		return value.Len()
	case reflect.Pointer:
		if value.IsNil() || visited(value.Pointer(), seen) {
			return 0
		}
		return sizeOf(value.Elem(), seen, depth+1)
	case reflect.Interface:
		if value.IsNil() {
			return 0
		}
		return sizeOf(value.Elem(), seen, depth+1)
	case reflect.Slice:
		if value.IsNil() || visited(value.Pointer(), seen) {
			return 0
		}
		size := value.Cap() * int(value.Type().Elem().Size())
		for index := range value.Len() {
			size += indirectSize(value.Index(index), seen, depth+1)
		}
		return size
	case reflect.Array:
		size := 0
		for index := range value.Len() {
			size += indirectSize(value.Index(index), seen, depth+1)
		}
		return size
	case reflect.Map:
		if value.IsNil() || visited(value.Pointer(), seen) {
			return 0
		}
		size := 0
		iterator := value.MapRange()
		for iterator.Next() {
			size += sizeOf(iterator.Key(), seen, depth+1) + sizeOf(iterator.Value(), seen, depth+1)
		}
		return size
	case reflect.Struct:
		size := 0
		for index := range value.NumField() {
			field := value.Field(index)
			if !field.CanInterface() {
				// Unexported fields, such as a sealed state's payload, are
				// read through a copy.
				copied := reflect.New(value.Type()).Elem()
				copied.Set(value)
				field = copied.Field(index)
				field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
			}
			size += indirectSize(field, seen, depth+1)
		}
		return size
	}
	return 0
}

func visited(pointer uintptr, seen map[uintptr]bool) bool {
	if seen[pointer] {
		return true
	}
	seen[pointer] = true
	return false
}
