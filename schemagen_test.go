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

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type schemaGenSuite struct{}

var _ = Suite(&schemaGenSuite{})

const (
	defJSONTest  = `{"$schema": "http://json-schema.org/draft-04/schema#", %s}`
	idDefinition = `"definitions": {"id": { "type": "integer", "minimum": 1}}`
	JSONTest     = `{"type": "object","properties": { %s "id": {"$ref": "#/definitions/id"}}}`
)

func (s *schemaGenSuite) SetUpTest(c *C) {
	services = make(map[string]string)
	mergeSchemas = false
	definitions = nil
}

func (s *schemaGenSuite) TestLoadDefinitions(c *C) {
	// no definitions.json file.
	noDefPath, err := ioutil.TempDir(os.TempDir(), "schemafail")
	c.Assert(err, IsNil)

	tests := []string{
		noDefPath,
		newSchemaJsonDir(c, "/*Invalid schema{", " "),
		newSchemaJsonDir(c, fmt.Sprintf(defJSONTest, `"id": 32`), " "),
	}
	for _, path := range tests {
		err = loadDefinitions(path)
		c.Assert(err, NotNil)
		c.Assert(definitions, IsNil)
	}

	// valid definitions.json schema.
	path := newSchemaJsonDir(c, fmt.Sprintf(defJSONTest, idDefinition), " ")
	err = loadDefinitions(path)
	c.Assert(err, IsNil)
	c.Assert(definitions, NotNil)
}

func (s *schemaGenSuite) TestFindReferences(c *C) {
	refs := findReferencesTest(c, fmt.Sprintf(JSONTest, ""))
	c.Assert(refs, HasLen, 1)
	c.Check(refs[0], Equals, "id")

	refs = findReferencesTest(c, `{ "id": 54 }`)
	c.Assert(refs, HasLen, 0)
}

func (s *schemaGenSuite) TestMakeDefinitions(c *C) {
	// nil definitions.
	c.Assert(definitions, IsNil)
	defsmap, err := makeDefinitions([]string{"id"})
	c.Assert(err, NotNil)
	// empty definitions non empty refs.
	definitions = make(map[string]interface{})
	c.Assert(definitions, NotNil)
	defsmap, err = makeDefinitions([]string{"id"})
	c.Assert(err, NotNil)

	// should extract id definition.
	definitions = nil
	path := newSchemaJsonDir(c, fmt.Sprintf(defJSONTest, idDefinition), " ")
	err = loadDefinitions(path)
	c.Assert(err, IsNil)
	c.Assert(definitions, NotNil)
	defsmap, err = makeDefinitions([]string{"id"})
	c.Assert(err, IsNil)
	_, ok := defsmap["id"]
	c.Check(ok, Equals, true)
}

func (s *schemaGenSuite) TestDumpToTmpDirs(c *C) {
	testPaths := map[string]string{
		filepath.Join("service1", "method1"):   "service1",
		filepath.Join("service2", "method1"):   "service2",
		filepath.Join("service3", "method100"): "service3",
		filepath.Join("service4", "method200"): "service4",
	}

	mergeSchemas = false
	for path, serv := range testPaths {
		err := dumpToTmpDirs(path, []byte("sth"))
		c.Assert(err, IsNil)
		_, ok := services[serv]
		c.Check(ok, Equals, true)
	}

	mergeSchemas = true
	services = make(map[string]string)
	for path, serv := range testPaths {
		err := dumpToTmpDirs(path, []byte("sth"))
		c.Assert(err, IsNil)
		_, ok := services[serv]
		c.Check(ok, Equals, false)
	}
	_, ok := services["schema"]
	c.Check(ok, Equals, true)
}

func (s *schemaGenSuite) TestSaveAsGoBinData(c *C) {
	services["testservice"] = filepath.Join(
		newSchemaJsonDir(c, "def", "\x44\x55\x50\x41"), "testservice")
	out, err := ioutil.TempDir(os.TempDir(), "out")
	c.Assert(err, IsNil)
	err = os.Mkdir(filepath.Join(out, "testservice"), 0755)
	c.Assert(err, IsNil)

	err = saveAsGoBinData(out)
	c.Assert(err, IsNil)
	f, err := os.Open(filepath.Join(out, "testservice", "schema.go"))
	c.Assert(err, IsNil)
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

	c.Check(strings.Index(string(content), byteSearch), Not(Equals), -1)
}

func (s *schemaGenSuite) TestCreateBindSchemaFiles(c *C) {
	services["testservice"] = filepath.Join(
		newSchemaJsonDir(c, "def", "cont"), "testservice")
	out, err := ioutil.TempDir(os.TempDir(), "out")
	c.Assert(err, IsNil)
	err = os.Mkdir(filepath.Join(out, "testservice"), 0755)
	c.Assert(err, IsNil)

	err = createBindSchemaFiles(out)
	c.Assert(err, IsNil)
	f, err := os.Open(filepath.Join(out, "testservice", "bind.go"))
	c.Assert(err, IsNil)
	defer f.Close()
	content, err := ioutil.ReadAll(f)

	c.Check(strings.Index(string(content), "package testservice"), Not(Equals), -1)
	c.Check(strings.Index(string(content), "(\"testservice: "), Not(Equals), -1)
}

func (s *schemaGenSuite) TestGenerateNoMerge(c *C) {
	inPath := newSchemaJsonDir(c,
		fmt.Sprintf(defJSONTest, idDefinition), fmt.Sprintf(JSONTest, ""))
	outPath, err := ioutil.TempDir(os.TempDir(), "out")
	c.Assert(err, IsNil)

	tests := map[string]Checker{
		filepath.Join(outPath, "testservice", "schema.go"): IsNil,
		filepath.Join(outPath, "testservice", "bind.go"):   IsNil,
		filepath.Join(outPath, "schema", "schema.go"):      NotNil,
		filepath.Join(outPath, "schema", "bind.go"):        NotNil,
	}

	err = Generate(inPath, outPath)
	c.Assert(err, IsNil)
	for path, checker := range tests {
		_, err = os.Stat(path)
		c.Check(err, checker)
	}
}

func (s *schemaGenSuite) TestGenerateMerge(c *C) {
	inPath := newSchemaJsonDir(c,
		fmt.Sprintf(defJSONTest, idDefinition), fmt.Sprintf(JSONTest, ""))
	outPath, err := ioutil.TempDir(os.TempDir(), "out")
	c.Assert(err, IsNil)

	tests := map[string]Checker{
		filepath.Join(outPath, "testservice", "schema.go"): NotNil,
		filepath.Join(outPath, "testservice", "bind.go"):   NotNil,
		filepath.Join(outPath, "schema", "schema.go"):      IsNil,
		filepath.Join(outPath, "schema", "bind.go"):        IsNil,
	}

	mergeSchemas = true
	err = Generate(inPath, outPath)
	c.Assert(err, IsNil)

	err = Generate(inPath, outPath)
	c.Assert(err, IsNil)
	for path, checker := range tests {
		_, err = os.Stat(path)
		c.Check(err, checker)
	}
}

func findReferencesTest(c *C, schema string) []string {
	var mapSchema map[string]interface{}
	err := json.Unmarshal([]byte(schema), &mapSchema)
	c.Assert(err, IsNil)
	return findReferences(mapSchema)
}

func newSchemaJsonDir(c *C, definitions, method string) (path string) {
	// write definitions.json file.
	path, err := ioutil.TempDir(os.TempDir(), "schema")
	if err != nil {
		c.Fatal("Cannot create a temporary folder", err)
	}
	defFile, err := os.OpenFile(
		filepath.Join(path, "definitions.json"), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		c.Fatalf("Cannot open %s file: %v", defFile.Name(), err)
	}
	defer defFile.Close()
	if _, err = defFile.WriteString(definitions); err != nil {
		c.Fatalf("Cannot write %s to file %s: %v", definitions, defFile.Name(), err)
	}
	// write service temp method content.
	servicePath := filepath.Join(path, "testservice")
	if err := os.Mkdir(servicePath, 0755); err != nil {
		c.Fatalf("Cannot create %s directory: %v", servicePath, err)
	}
	methodFile, err := os.OpenFile(
		filepath.Join(servicePath, "testmethod.json"), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		c.Fatalf("Cannot open %s file: %v", methodFile.Name(), err)
	}
	defer methodFile.Close()
	if _, err = methodFile.WriteString(method); err != nil {
		c.Fatalf("Cannot write %s to file %s: %v", method, methodFile.Name(), err)
	}
	return
}
