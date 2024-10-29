package tests

import (
	"path/filepath"
	"runtime"
)

// RootDir returns the root directory of the project
func RootDir() string {
	_, b, _, _ := runtime.Caller(0)
	// this file is located at pkg/internal/tests, which is three directories from the root
	return filepath.Join(filepath.Dir(b), "../../..")
}
