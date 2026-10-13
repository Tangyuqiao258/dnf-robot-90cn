package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"robot/internal/shared"
)

// capabilityConstantPrefix is how every capability constant is named. A
// capability declared without it is invisible to the catalog check below, so
// the constant block is the contract the prefix describes.
const capabilityConstantPrefix = "Capability"

// capabilityCatalogSource is the file that declares the constants and the
// catalog slice, and the only place either may live.
const capabilityCatalogSource = "internal/shared/backend.go"

// TestCapabilityCatalogCoversEveryDeclaredConstant keeps the catalog and the
// declared constants from drifting apart. A capability that is declared but not
// catalogued is the dangerous direction: CapabilityMatrix seeds a backend's
// status map from the catalog alone, so an uncatalogued capability is simply
// absent from every backend that does not name it explicitly, and an absent key
// reads as disabled through Supports. The backend then reports a capability it
// actually has as unavailable, and nothing in the build notices.
func TestCapabilityCatalogCoversEveryDeclaredConstant(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, filepath.FromSlash(capabilityCatalogSource))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	declared := declaredCapabilityConstants(t, file)
	catalogued := cataloguedCapabilities(t, file)
	for _, name := range sortedKeys(declared) {
		if !catalogued[name] {
			t.Errorf("%s declares %s = %q but the capability catalog omits it; every backend would report it as unsupported",
				capabilityCatalogSource, name, declared[name])
		}
	}
	if len(catalogued) == 0 {
		t.Fatal("the capability catalog is empty; the check would pass vacuously")
	}
	// The other direction matters too: a catalogued name with no constant is a
	// capability nobody can refer to, which no backend could ever implement.
	for _, name := range sortedBoolKeys(catalogued) {
		if _, ok := declared[name]; !ok {
			t.Errorf("the capability catalog lists %s, which no %s* constant declares", name, capabilityConstantPrefix)
		}
	}
}

// TestCapabilityMatrixSeedsTheWholeCatalog checks the map a backend starts
// from. The backend-facing half of the invariant -- that every registered
// backend really does end up reporting all of them -- is asserted in the
// registry test, where the registry is already imported.
func TestCapabilityMatrixSeedsTheWholeCatalog(t *testing.T) {
	catalog := shared.AllCapabilities()
	if len(catalog) == 0 {
		t.Fatal("the capability catalog is empty")
	}
	seen := make(map[shared.BackendCapability]bool, len(catalog))
	for _, capability := range catalog {
		if seen[capability] {
			t.Errorf("the capability catalog lists %q twice", capability)
		}
		seen[capability] = true
	}
	matrix := shared.CapabilityMatrix(shared.CapabilityStatus{Enabled: true})
	if len(matrix) != len(catalog) {
		t.Fatalf("CapabilityMatrix seeded %d entries for a catalog of %d", len(matrix), len(catalog))
	}
	for _, capability := range catalog {
		if _, ok := matrix[capability]; !ok {
			t.Errorf("CapabilityMatrix omitted catalogued capability %q", capability)
		}
	}
}

// webCapabilityFieldPattern matches the property reads the Web runtime does on
// the capability map: backendCapabilities.dungeon_follow and friends.
var webCapabilityFieldPattern = regexp.MustCompile(`backendCapabilities\.([A-Za-z_][A-Za-z0-9_]*)`)

// webCapabilityIndexPattern matches the indexed form with a literal key.
var webCapabilityIndexPattern = regexp.MustCompile(`backendCapabilities\[\s*'([^']+)'\s*\]`)

// webCapabilityAssignmentPattern captures the right-hand side of the local the
// toolbar button map assigns its capability name to, up to the end of the
// statement. The two buttons that choose between a broadcast and a directed
// capability pick their literal inside a ternary, so the whole statement has to
// be read rather than a single literal after the equals sign.
var webCapabilityAssignmentPattern = regexp.MustCompile(`\bcap=([^;]*);`)

var webQuotedLiteralPattern = regexp.MustCompile(`'([^']*)'`)

// TestWebRuntimeReadsOnlyDeclaredCapabilities holds the Web runtime to the
// capability catalog. The Web decides which toolbar buttons to disable by
// looking capability names up in a map the API returns, and a name that is not
// a real capability is not an error there -- it is simply absent, and an absent
// entry disables nothing. A typo therefore ships as a button that stays enabled
// on a backend that cannot serve it. Reading the names out of the asset is the
// only way to catch that, since nothing type-checks a string the browser looks
// up at render time.
func TestWebRuntimeReadsOnlyDeclaredCapabilities(t *testing.T) {
	root := repoRoot(t)
	known := make(map[string]bool)
	for _, capability := range shared.AllCapabilities() {
		known[string(capability)] = true
	}
	read := make(map[string]string)
	assetsDir := filepath.Join(root, "internal", "entry", "webadmin", "assets")
	entries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatalf("read Web assets: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".js" && ext != ".html" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(assetsDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		content := string(data)
		for _, match := range webCapabilityFieldPattern.FindAllStringSubmatch(content, -1) {
			read[match[1]] = entry.Name()
		}
		for _, match := range webCapabilityIndexPattern.FindAllStringSubmatch(content, -1) {
			read[match[1]] = entry.Name()
		}
		for _, statement := range webCapabilityAssignmentPattern.FindAllStringSubmatch(content, -1) {
			for _, literal := range webQuotedLiteralPattern.FindAllStringSubmatch(statement[1], -1) {
				if literal[1] == "" {
					continue
				}
				read[literal[1]] = entry.Name()
			}
		}
	}
	if len(read) == 0 {
		t.Fatal("no capability lookups found in the Web assets; the check would pass vacuously")
	}
	for _, key := range sortedKeys(read) {
		if !known[key] {
			t.Errorf("%s reads capability %q, which is not a declared capability; the button or card it guards can never be disabled",
				read[key], key)
		}
	}
}

// versionTokens are the backend and version names that must not appear in a
// capability key. The key is the contract every backend answers to, so a key
// naming one version describes a capability the others cannot claim even when
// they implement exactly the same thing.
var versionTokens = []string{"cn90", "90cn", "115us", "86jp", "sim"}

func TestCapabilityKeysCarryNoVersionName(t *testing.T) {
	for _, capability := range shared.AllCapabilities() {
		key := strings.ToLower(string(capability))
		for _, token := range versionTokens {
			if strings.Contains(key, token) {
				t.Errorf("capability key %q names a specific backend or version with token %q; the key is the shared contract, so it must describe the capability instead",
					capability, token)
			}
		}
	}
}

// declaredCapabilityConstants collects the name to string value of every
// constant declared with the BackendCapability type.
func declaredCapabilityConstants(t *testing.T, file *ast.File) map[string]string {
	t.Helper()
	declared := make(map[string]string)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || !isBackendCapabilityType(valueSpec.Type) {
				continue
			}
			if len(valueSpec.Values) != 1 {
				continue
			}
			literal, ok := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			for _, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, capabilityConstantPrefix) {
					t.Errorf("%s declares %s with the capability type but without the %s prefix; the catalog check cannot see it",
						capabilityCatalogSource, name.Name, capabilityConstantPrefix)
					continue
				}
				declared[name.Name] = strings.Trim(literal.Value, `"`)
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("no capability constants found; the check would pass vacuously")
	}
	return declared
}

// cataloguedCapabilities reads the values listed in the catalog slice.
func cataloguedCapabilities(t *testing.T, file *ast.File) map[string]bool {
	t.Helper()
	catalogued := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		valueSpec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		named := false
		for _, name := range valueSpec.Names {
			if name.Name == "allCapabilities" {
				named = true
			}
		}
		if !named || len(valueSpec.Values) != 1 {
			return true
		}
		composite, ok := valueSpec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, element := range composite.Elts {
			ident, ok := element.(*ast.Ident)
			if !ok {
				t.Errorf("the capability catalog holds a non-identifier entry; list every catalogued capability by its constant")
				continue
			}
			catalogued[ident.Name] = true
		}
		return false
	})
	return catalogued
}

func isBackendCapabilityType(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name == "BackendCapability"
	case *ast.SelectorExpr:
		return x.Sel.Name == "BackendCapability"
	default:
		return false
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
