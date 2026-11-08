package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const module = "moodle-autograder/"

// allowed says what a layer may import from this module: dependencies point inward, use cases do not call
// each other, adapters implement the ports of app, delivery calls app and never an adapter, and the two ways in (HTTP
// and Temporal) know nothing of each other. An entry ending in "/" is a prefix, any other one an exact package. Tests
// may import anything.
var allowed = map[string][]string{
	"domain":             {"internal/domain/"},
	"app":                {"internal/domain/", "internal/app"},
	"adapters":           {"internal/domain/", "internal/app", "internal/app/", "internal/adapters/", "db"},
	"delivery/httpapi":   {"internal/domain/", "internal/app", "internal/app/", "internal/delivery/session"},
	"delivery/workflows": {"internal/domain/", "internal/app", "internal/app/", "internal/delivery/session"},
	"delivery/session":   {},
}

// pureLayers import nothing from outside this module and the standard library: rules, use cases and HTTP do not know
// which libraries the adapters use.
var pureLayers = []string{"domain", "app", "delivery/httpapi", "delivery/session"}

// temporalAllowed are the only places that know Temporal: the way in (workflows, activities) and the way out (runner).
var temporalAllowed = []string{"delivery/workflows", "adapters/runner"}

func under(path, dir string) bool { return strings.HasPrefix(path, dir+"/") }

// layerOf returns the key of allowed for a source file, and false for code outside the layers.
func layerOf(path string) (string, bool) {
	for layer := range allowed {
		if under(path, layer) {
			return layer, true
		}
	}

	return "", false
}

func TestLayersDependInward(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		path = filepath.ToSlash(path)
		layer, ok := layerOf(path)
		if !ok {
			return nil
		}
		rules := allowed[layer]
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(p, module) {
				thirdParty := strings.Contains(strings.SplitN(p, "/", 2)[0], ".")
				if thirdParty && slices.ContainsFunc(pureLayers, func(dir string) bool { return under(path, dir) }) {
					t.Errorf("%s (%s) imports the library %s", path, layer, p)
				}
				if strings.HasPrefix(p, "go.temporal.io/") && !slices.ContainsFunc(temporalAllowed, func(dir string) bool { return under(path, dir) }) {
					t.Errorf("%s knows Temporal: only %v may import %s", path, temporalAllowed, p)
				}
				continue
			}
			p = strings.TrimPrefix(p, module)
			ok := false
			for _, prefix := range rules {
				ok = ok || p == prefix || (strings.HasSuffix(prefix, "/") && strings.HasPrefix(p, prefix))
			}
			if !ok {
				t.Errorf("%s (%s) imports %s", path, layer, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
