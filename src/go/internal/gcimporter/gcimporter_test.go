// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter

import (
	"fmt"
	"internal/testenv"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go/types"
)

// skipSpecialPlatforms causes the test to be skipped for platforms where
// builders (build.golang.org) don't have access to compiled packages for
// import.
func skipSpecialPlatforms(t *testing.T) {
	switch platform := runtime.GOOS + "-" + runtime.GOARCH; platform {
	case "nacl-amd64p32",
		"nacl-386",
		"nacl-arm",
		"darwin-arm",
		"darwin-arm64":
		t.Skipf("no compiled packages available for import on %s", platform)
	}
}

func compile(t *testing.T, dirname, filename string) string {
	testenv.MustHaveGoBuild(t)
	cmd := exec.Command("go", "tool", "compile", filename)
	cmd.Dir = dirname
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("%s", out)
		t.Fatalf("go tool compile %s failed: %s", filename, err)
	}
	// filename should end with ".go"
	return filepath.Join(dirname, filename[:len(filename)-2]+"o")
}

// TODO(gri) Remove this function once we switched to new export format by default.
func compileNewExport(t *testing.T, dirname, filename string) string {
	testenv.MustHaveGoBuild(t)
	cmd := exec.Command("go", "tool", "compile", "-newexport", filename)
	cmd.Dir = dirname
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("%s", out)
		t.Fatalf("go tool compile %s failed: %s", filename, err)
	}
	// filename should end with ".go"
	return filepath.Join(dirname, filename[:len(filename)-2]+"o")
}

func testPath(t *testing.T, path string) *types.Package {
	t0 := time.Now()
	pkg, err := Import(make(map[string]*types.Package), path)
	if err != nil {
		t.Errorf("testPath(%s): %s", path, err)
		return nil
	}
	t.Logf("testPath(%s): %v", path, time.Since(t0))
	return pkg
}

const maxTime = 30 * time.Second

func testDir(t *testing.T, dir string, endTime time.Time) (nimports int) {
	dirname := filepath.Join(runtime.GOROOT(), "pkg", runtime.GOOS+"_"+runtime.GOARCH, dir)
	list, err := ioutil.ReadDir(dirname)
	if err != nil {
		t.Fatalf("testDir(%s): %s", dirname, err)
	}
	for _, f := range list {
		if time.Now().After(endTime) {
			t.Log("testing time used up")
			return
		}
		switch {
		case !f.IsDir():
			// try extensions
			for _, ext := range pkgExts {
				if strings.HasSuffix(f.Name(), ext) {
					name := f.Name()[0 : len(f.Name())-len(ext)] // remove extension
					if testPath(t, filepath.Join(dir, name)) != nil {
						nimports++
					}
				}
			}
		case f.IsDir():
			nimports += testDir(t, filepath.Join(dir, f.Name()), endTime)
		}
	}
	return
}

func TestImportTestdata(t *testing.T) {
	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
		return
	}

	if outFn := compile(t, "testdata", "exports.go"); outFn != "" {
		defer os.Remove(outFn)
	}

	if pkg := testPath(t, "./testdata/exports"); pkg != nil {
		// The package's Imports list must include all packages
		// explicitly imported by exports.go, plus all packages
		// referenced indirectly via exported objects in exports.go.
		// With the textual export format, the list may also include
		// additional packages that are not strictly required for
		// import processing alone (they are exported to err "on
		// the safe side").
		got := fmt.Sprint(pkg.Imports())
		for _, want := range []string{"go/ast", "go/token"} {
			if !strings.Contains(got, want) {
				t.Errorf(`Package("exports").Imports() = %s, does not contain %s`, got, want)
			}
		}
	}
}

// TODO(gri) Remove this function once we switched to new export format by default
//           (and update the comment and want list in TestImportTestdata).
func TestImportTestdataNewExport(t *testing.T) {
	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
		return
	}

	if outFn := compileNewExport(t, "testdata", "exports.go"); outFn != "" {
		defer os.Remove(outFn)
	}

	if pkg := testPath(t, "./testdata/exports"); pkg != nil {
		// The package's Imports list must include all packages
		// explicitly imported by exports.go, plus all packages
		// referenced indirectly via exported objects in exports.go.
		want := `[package ast ("go/ast") package token ("go/token")]`
		got := fmt.Sprint(pkg.Imports())
		if got != want {
			t.Errorf(`Package("exports").Imports() = %s, want %s`, got, want)
		}
	}
}

func TestImportStdLib(t *testing.T) {
	skipSpecialPlatforms(t)

	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
		return
	}

	dt := maxTime
	if testing.Short() && testenv.Builder() == "" {
		dt = 10 * time.Millisecond
	}
	nimports := testDir(t, "", time.Now().Add(dt)) // installed packages
	t.Logf("tested %d imports", nimports)
}

var importedObjectTests = []struct {
	name string
	want string
}{
	{"math.Pi", "const Pi untyped float"},
	{"io.Reader", "type Reader interface{Read(p []byte) (n int, err error)}"},
	{"io.ReadWriter", "type ReadWriter interface{Read(p []byte) (n int, err error); Write(p []byte) (n int, err error)}"},
	{"math.Sin", "func Sin(x float64) float64"},
	// TODO(gri) add more tests
}

func TestImportedTypes(t *testing.T) {
	skipSpecialPlatforms(t)

	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
		return
	}

	for _, test := range importedObjectTests {
		s := strings.Split(test.name, ".")
		if len(s) != 2 {
			t.Fatal("inconsistent test data")
		}
		importPath := s[0]
		objName := s[1]

		pkg, err := Import(make(map[string]*types.Package), importPath)
		if err != nil {
			t.Error(err)
			continue
		}

		obj := pkg.Scope().Lookup(objName)
		if obj == nil {
			t.Errorf("%s: object not found", test.name)
			continue
		}

		got := types.ObjectString(obj, types.RelativeTo(pkg))
		if got != test.want {
			t.Errorf("%s: got %q; want %q", test.name, got, test.want)
		}
	}
}

func TestIssue5815(t *testing.T) {
	skipSpecialPlatforms(t)

	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
		return
	}

	pkg, err := Import(make(map[string]*types.Package), "strings")
	if err != nil {
		t.Fatal(err)
	}

	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if obj.Pkg() == nil {
			t.Errorf("no pkg for %s", obj)
		}
		if tname, _ := obj.(*types.TypeName); tname != nil {
			named := tname.Type().(*types.Named)
			for i := 0; i < named.NumMethods(); i++ {
				m := named.Method(i)
				if m.Pkg() == nil {
					t.Errorf("no pkg for %s", m)
				}
			}
		}
	}
}

// Smoke test to ensure that imported methods get the correct package.
func TestCorrectMethodPackage(t *testing.T) {
	skipSpecialPlatforms(t)

	// This package only handles gc export data.
	if runtime.Compiler != "gc" {
		t.Skipf("gc-built packages not available (compiler = %s)", runtime.Compiler)
		return
	}

	imports := make(map[string]*types.Package)
	_, err := Import(imports, "net/http")
	if err != nil {
		t.Fatal(err)
	}

	mutex := imports["sync"].Scope().Lookup("Mutex").(*types.TypeName).Type()
	mset := types.NewMethodSet(types.NewPointer(mutex)) // methods of *sync.Mutex
	sel := mset.Lookup(nil, "Lock")
	lock := sel.Obj().(*types.Func)
	if got, want := lock.Pkg().Path(), "sync"; got != want {
		t.Errorf("got package path %q; want %q", got, want)
	}
}
