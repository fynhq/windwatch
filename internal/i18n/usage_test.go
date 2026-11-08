package i18n

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var templateTranslationKey = regexp.MustCompile("\\.I18n\\.Tf?\\s+(?:\"([^\"]*)\"|`([^`]*)`)")

// TestTranslationKeyUsage keeps the locale catalogs and the code that renders
// them in sync. Dynamic key selection is common (error, notice, and status keys
// are passed through variables), so Go string literals count as references and
// key-shaped literals are validated wherever they appear.
func TestTranslationKeyUsage(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatalf("load catalogs: %v", err)
	}
	catalog := catalogs.byLang[LangEN].messages
	keyPattern := translationKeyPattern(catalog)

	root := repositoryRoot(t)
	used := make(map[string]struct{})
	missing := make(map[string]struct{})

	if scanGoSources(t, root, catalog, keyPattern, used, missing) == 0 {
		t.Fatal("no production Go files found")
	}
	if scanTemplates(t, root, catalog, used, missing) == 0 {
		t.Fatal("no templates found")
	}

	if len(missing) > 0 {
		t.Fatalf("translation keys missing from %s locale:\n%s", LangEN, joinSorted(missing))
	}

	unused := make([]string, 0, len(catalog))
	for key := range catalog {
		if _, ok := used[key]; !ok {
			unused = append(unused, key)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		t.Fatalf("locale keys not referenced by production source or templates:\n%s", strings.Join(unused, "\n"))
	}
}

func translationKeyPattern(catalog map[string]string) *regexp.Regexp {
	namespaces := make(map[string]struct{})
	for key := range catalog {
		if index := strings.IndexByte(key, '.'); index > 0 {
			namespaces[key[:index]] = struct{}{}
		}
	}
	names := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		names = append(names, regexp.QuoteMeta(namespace))
	}
	sort.Strings(names)
	return regexp.MustCompile(`^(?:` + strings.Join(names, "|") + `)\.[A-Za-z0-9.]+$`)
}

func scanGoSources(t *testing.T, root string, catalog map[string]string, keyPattern *regexp.Regexp, used map[string]struct{}, missing map[string]struct{}) int {
	t.Helper()
	count := 0
	for _, dir := range []string{"internal", "cmd"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			count++
			scanGoFile(t, root, path, catalog, keyPattern, used, missing)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return count
}

func scanGoFile(t *testing.T, root, filename string, catalog map[string]string, keyPattern *regexp.Regexp, used map[string]struct{}, missing map[string]struct{}) {
	t.Helper()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relativePath(t, root, filename), err)
	}
	rel := relativePath(t, root, filename)
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(n.Value)
			if err != nil {
				return true
			}
			used[value] = struct{}{}
			if keyPattern.MatchString(value) {
				if _, ok := catalog[value]; !ok {
					recordMissing(missing, rel, files.Position(n.Pos()).Line, value)
				}
			}
		case *ast.CallExpr:
			selector, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "T" && selector.Sel.Name != "Tf") || len(n.Args) == 0 {
				return true
			}
			literal, ok := n.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			key, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			if _, ok := catalog[key]; !ok {
				recordMissing(missing, rel, files.Position(literal.Pos()).Line, key)
			}
		}
		return true
	})
}

func scanTemplates(t *testing.T, root string, catalog map[string]string, used map[string]struct{}, missing map[string]struct{}) int {
	t.Helper()
	base := filepath.Join(root, "internal", "web", "templates")
	count := 0
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		count++
		scanTemplateFile(t, root, path, catalog, used, missing)
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	return count
}

func scanTemplateFile(t *testing.T, root, filename string, catalog map[string]string, used map[string]struct{}, missing map[string]struct{}) {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", relativePath(t, root, filename), err)
	}
	rel := relativePath(t, root, filename)
	for _, match := range templateTranslationKey.FindAllSubmatchIndex(data, -1) {
		var key string
		switch {
		case match[2] >= 0:
			key = string(data[match[2]:match[3]])
		case match[4] >= 0:
			key = string(data[match[4]:match[5]])
		default:
			continue
		}
		used[key] = struct{}{}
		if _, ok := catalog[key]; !ok {
			line := bytes.Count(data[:match[0]], []byte("\n")) + 1
			recordMissing(missing, rel, line, key)
		}
	}
}

func recordMissing(missing map[string]struct{}, rel string, line int, key string) {
	missing[fmt.Sprintf("%s:%d: %q", rel, line, key)] = struct{}{}
}

func joinSorted(entries map[string]struct{}) string {
	sorted := make([]string, 0, len(entries))
	for entry := range entries {
		sorted = append(sorted, entry)
	}
	sort.Strings(sorted)
	return strings.Join(sorted, "\n")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		root := filepath.Dir(filepath.Dir(filepath.Dir(filename)))
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
	}
	// -trimpath rewrites runtime.Caller paths; go test always starts in the
	// package directory, so fall back to it.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func relativePath(t *testing.T, root, filename string) string {
	t.Helper()
	rel, err := filepath.Rel(root, filename)
	if err != nil {
		t.Fatalf("relative path for %s: %v", filename, err)
	}
	return filepath.ToSlash(rel)
}
