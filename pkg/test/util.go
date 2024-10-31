package test

import (
	"fmt"
	"os"
	"path"
	"strconv"

	"golang.org/x/tools/go/packages"
	"k8s.io/apimachinery/pkg/util/rand"
)

// ModuleDir returns the absolute path for the module containing the provided package.
func ModuleDir(pkg string, rootDir string) (string, error) {
	cfg := &packages.Config{
		Mode: packages.NeedModule,
		Dir:  rootDir,
	}

	loaded, err := packages.Load(cfg, pkg)
	if err != nil {
		return "", fmt.Errorf("loading package info: %w", err)
	}

	if len(loaded) == 0 {
		return "", fmt.Errorf("could not find package")
	}

	found := loaded[0]
	if found.Module == nil {
		return "", fmt.Errorf("found module is nil: %v", found.Errors)
	}

	return found.Module.Dir, nil
}

// ExternalCRDDirectoryPaths returns a list of paths of CRD directories given a mapping of pkg names to relative CRD directories
func ExternalCRDDirectoryPaths(pkgToCRDDir map[string][]string, rootDir string) ([]string, error) {
	var paths []string
	for pkgName, crdDirs := range pkgToCRDDir {
		pkgDir, err := ModuleDir(pkgName, rootDir)
		if err != nil {
			return nil, err
		}
		for _, crdDir := range crdDirs {
			paths = append(paths, path.Join(pkgDir, crdDir))
		}
	}
	return paths, nil
}

func MustParseBool(envVarName string) bool {
	v, _ := strconv.ParseBool(os.Getenv(envVarName))
	return v
}

func GenerateRandomString(length int) string {
	return rand.String(length)
}
