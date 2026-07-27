package sdk

import (
	"encoding/json"
	"fmt"
)

// coerceList normalises a generated oneOf payload into the concrete entity slice.
//
// A v4 collection can answer with either the full entity or a "projection" of
// it, and the generated response wrapper picks between the two by looking at the
// $objectType of the first record. The projection carries the same fields, so
// re-encoding it into the full type keeps a single conversion path instead of a
// second set of converters that would drift.
func coerceList[T any](v any) ([]T, error) {
	if v == nil {
		return nil, nil
	}
	if typed, ok := v.([]T); ok {
		return typed, nil
	}
	var out []T
	if err := reencode(v, &out); err != nil {
		return nil, fmt.Errorf("read %T as %T: %w", v, out, err)
	}
	return out, nil
}

// coerceOne is coerceList for a single-entity response.
func coerceOne[T any](v any) (T, error) {
	var out T
	if v == nil {
		return out, fmt.Errorf("server returned no data")
	}
	if typed, ok := v.(T); ok {
		return typed, nil
	}
	if err := reencode(v, &out); err != nil {
		return out, fmt.Errorf("read %T as %T: %w", v, out, err)
	}
	return out, nil
}

func reencode(from, into any) error {
	raw, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// guard turns a panic inside a generated client into an ordinary error.
//
// Several of the generated list unmarshallers dereference the $objectType of the
// first record without checking it for nil, so a response that omits the field
// takes the whole process down. A tool that migrates production subnets must not
// die that way part-way through a run, so every SDK call goes through here.
func guard(operation string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: the Nutanix SDK client could not read the server response (%v)", operation, r)
		}
	}()
	return fn()
}
