package errors

import "k8s.io/apimachinery/pkg/api/errors"

// An ErrorIs function returns true if an error satisfies a particular condition.
type ErrorIs func(err error) bool

// Ignore any errors that satisfy the supplied ErrorIs function by returning
// nil. Errors that do not satisfy the supplied function are returned unmodified.
func Ignore(is ErrorIs, err error) error {
	if is(err) {
		return nil
	}
	return err
}

// IgnoreAlreadyExists returns the given error or nil if the error indicates that a
// resource already exists.
func IgnoreAlreadyExists(err error) error {
	return Ignore(errors.IsAlreadyExists, err)
}
