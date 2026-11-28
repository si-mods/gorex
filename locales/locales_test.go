package locales

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

func parseCatalogBytes(name string, b []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}

func messageKeys(m map[string]json.RawMessage) []string {
	var keys []string
	for k := range m {
		if !strings.HasPrefix(k, "@") {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

func checkCatalogParity(enRaw, zhRaw map[string]json.RawMessage) []string {
	var errs []string
	en := messageKeys(enRaw)
	zh := messageKeys(zhRaw)
	if len(en) == 0 {
		return []string{"en.json has no messages"}
	}
	for _, k := range en {
		if !slices.Contains(zh, k) {
			errs = append(errs, fmt.Sprintf("zh-Hans.json lacks %s", k))
		}
	}
	for _, k := range zh {
		if !slices.Contains(en, k) {
			errs = append(errs, fmt.Sprintf("zh-Hans.json has %s, which en.json lacks", k))
		}
	}
	for _, pair := range []struct {
		name string
		raw  map[string]json.RawMessage
	}{
		{"en.json", enRaw},
		{"zh-Hans.json", zhRaw},
	} {
		for k, v := range pair.raw {
			if strings.HasPrefix(k, "@") && !strings.HasPrefix(k, "@@") {
				base := k[1:]
				if _, ok := pair.raw[base]; !ok {
					errs = append(errs, fmt.Sprintf("%s has orphaned metadata %s without message %s", pair.name, k, base))
				}
			}
			if strings.Contains(string(v), "...") {
				errs = append(errs, fmt.Sprintf("%s: %s contains ASCII '...' instead of '…': %s", pair.name, k, v))
			}
		}
	}
	slices.Sort(errs)
	return errs
}

func checkCatalogGenSync(en []string, genSrc []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "catalog_gen.go", genSrc, 0)
	if err != nil {
		return nil, fmt.Errorf("parse catalog_gen.go: %w", err)
	}
	genKeys := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Format" {
			return true
		}
		lit, ok := call.Args[1].(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			genKeys[strings.Trim(lit.Value, `"`)] = true
		}
		return true
	})
	var errs []string
	for _, k := range en {
		if !genKeys[k] {
			errs = append(errs, fmt.Sprintf("catalog_gen.go missing generated entry for %s (run `go tool mygo generate`)", k))
		}
	}
	for k := range genKeys {
		if !slices.Contains(en, k) {
			errs = append(errs, fmt.Sprintf("catalog_gen.go has stale entry for %s (run `go tool mygo generate`)", k))
		}
	}
	slices.Sort(errs)
	return errs, nil
}

func TestCatalogParityAndMetadata(t *testing.T) {
	enBytes, err := os.ReadFile("en.json")
	if err != nil {
		t.Fatal(err)
	}
	zhBytes, err := os.ReadFile("zh-Hans.json")
	if err != nil {
		t.Fatal(err)
	}
	enRaw, err := parseCatalogBytes("en.json", enBytes)
	if err != nil {
		t.Fatal(err)
	}
	zhRaw, err := parseCatalogBytes("zh-Hans.json", zhBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range checkCatalogParity(enRaw, zhRaw) {
		t.Error(e)
	}
}

func TestCatalogGenInSync(t *testing.T) {
	enBytes, err := os.ReadFile("en.json")
	if err != nil {
		t.Fatal(err)
	}
	enRaw, err := parseCatalogBytes("en.json", enBytes)
	if err != nil {
		t.Fatal(err)
	}
	genSrc, err := os.ReadFile("catalog_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	errs, err := checkCatalogGenSync(messageKeys(enRaw), genSrc)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range errs {
		t.Error(e)
	}
}

func TestCheckCatalogParityViolations(t *testing.T) {
	enRaw, err := parseCatalogBytes("en.json", []byte(`{
		"@@locale": "en",
		"hello": "Hello...",
		"onlyEn": "Only English",
		"@orphanEn": {"description": "dangling"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	zhRaw, err := parseCatalogBytes("zh-Hans.json", []byte(`{
		"@@locale": "zh-Hans",
		"hello": "你好",
		"onlyZh": "仅中文...",
		"@orphanZh": {"description": "悬空"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(checkCatalogParity(enRaw, zhRaw), "\n")
	for _, want := range []string{
		"zh-Hans.json lacks onlyEn",
		"zh-Hans.json has onlyZh, which en.json lacks",
		"en.json has orphaned metadata @orphanEn without message orphanEn",
		"zh-Hans.json has orphaned metadata @orphanZh without message orphanZh",
		"en.json: hello contains ASCII '...'",
		"zh-Hans.json: onlyZh contains ASCII '...'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing expected parity violation %q in:\n%s", want, got)
		}
	}
}

func TestCheckCatalogGenSyncViolations(t *testing.T) {
	src := []byte(`package locales
func (msgs Messages) Hello() string { return Catalog.Format(msgs.locale, "hello", nil) }
func (msgs Messages) Stale() string { return Catalog.Format(msgs.locale, "staleKey", nil) }
`)
	errs, err := checkCatalogGenSync([]string{"hello", "missingKey"}, src)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(errs, "\n")
	for _, want := range []string{
		"missing generated entry for missingKey",
		"stale entry for staleKey",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing expected sync violation %q in:\n%s", want, got)
		}
	}
}
