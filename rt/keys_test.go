package rt

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/karl-tye/ktml"
)

func TestStringKeys_GIVENSmallAndLargeCollections_WHENKeysAdded_THENReportsOnlyDuplicates(t *testing.T) {
	for _, size := range []int{0, 1, smallKeys, smallKeys + 1, 1000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			var keys StringKeys
			keys.Reset(size)
			for index := 0; index < size; index++ {
				if !keys.Add("k" + strconv.Itoa(index)) {
					t.Fatalf("Add(k%d) reported a duplicate", index)
				}
			}
			for index := 0; index < size; index++ {
				if keys.Add("k" + strconv.Itoa(index)) {
					t.Fatalf("Add(k%d) accepted a duplicate", index)
				}
			}
			keys.Reset(1)
			if !keys.Add("k0") {
				t.Fatal("Reset did not clear the set")
			}
		})
	}
}

func TestIntegerKeys_GIVENSmallAndLargeCollections_WHENKeysAdded_THENReportsOnlyDuplicates(t *testing.T) {
	for _, size := range []int{0, 1, smallKeys, smallKeys + 1, 1000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			var keys IntegerKeys
			keys.Reset(size)
			for index := int64(0); index < int64(size); index++ {
				if !keys.Add(index - 3) {
					t.Fatalf("Add(%d) reported a duplicate", index-3)
				}
			}
			for index := int64(0); index < int64(size); index++ {
				if keys.Add(index - 3) {
					t.Fatalf("Add(%d) accepted a duplicate", index-3)
				}
			}
		})
	}
}

var sinkBool bool

func TestKeySets_GIVENAtMostEightKeys_WHENPreflighted_THENDoNotAllocate(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	allocations := testing.AllocsPerRun(100, func() {
		var set StringKeys
		set.Reset(len(keys))
		for _, key := range keys {
			sinkBool = set.Add(key)
		}
		var integers IntegerKeys
		integers.Reset(len(keys))
		for index := range keys {
			sinkBool = integers.Add(int64(index))
		}
	})
	if allocations != 0 {
		t.Errorf("allocations = %v, want 0", allocations)
	}
}

func TestKeyErrors_GIVENDuplicateOrCanceled_WHENBuilt_THENAreTypedWithCanonicalKeys(t *testing.T) {
	var renderError *ktml.RenderError
	err := DuplicateStringKey("example.com/app.Board", "group.items", "42")
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrDuplicateKey || renderError.Component != "example.com/app.Board" || renderError.Input != "group.items" || renderError.Key != `"42"` {
		t.Errorf("DuplicateStringKey() = %#v", err)
	}
	err = DuplicateIntegerKey("example.com/app.Board", "columns", -42)
	if !errors.As(err, &renderError) || renderError.Key != "-42" {
		t.Errorf("DuplicateIntegerKey() = %#v", err)
	}
	if StringKey("42") == IntegerKey(42) {
		t.Error("string and integer keys share an encoding")
	}
	err = Canceled("example.com/app.Board", "columns", context.Canceled)
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrCanceled || !errors.Is(err, context.Canceled) {
		t.Errorf("Canceled() = %#v", err)
	}
}
