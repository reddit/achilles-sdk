package json

/*

This package is forked from Go SDK 1.17.2.
Source: https://github.com/golang/go/tree/go1.17.2/src/encoding/json

Modifications:

1. Modify JSON marshalling to distinguish between nil and empty values for Arrays, Slices, and Maps.
  - Modification lives on L344-347 of encode.go
  - This modification allows Go structs with Array/Slice/Map fields with the `omitempty` tag to be serialized
    as the empty value when the Go value is empty. Nil values tagged with `omitempty` follow standard behavior
    and are not serialized into the resulting output.
  - This is needed for performing Kubernetes patch operations using JSON merge semantics so that Array/Slice/Map
    fields can be deleted.

2. Removed `bench_test.go` to avoid bringing in other internal package dependencies.
*/
