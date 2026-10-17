package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var forbiddenFileNameTokens = []string{
	"compat",
	"temp",
	"legacy",
	"bridge",
	"adapter",
	"misc",
	"helper",
}

var forbiddenRuntimeArtifactSuffixes = []string{
	".bak",
	".jsonl",
	".log",
	".tmp",
}

var sqlImportAllowedDirs = []string{
	"cmd/robot",
	"internal/composition/backend/cn90",
}

var actionResultStateDirs = []string{
	"internal/actor",
	"internal/scheduler",
	"internal/capability/robotaction",
	"internal/capability/store",
}

func TestActionResultStatesUseNamedConstants(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range actionResultStateDirs {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch x := node.(type) {
				case *ast.CompositeLit:
					if !isActionResultType(x.Type) {
						return true
					}
					for _, elt := range x.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok || !identNamed(kv.Key, "State") || !isStringLiteral(kv.Value) {
							continue
						}
						t.Errorf("%s uses literal ActionResult.State in composite literal", path)
					}
				case *ast.AssignStmt:
					for i, lhs := range x.Lhs {
						if i >= len(x.Rhs) || !selectorNamed(lhs, "State") || !isStringLiteral(x.Rhs[i]) {
							continue
						}
						t.Errorf("%s assigns literal ActionResult.State", path)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func TestActorLocksUsePurposeNames(t *testing.T) {
	root := repoRoot(t)
	assertStructHasNoGenericLockField(t, filepath.Join(root, "internal", "actor", "actor_core.go"), "Actor")
	assertStructHasNoGenericLockField(t, filepath.Join(root, "internal", "actor", "ledger.go"), "Ledger")
}

func TestStoreLocksUsePurposeNames(t *testing.T) {
	root := repoRoot(t)
	assertStructHasNoGenericLockField(t, filepath.Join(root, "internal", "capability", "store", "coordinator.go"), "PointCoordinator")
}

func assertStructHasNoGenericLockField(t *testing.T, path, structName string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			st, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == "mu" {
						t.Errorf("%s %s lock field uses generic name mu; use a purpose name", path, structName)
					}
				}
			}
		}
	}
}

func TestMutexDeclarationsStayInsideLockhub(t *testing.T) {
	root := repoRoot(t)
	internal := filepath.Join(root, "internal")
	err := filepath.WalkDir(internal, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "/internal/foundation/lockhub/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		mutexToken := "sync" + "." + "Mutex"
		rwMutexToken := "sync" + "." + "RWMutex"
		if strings.Contains(text, mutexToken) || strings.Contains(text, rwMutexToken) {
			t.Errorf("%s declares raw mutex outside lockhub", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", internal, err)
	}
}

func TestSQLPackageImportsStayInRepositoryOrProtocolCode(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) != "database/sql" {
				continue
			}
			if !pathUnderAny(root, path, sqlImportAllowedDirs) {
				t.Errorf("%s imports database/sql outside approved SQL boundary", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestGoFileNamesDoNotUseTemporaryStructureNames(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".go"))
		for _, token := range forbiddenFileNameTokens {
			if base == token || strings.HasPrefix(base, token+"_") || strings.HasSuffix(base, "_"+token) || strings.Contains(base, "_"+token+"_") {
				t.Errorf("%s uses temporary structure token %q in file name", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// backendAssemblyCommandFiles lists the composition-root files allowed to
// reference the concrete adapter. Everything else under cmd/ must stay
// backend-neutral and only touch shared ports.
var backendAssemblyCommandFiles = map[string]bool{
	"runtime_main.go": true,
}

func TestCommandEntryStaysBackendNeutralOutsideAssembly(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "robot")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read cmd/robot: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if backendAssemblyCommandFiles[entry.Name()] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, token := range []string{"composition/backend/cn90", "protocol/cn90", "90CN"} {
			if strings.Contains(string(data), token) {
				t.Errorf("cmd/robot/%s references concrete adapter token %q; move adapter assembly to runtime_main.go or the adapter package", entry.Name(), token)
			}
		}
	}
}

func TestReadmeFilesStayAtRepositoryRoot(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Base(path), "README.md") {
			if strings.EqualFold(filepath.Dir(path), root) {
				return nil
			}
			t.Errorf("%s fragments documentation; keep the single README.md at the repository root and use doc/规整文档.md for everything else", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestRuntimeArtifactsDoNotStayInRepository(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		base := strings.ToLower(filepath.Base(path))
		for _, suffix := range forbiddenRuntimeArtifactSuffixes {
			if strings.HasSuffix(base, suffix) {
				t.Errorf("%s is a runtime or temporary artifact; keep generated files outside the repository", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func TestSchedulerDoesNotExposeActionFacadeMethods(t *testing.T) {
	root := repoRoot(t)
	schedulerDir := filepath.Join(root, "internal", "scheduler")
	forbidden := map[string]bool{
		"Online":          true,
		"OnlineNoConfirm": true,
		"Logout":          true,
		"Move":            true,
		"ShoutOne":        true,
		"Store":           true,
	}
	err := filepath.WalkDir(schedulerDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !forbidden[fn.Name.Name] {
				continue
			}
			for _, field := range fn.Recv.List {
				if receiverIsRobotManager(field.Type) {
					t.Errorf("%s defines RobotManager.%s action facade; use managed command entry or capability service", path, fn.Name.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", schedulerDir, err)
	}
}

func TestSchedulerDoesNotReferenceConcreteBackends(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "internal", "scheduler")
	forbidden := []string{"Backend90CN", "sim_90cn", "90CN", "CN90", "composition/backend/cn90", "protocol/cn90"}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Errorf("%s references concrete backend token %q", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk scheduler: %v", err)
	}
}

func TestPublicLayersDoNotReferenceConcreteBackendNames(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"internal/actor", "internal/bootstrap", "internal/capability", "internal/entry",
		"internal/foundation", "internal/scheduler", "internal/shared",
	} {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, token := range []string{"CN90", "90CN", "sim_90cn", "protocol/cn90", "composition/backend/cn90"} {
				if strings.Contains(string(data), token) {
					t.Errorf("%s references concrete backend token %q", path, token)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func TestWebRuntimeDoesNotUseBackendTypeGuards(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "internal", "entry", "webadmin")
	forbidden := []string{
		"isNativeBackend", "nativeBackendOnly",
		"BackendNative", "backend/native",
	}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".js" && ext != ".html" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Errorf("%s references concrete backend token %q; use catalog metadata or declared capabilities", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk Web runtime: %v", err)
	}
}

func TestSchedulerHasOneRuntimeStatusCacheOwner(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "internal", "scheduler")
	owners := 0
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			structType, ok := node.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range structType.Fields.List {
				if !isRuntimeStatusMap(field.Type) {
					continue
				}
				allowed := filepath.Base(path) == "runtime_status.go" && len(field.Names) == 1 && field.Names[0].Name == "snapshot"
				if !allowed {
					t.Errorf("%s declares a parallel runtime-status cache field; runtimeStateTable.snapshot is the sole owner", path)
					continue
				}
				owners++
			}
			return false
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk scheduler: %v", err)
	}
	if owners != 1 {
		t.Fatalf("runtime-status cache owners = %d, want exactly 1", owners)
	}
	runtimeSource, err := os.ReadFile(filepath.Join(dir, "runtime_status.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runtimeSource), "robotcap.CopyRuntimeStatusMap(status)") {
		t.Fatal("adapter runtime state must be copied at runtimeStateTable ingress")
	}
}

func TestWebConsumesSchedulerRuntimeProjections(t *testing.T) {
	root := repoRoot(t)
	appPath := filepath.Join(root, "internal", "entry", "webadmin", "assets", "app.js")
	data, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, required := range []string{"api('dashboardStatus')", "api('robotsStatus'"} {
		if !strings.Contains(content, required) {
			t.Errorf("Web runtime projection is missing %q", required)
		}
	}
	for _, forbidden := range []string{"RuntimeStatusMap", "/api/runtime-status", "actorRuntime", "runtimeStatusCache"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("Web builds an alternate runtime-state source with token %q", forbidden)
		}
	}
}

func TestSchedulerLockResourcesUseNamedConstants(t *testing.T) {
	root := repoRoot(t)
	targets := []string{
		filepath.Join(root, "internal", "scheduler"),
	}
	for _, dir := range targets {
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(data)
			for _, token := range []string{
				`WithResource("scheduler"`,
				`WithResource("repository"`,
				`WithResource("config"`,
			} {
				if strings.Contains(text, token) {
					t.Errorf("%s uses literal lock resource scope %s; use named lock resource constants", path, token)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func receiverIsRobotManager(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.StarExpr:
		return receiverIsRobotManager(x.X)
	case *ast.Ident:
		return x.Name == "RobotManager"
	default:
		return false
	}
}

func isActionResultType(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == "ActionResult"
	case *ast.Ident:
		return x.Name == "ActionResult"
	default:
		return false
	}
}

func identNamed(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func selectorNamed(expr ast.Expr, name string) bool {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == name
	case *ast.IndexExpr:
		return selectorNamed(x.X, name)
	default:
		return false
	}
}

func isStringLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func isRuntimeStatusMap(expr ast.Expr) bool {
	mapType, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := mapType.Key.(*ast.Ident)
	if !ok || key.Name != "int" {
		return false
	}
	switch value := mapType.Value.(type) {
	case *ast.SelectorExpr:
		return value.Sel.Name == "RuntimeStatus"
	case *ast.Ident:
		return value.Name == "RuntimeStatus"
	default:
		return false
	}
}

func pathUnderAny(root string, path string, dirs []string) bool {
	cleanPath := filepath.ToSlash(filepath.Clean(path))
	for _, dir := range dirs {
		cleanDir := filepath.ToSlash(filepath.Join(root, filepath.FromSlash(dir)))
		if cleanPath == cleanDir || strings.HasPrefix(cleanPath, cleanDir+"/") {
			return true
		}
	}
	return false
}
