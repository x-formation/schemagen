package schemagen

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	defJSONTest  = `{"$schema": "http://json-schema.org/draft-04/schema#", %s}`
	idDefinition = `"definitions": {"id": { "type": "integer", "minimum": 1}}`
	JSONTest     = `{"type": "object","properties": { %s "id": {"$ref": "#/definitions/id"}}}`
)

func TestLoadDefinitions(t *testing.T) {
	schg := New(false)
	// no definitions.json file.
	noDefPath, err := ioutil.TempDir(os.TempDir(), "schemafail")
	if err != nil {
		t.Fatalf("want err=nil; got %q", err)
	}

	tests := []string{
		noDefPath,
		newSchemaJSONDir(t, "/*Invalid schema{", " ", ""),
		newSchemaJSONDir(t, fmt.Sprintf(defJSONTest, `"id": 32`), " ", ""),
	}
	defer func() {
		for _, p := range tests {
			os.RemoveAll(p)
		}
	}()
	for _, path := range tests {
		err = schg.loadDefinitions(path)
		if err == nil {
			t.Fatalf("want err!=nil")
		}

		if schg.definitions != nil {
			t.Fatalf("want schg.definitions=nil; got %v", schg.definitions)
		}
	}

	// valid definitions.json schema.
	path := newSchemaJSONDir(t, fmt.Sprintf(defJSONTest, idDefinition), " ", "")
	defer os.RemoveAll(path)
	err = schg.loadDefinitions(path)
	if err != nil {
		t.Fatalf("want err=nil; got %q", err)
	}

	if schg.definitions == nil {
		t.Errorf("want schg.definitions!=nil")
	}
}

func TestFindReferences(t *testing.T) {
	schg := New(false)
	refs := findReferencesTest(t, fmt.Sprintf(JSONTest, ""), schg)
	if len(refs) != 1 {
		t.Fatalf("want len(refs)=1; got %d", len(refs))
	}
	if refs[0] != "id" {
		t.Fatalf("want refs[0]=\"id\"; got %s", refs[0])
	}

	refs = findReferencesTest(t, `{ "id": 54 }`, schg)
	if len(refs) != 0 {
		t.Fatalf("want len(refs)=0; got %d", len(refs))
	}
}

func TestMakeDefinitions(t *testing.T) {
	schg := New(false)
	// nil definitions.
	if schg.definitions != nil {
		t.Fatalf("want schg.definitions=nil")
	}

	defsmap, err := schg.makeDefinitions([]string{"id"})
	if err == nil {
		t.Fatalf("want err!=nil")
	}

	// empty definitions non empty refs.
	schg.definitions = make(map[string]interface{})
	if schg.definitions == nil {
		t.Fatalf("want schg.definitions!=nil")
	}

	defsmap, err = schg.makeDefinitions([]string{"id"})
	if err == nil {
		t.Fatal("want err!=nil")
	}

	// should extract id definition.
	schg.definitions = nil
	path := newSchemaJSONDir(t, fmt.Sprintf(defJSONTest, idDefinition), " ", "")
	defer os.RemoveAll(path)
	err = schg.loadDefinitions(path)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	if schg.definitions == nil {
		t.Fatalf("want schg.definitions!=nil")
	}

	defsmap, err = schg.makeDefinitions([]string{"id"})
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	_, ok := defsmap["id"]
	if !ok {
		t.Fatalf("want ok=true")
	}
}

func TestDumpToTmpDirs(t *testing.T) {
	testPaths := map[string]string{
		filepath.Join("service1", "method1"):   "service1",
		filepath.Join("service2", "method1"):   "service2",
		filepath.Join("service3", "method100"): "service3",
		filepath.Join("service4", "method200"): "service4",
	}
	schg := New(false)
	schg.pkg = "serv_dir"

	for path, serv := range testPaths {
		err := schg.dumpToTmpDirs(path, []byte("sth"))
		defer schg.dropTmpDirs()

		if err != nil {
			t.Fatalf("want err=nil; got %v", err)
		}
		_, ok := schg.services[serv]
		if !ok {
			t.Fatalf("want ok=true")
		}
	}

	schg.merge = true
	schg.services = make(map[string]string)
	for path, serv := range testPaths {
		err := schg.dumpToTmpDirs(path, []byte("sth"))
		if err != nil {
			t.Fatalf("want err=nil; got %v", err)
		}

		_, ok := schg.services[serv]
		if ok {
			t.Fatalf("want ok=false")
		}
	}
	_, ok := schg.services["serv_dir"]
	if !ok {
		t.Fatalf("want ok=true")
	}
	_, ok = schg.services["schema"]
	if ok {
		t.Fatalf("want ok=false")
	}
}

func TestSaveAsGoBinData(t *testing.T) {
	schg := New(false)
	tmpPath := newSchemaJSONDir(t, "def", "\x44\x55\x50\x41", "")

	schg.services["testservice"] = filepath.Join(tmpPath, "testservice")
	defer os.RemoveAll(tmpPath)
	out, err := ioutil.TempDir(os.TempDir(), "out")
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}
	defer os.RemoveAll(out)

	err = os.Mkdir(filepath.Join(out, "testservice"), 0755)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	err = schg.saveAsGoBinData(out)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	f, err := os.Open(filepath.Join(out, "testservice", "schema.go"))
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	defer f.Close()
	content, err := ioutil.ReadAll(f)

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write([]byte{0x44, 0x55, 0x50, 0x41})
	w.Close()
	byteSearch := ""
	for i := 0; i < 8 && i < len(buf.Bytes()); i++ {
		byteSearch += fmt.Sprintf("%#x, ", buf.Bytes()[i])
	}
	re := regexp.MustCompile(`0x([a-f0-9]), `)
	byteSearch = re.ReplaceAllString(byteSearch, `0x0$1, `)

	if strings.Index(string(content), byteSearch) == -1 {
		t.Errorf("want content (%s) to contain \"%s\"", string(content), byteSearch)
	}
}

func TestCreateBindSchemaFiles(t *testing.T) {
	schg := New(false)
	tmpPath := newSchemaJSONDir(t, "def", "cont", "")
	schg.services["testservice"] = filepath.Join(tmpPath, "testservice")
	defer os.RemoveAll(tmpPath)
	out, err := ioutil.TempDir(os.TempDir(), "out")
	defer os.RemoveAll(out)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	err = os.Mkdir(filepath.Join(out, "testservice"), 0755)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	err = schg.createBindSchemaFiles(out)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	f, err := os.Open(filepath.Join(out, "testservice", "bind.go"))
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	defer f.Close()
	content, err := ioutil.ReadAll(f)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	if strings.Index(string(content), "package testservice") == -1 {
		t.Errorf("want content (%s) to contain \"package testservice\"",
			string(content))
	}
	if strings.Index(string(content), "(\"testservice: ") == -1 {
		t.Errorf("want content (%s) to contain \"(\"testservice: \"",
			string(content))
	}
}

func TestGenerateNoMerge(t *testing.T) {
	schg := New(false)
	inPath := newSchemaJSONDir(t,
		fmt.Sprintf(defJSONTest, idDefinition), fmt.Sprintf(JSONTest, ""), "")
	defer os.RemoveAll(inPath)
	outPath, err := ioutil.TempDir(os.TempDir(), "out")
	defer os.RemoveAll(outPath)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	tests := map[string]bool{
		filepath.Join(outPath, "testservice", "schema.go"):         true,
		filepath.Join(outPath, "testservice", "bind.go"):           true,
		filepath.Join(outPath, filepath.Dir(outPath), "schema.go"): false,
		filepath.Join(outPath, filepath.Dir(outPath), "bind.go"):   false}

	err = schg.Generate(inPath, outPath)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	for path, isnil := range tests {
		_, err = os.Stat(path)
		if isnil {
			if err != nil {
				t.Errorf("want err=nil; got %v (path: %v)", err, path)
			}
		} else {
			if err == nil {
				t.Errorf("want err!=nil (path: %v)", path)
			}
		}
	}
}

func TestGenerateMerge(t *testing.T) {
	inPath := newSchemaJSONDir(t,
		fmt.Sprintf(defJSONTest, idDefinition), fmt.Sprintf(JSONTest, ""), "")
	defer os.RemoveAll(inPath)
	outPath, err := ioutil.TempDir(os.TempDir(), "out")
	defer os.RemoveAll(outPath)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	tests := map[string]bool{
		filepath.Join(outPath, "testservice", "schema.go"): false,
		filepath.Join(outPath, "testservice", "bind.go"):   false,
		filepath.Join(outPath, "schema.go"):                true,
		filepath.Join(outPath, "bind.go"):                  true,
	}
	schg := New(true)

	err = schg.Generate(inPath, outPath)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	err = schg.Generate(inPath, outPath)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}

	for path, isnil := range tests {
		_, err = os.Stat(path)
		if isnil {
			if err != nil {
				t.Errorf("want err=nil; got %v (path: %v)", err, path)
			}
		} else {
			if err == nil {
				t.Errorf("want err!=nil (path: %v)", path)
			}
		}
	}
}

func findReferencesTest(t *testing.T, schema string, schg *schg) []string {
	var mapSchema map[string]interface{}
	err := json.Unmarshal([]byte(schema), &mapSchema)
	if err != nil {
		t.Fatalf("want err=nil; got %v", err)
	}
	return schg.findReferences(mapSchema)
}

func newSchemaJSONDir(t *testing.T, definitions, method,
	subdir string) (path string) {
	// write definitions.json file.
	path, err := ioutil.TempDir(filepath.Join(os.TempDir(), subdir), "schema")
	if err != nil {
		t.Fatalf("Cannot create a temporary folder %v", err)
	}
	defFile, err := os.OpenFile(
		filepath.Join(path, definitionsFile), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		t.Fatalf("Cannot open %s file: %v", defFile.Name(), err)
	}
	defer defFile.Close()
	if _, err = defFile.WriteString(definitions); err != nil {
		t.Fatalf("Cannot write %s to file %s: %v", definitions, defFile.Name(), err)
	}
	// write service temp method content.
	servicePath := filepath.Join(path, "testservice")
	if err := os.Mkdir(servicePath, 0755); err != nil {
		t.Fatalf("Cannot create %s directory: %v", servicePath, err)
	}
	methodFile, err := os.OpenFile(
		filepath.Join(servicePath, "testmethod.json"), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		t.Fatalf("Cannot open %s file: %v", methodFile.Name(), err)
	}
	defer methodFile.Close()
	if _, err = methodFile.WriteString(method); err != nil {
		t.Fatalf("Cannot write %s to file %s: %v", method, methodFile.Name(), err)
	}
	return
}

type expDir struct {
	path  string
	pkg   string
	funcs []string
}

func testDirs(t *testing.T, exp []expDir, merge bool) {
	in := []string{
		"schema/gh.com/user/proj/schema/definitions.json",
		"schema/gh.com/user/proj/schema/subdir/this.json",
		"schema/gh.com/user/proj/schema/subdir2/next.json",
		"schema/gh.com/user/proj/other/jsons/definitions.json",
		"schema/gh.com/user/proj/other/jsons/sub/file.son",
		"schema/gh.com/user/proj/other/jsons/sub2/n.json",
		"schema/gh.com/other/schema/definitions.json",
		"schema/gh.com/other/schema/service/next.json",
		"schema/gh.com/user/proj/schema/next_sch/definitions.json",
		"schema/gh.com/user/proj/schema/next_sch/js.json",
		"schema/bitbucket.org/user/proj/definitions.json",
		"schema/bitbucket.org/user/proj/sub/next.json",
	}
	out := []string{
		"src/gh.com/user/proj/schema/source_test.go",
		"src/gh.com/user/proj/schema/subdirnext/file.go",
		"src/gh.com/user/proj/schema/next_sch/s.go",
		"src/gh.com/user/proj/other/jsons/file.go",
		"src/gh.com/user/proj/other/direct/f.go",
		"src/bitbucket.org/user/proj/subdir/so.go",
	}

	tdir, err := ioutil.TempDir(os.TempDir(), "")
	if err != nil {
		t.Fatalf("want err=nil; got %q", err)
	}

	defer os.RemoveAll(tdir)
	// Dump in and out to filesystem.
	for _, p := range append(in, out...) {
		f := filepath.Join(tdir, filepath.FromSlash(p))
		err := os.MkdirAll(filepath.Dir(f), 0775)
		if err != nil {
			t.Fatalf("want err=nil; got %q", err)
		}
		var file *os.File
		file, err = os.Create(f)
		if err != nil {
			t.Fatalf("want err=nil; got %q", err)
		}
		if filepath.Base(f) == definitionsFile {
			_, err = file.WriteString(fmt.Sprintf(defJSONTest, idDefinition))
			if err != nil {
				t.Fatalf("want err=nil; got %q", err)
			}
		} else {
			_, err = file.WriteString(fmt.Sprintf(JSONTest, ""))
			if err != nil {
				t.Fatalf("want err=nil; got %q", err)
			}
		}
		if err = file.Close(); err != nil {
			t.Fatalf("want err=nil; got %q", err)
		}
	}

	os.Setenv("GOPATH", tdir)
	err = Glob(merge)
	if err != nil {
		t.Fatalf("want err=nil; got %q", err)
	}
	// Gather current state of filesystem for used directory.
	var files []string
	err = filepath.Walk(filepath.Join(tdir, "src"), func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatalf("want err=nil; got %q", err)
	}

	// Check if current directory structure is equal to expected.
	if len(files) != len(exp) {
		t.Fatalf("want len(files)=len(exp); got %d!=%d", len(files), len(exp))
	}
	for _, exp := range exp {
		g := filepath.Join(tdir, exp.path)
		inf, err := os.Stat(g)
		if err != nil {
			t.Fatalf("want err=nil; got %q", err)
		}
		if inf.IsDir() {
			t.Fatalf("want %q to be file", exp.path)
		}
		if exp.pkg != "" || exp.funcs != nil {
			cnt, err := ioutil.ReadFile(g)
			if err != nil {
				t.Fatalf("want err=nil; got %q", err)
			}
			if exp.pkg != "" && !strings.Contains(string(cnt), fmt.Sprintf("package %v", exp.pkg)) {
				t.Errorf("want content (%s) to contain \"%s\"", string(cnt), fmt.Sprintf("package %v", exp.pkg))
			}
			for _, f := range exp.funcs {
				if !strings.Contains(string(cnt), fmt.Sprintf(": %s,", f)) {
					t.Errorf("want content (%s) to contain \"%s\"", string(cnt), fmt.Sprintf(": %s", f))
				}
			}
		}
	}
}

func TestGlobMerge(t *testing.T) {
	exp := []expDir{
		{"src/gh.com/user/proj/schema/source_test.go", "", nil},
		{"src/gh.com/user/proj/schema/subdirnext/file.go", "", nil},
		{"src/gh.com/user/proj/schema/schema.go", "schema", []string{"this", "next"}},
		{"src/gh.com/user/proj/schema/bind.go", "schema", nil},
		{"src/gh.com/user/proj/schema/next_sch/s.go", "", nil},
		{"src/gh.com/user/proj/schema/next_sch/bind.go", "next_sch", nil},
		{"src/gh.com/user/proj/schema/next_sch/schema.go", "next_sch", []string{"js"}},
		{"src/gh.com/user/proj/other/jsons/file.go", "", nil},
		{"src/gh.com/user/proj/other/jsons/schema.go", "jsons", []string{"n"}},
		{"src/gh.com/user/proj/other/jsons/bind.go", "jsons", nil},
		{"src/gh.com/user/proj/other/direct/f.go", "", nil},
		{"src/bitbucket.org/user/proj/subdir/so.go", "", nil},
		{"src/bitbucket.org/user/proj/bind.go", "", nil},
		{"src/bitbucket.org/user/proj/schema.go", "proj", []string{"next"}},
	}
	testDirs(t, exp, true)
}

func TestGlobNoMerge(t *testing.T) {
	exp := []expDir{
		{"src/gh.com/user/proj/schema/source_test.go", "", nil},
		{"src/gh.com/user/proj/schema/subdirnext/file.go", "", nil},
		{"src/gh.com/user/proj/schema/subdir/schema.go", "subdir", []string{"this"}},
		{"src/gh.com/user/proj/schema/subdir/bind.go", "subdir", nil},
		{"src/gh.com/user/proj/schema/subdir2/schema.go", "subdir2", []string{"next"}},
		{"src/gh.com/user/proj/schema/subdir2/bind.go", "subdir2", nil},
		{"src/gh.com/user/proj/schema/next_sch/s.go", "", nil},
		{"src/gh.com/user/proj/schema/next_sch/bind.go", "next_sch", nil},
		{"src/gh.com/user/proj/schema/next_sch/schema.go", "next_sch", []string{"js"}},
		{"src/gh.com/user/proj/other/jsons/file.go", "", nil},
		{"src/gh.com/user/proj/other/jsons/sub2/schema.go", "sub2", []string{"n"}},
		{"src/gh.com/user/proj/other/jsons/sub2/bind.go", "sub2", nil},
		{"src/gh.com/user/proj/other/direct/f.go", "", nil},
		{"src/bitbucket.org/user/proj/subdir/so.go", "", nil},
		{"src/bitbucket.org/user/proj/sub/bind.go", "", nil},
		{"src/bitbucket.org/user/proj/sub/schema.go", "sub", []string{"next"}},
	}
	testDirs(t, exp, false)
}
