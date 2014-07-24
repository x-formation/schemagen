package schemagen

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/rjeczalik/bindata"
	"github.com/rjeczalik/tools/fs/fsutil"
)

type schg struct {
	// definitions map contains partialy parsed json-schema definitions
	// grouped by their names.
	definitions map[string]interface{}

	// services is a helper map that contains service name as key and
	// path to temporarily created folder for marshaled methods.
	services map[string]string

	// merge if enabled schemgen creates one schema.go file which
	// contain schemas from all subdirectories.
	merge bool

	// pkg is name of package where merged schema.go would be stored.
	pkg string

	// tmp stores created temporary files/dirs to be removed at the end.
	tmp []string

	// defFile stores path to definitions file.
	defFile string
}

// New creates pointer to new instance of schg struct.
func New(merge bool) *schg {
	return &schg{services: make(map[string]string), merge: merge}
}

const (
	// definitionsFile is a json file which should contain all definitions
	// refered in other shemas.
	definitionsFile = `definitions.json`
	// outputFile is a Go file to which generated data will be stored.
	outputFile = `bind.go`
)

const (
	noDefinitionsErr        = `schemagen: invalid %s file format(missing definitions)`
	missingDefinitionsErr   = `schemagen: missing definitions`
	missingOneDefinitionErr = `schemagen: missing definition %s`
	schemaHasDefinitionsErr = `schemagen: %s file must not have "definitions" filed %#v`
	cannotOpenFileErr       = `schemagen: cannot open file: %v`
	cannotWriteToFileErr    = `schemagen: cannot write binding template to file %s: %v`
	cannotReadFileErr       = `schemagen: cannot read %s, file: %v`
	cannotRemoveTempDirsErr = `schemagen: cannot remove tmp dir: %v`
)

// loadDefinitions reads all definitions from `definitionsFile` file which needs
// to be located in 'schemaInBase' directory. If this function fails the program
// will not parse schema files which contain '$ref' field.
func (s *schg) loadDefinitions(schemaInBase string) (err error) {
	data, err := ioutil.ReadFile(filepath.Join(schemaInBase, definitionsFile))
	if err != nil {
		return
	}
	if err = json.Unmarshal(data, &s.definitions); err != nil {
		return
	}
	var ok bool
	if s.definitions, ok = s.definitions[`definitions`].(map[string]interface{}); !ok {
		return fmt.Errorf(noDefinitionsErr, definitionsFile)
	}
	return
}

// findReferences recursively searches schema for `$ref` token and,
// if found token has #/definitions/* structure, adds definition name
// into a return slice.
func (s *schg) findReferences(schema map[string]interface{}) []string {
	var refs []string
	for name, cont := range schema {
		switch reflect.ValueOf(cont).Kind() {
		case reflect.Map:
			refs = append(refs, s.findReferences(cont.(map[string]interface{}))...)
		case reflect.String:
			if name == `$ref` {
				toks := strings.Split(cont.(string), `/`)
				if len(toks) == 3 && toks[0] == `#` && toks[1] == `definitions` {
					refs = append(refs, toks[2])
				}
			}
		}
	}
	return refs
}

// makeDefinitions extracts required definitions form main `definitionsFile`
// schema. This guarantees that only references truely used in processing
// schema will be injected into it.
func (s *schg) makeDefinitions(req []string) (map[string]interface{}, error) {
	if s.definitions == nil || (len(s.definitions) == 0 && len(req) != 0) {
		return nil, fmt.Errorf(missingDefinitionsErr)
	}
	def := make(map[string]interface{})
	for _, tok := range req {
		content, ok := s.definitions[tok]
		if !ok {
			return nil, fmt.Errorf(missingOneDefinitionErr, tok)
		}
		def[tok] = content
	}
	return def, nil
}

// dumpToTmpDirs saves unmarshaled binary data into temporary directory.
// Each service has a separate folder required by gobindata package.
// Service name and its temporary folder are stored in `services` map.
func (s *schg) dumpToTmpDirs(path string, data []byte) (err error) {
	service := filepath.Base(filepath.Dir(path))
	if s.merge {
		service = s.pkg
	}
	fName := strings.TrimSuffix(filepath.Base(path), ".json")
	if _, ok := s.services[service]; !ok {
		dir, err := ioutil.TempDir("", "schema_bin")
		if err != nil {
			return err
		}
		s.tmp = append(s.tmp, dir)
		s.services[service] = dir
	}
	fpath := filepath.Join(s.services[service], fName)
	file, err := os.OpenFile(fpath, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	s.tmp = append(s.tmp, fpath)
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return
	}
	return nil
}

// walkFunc returns function, which is executed for each
// nondefinition JSON schema file. It creates unmarshaled interface map,
// injects referenced definitions into it and dumps to temporary directory
// in order to further processing.
func (s *schg) walkFunc() filepath.WalkFunc {
	var ignDir string
	return func(path string, info os.FileInfo, extErr error) error {
		if extErr != nil {
			return extErr
		}
		// if currently in directory with own definitions.json file,
		// we are ignoring it's content
		if ignDir != "" && strings.HasPrefix(path, ignDir) {
			return nil
		}

		// checking if current directory has independent definitions.json file
		// if that's true, we are storing info about this path in ignDir
		// and continuing ignoring this directory.
		if info.IsDir() {
			f := filepath.Join(path, definitionsFile)
			_, err := os.Stat(f)
			if (err == nil || !os.IsNotExist(err)) && f != s.defFile {
				ignDir = path
				return nil
			}
		}
		// current directory is not ignored and ignored one is left
		ignDir = ""
		if info.Name() != definitionsFile && filepath.Ext(info.Name()) == `.json` {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			var mapSchema map[string]interface{}
			if err := json.Unmarshal(data, &mapSchema); err != nil {
				return err
			}
			def, err := s.makeDefinitions(s.findReferences(mapSchema))
			if err != nil {
				return err
			}
			if _, ok := mapSchema[`definitions`]; ok {
				return fmt.Errorf(schemaHasDefinitionsErr, info.Name(), mapSchema)
			}
			// inject required definitions into processing schema.
			mapSchema[`definitions`] = def
			marshaled, err := json.Marshal(mapSchema)
			if err != nil {
				return err
			}
			if err := s.dumpToTmpDirs(path, marshaled); err != nil {
				return err
			}
		}
		return nil
	}
}

// createPaths if necessary, creates service named folders in output path.
func (s *schg) createPaths(schemaOutBase string) (err error) {
	for serv := range s.services {
		path := schemaOutBase
		if !s.merge && serv != filepath.Base(path) {
			path = filepath.Join(path, serv)
		}
		if err = os.MkdirAll(path, 0755); err != nil {
			return
		}
	}
	return
}

// saveAsGoBinData creates a `schema.go` source file for each parsed service.
// Output file contains a compressed data representation of parsed schemas
// and `_bindata` map which keys represent json methods' name.
func (s *schg) saveAsGoBinData(schemaOutBase string) (err error) {
	ch, ret := make(chan *bindata.Config, len(s.services)), make(chan error)
	for serv, path := range s.services {
		subdir := serv
		if s.merge || serv == filepath.Base(schemaOutBase) {
			subdir = ""
		}
		ch <- &bindata.Config{
			Package:   serv,
			Input:     []bindata.InputConfig{bindata.InputConfig{Path: path}},
			Output:    filepath.Join(schemaOutBase, subdir, "schema.go"),
			Prefix:    path,
			Recursive: true,
			Fmt:       true,
		}
	}
	defer close(ch)
	for n := min(runtime.GOMAXPROCS(-1), len(s.services)); n > 0; n-- {
		go func() {
			for c := range ch {
				ret <- bindata.Generate(c)
			}
		}()
	}
	var e error
	for _ = range s.services {
		if err := <-ret; err != nil {
			e = err
		}
	}
	return e
}

// createBindSchemaFiles makes additional bind.go file. The file contains
// Schemas map which has ready to use JSON schema documents.
func (s *schg) createBindSchemaFiles(schemaOutBase string) (err error) {
	for serv := range s.services {
		subdir := serv
		if s.merge || serv == filepath.Base(schemaOutBase) {
			subdir = ""
		}

		file, err := os.OpenFile(filepath.Join(
			schemaOutBase, subdir, outputFile), os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0755)
		if err != nil {
			return fmt.Errorf(cannotOpenFileErr, err)
		}
		defer file.Close()
		_, err = file.WriteString(fmt.Sprintf(bindTemplate, serv))
		if err != nil {
			return fmt.Errorf(cannotWriteToFileErr, file.Name(), err)
		}
	}
	return
}

// Generate loads definitions from schemaInBase/definitions.json file and
// uses them with other JSON schemas got from folders representing service
// name. If function successed schemaOutBase directory will contain exacly
// the same folder structure as in schemaInBase. Each folder will have
// a schema.go file with binarized schemas collected in '_bindata' map.
func (s *schg) Generate(schemaInBase, schemaOutBase string) (err error) {
	s.definitions = nil
	s.services = make(map[string]string, 0)
	s.pkg = filepath.Base(schemaOutBase)
	if schemaInBase, err = filepath.Abs(filepath.Clean(schemaInBase)); err != nil {
		return
	}
	if schemaOutBase, err = filepath.Abs(filepath.Clean(schemaOutBase)); err != nil {
		return
	}
	s.defFile = filepath.Join(schemaInBase, definitionsFile)

	if err = s.loadDefinitions(schemaInBase); err != nil {
		log.Println(fmt.Sprintf(cannotReadFileErr, definitionsFile, err))
	}
	if err = filepath.Walk(schemaInBase, s.walkFunc()); err != nil {
		return
	}
	// remove created temporary files/dirs at the end.
	defer func() {
		if e := s.dropTmpDirs(); e != nil {
			log.Println(fmt.Sprintf(cannotRemoveTempDirsErr, e))
		}
	}()
	if err = s.createPaths(schemaOutBase); err != nil {
		return
	}
	if err = s.saveAsGoBinData(schemaOutBase); err != nil {
		return
	}
	if err = s.createBindSchemaFiles(schemaOutBase); err != nil {
		return
	}
	return
}

// dropTmpDirs removes temporary files/dirs created during Generate's run.
func (s *schg) dropTmpDirs() (err error) {
	for _, p := range s.tmp {
		e := os.RemoveAll(p)
		if err == nil {
			err = e
		}
	}
	s.tmp = make([]string, 0)
	return
}

// min returns min value of two ints.
func min(i, j int) int {
	if i < j {
		return i
	}
	return j
}

type path struct{ in, out string }

// globGopath runs glob.Default.Intersect for provided gopath and returns
// slice of path data structure.
func globGopath(gopath string) (paths []path) {
	inter := fsutil.Intersect(filepath.Join(gopath, "src"),
		filepath.Join(gopath, "schema"))
	for i := range inter {
		paths = append(paths, path{filepath.Join(gopath, "schema", inter[i]),
			filepath.Join(gopath, "src", inter[i])})
	}
	return
}

// Glob generates Go source code for all JSON schemas present in directories
// specified in GOPATH variable.
func Glob(merge bool) error {
	var paths []path
	// get paths for wich Go code for JSON schemas should be generated.
	for _, p := range strings.Split(os.Getenv("GOPATH"),
		string(os.PathListSeparator)) {
		if p == "" {
			continue
		}
		paths = append(paths, globGopath(p)...)
	}
	ch, ret := make(chan path, len(paths)), make(chan error)
	for _, r := range paths {
		ch <- r
	}
	defer close(ch)
	for n := min(runtime.GOMAXPROCS(-1), len(paths)); n > 0; n-- {
		go func() {
			for c := range ch {
				ret <- New(merge).Generate(c.in, c.out)
			}
		}()
	}
	var e error
	for _ = range paths {
		if err := <-ret; err != nil {
			e = err
		}
	}
	return e
}

// bindTemplate is a generic bind.go file template used to bind raw schemas
// into gojsonschema documents.
const bindTemplate = `package %[1]s

import (
	"encoding/json"
	"fmt"

	"github.com/sigu-399/gojsonschema"
)

var Schemas = make(map[string]*gojsonschema.JsonSchemaDocument)

func init() {
	for service, schemaFunc := range _bindata {
		rawSchema, err := schemaFunc()
		if err != nil {
			panic(fmt.Sprintf("%[1]s: %%v", err))
		}
		var mapSchema interface{}
		if err := json.Unmarshal(rawSchema, &mapSchema); err != nil {
			panic(fmt.Sprintf("%[1]s: %%v", err))
		}
		s, err := gojsonschema.NewJsonSchemaDocument(mapSchema)
		if err != nil {
			panic(fmt.Sprintf("%[1]s: %%v", err))
		}
		Schemas[service] = s
	}
}
`
