package gofast

import (
	"fmt"
	"reflect"
)

func isNilable(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return true
	default:
		return false
	}
}

// reflectedError safely extracts an error from a reflect.Value. Concrete
// error structs are not nilable and must not be checked with Value.IsNil.
func reflectedError(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}
	if isNilable(value.Kind()) && value.IsNil() {
		return nil
	}
	err, ok := value.Interface().(error)
	if !ok {
		return fmt.Errorf("reflected value of type %s does not implement error", value.Type())
	}
	return err
}
