package io

// ResourceVersionMissing is returned if an object is missing a resource version
type ResourceVersionMissing struct {
}

func (r ResourceVersionMissing) Error() string {
	return "cannot use optimistic lock, object missing resource version"
}
