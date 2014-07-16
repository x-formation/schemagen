package schemagen

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/rjeczalik/bindata"
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
	NoDefinitionsErr        = `schemagen: invalid %s file format(missing definitions)`
	MissingDefinitionsErr   = `schemagen: missing definitions`
	MissingOneDefinitionErr = `schemagen: missing definition %s`
	SchemaHasDefinitionsErr = `schemagen: %s file must not have "definitions" filed %#v`
	CannotOpenFileErr       = `schemagen: cannot open file: %v`
	CannotWriteToFileErr    = `schemagen: cannot write binding template to file %s: %v`
	CannotReadFileErr       = `schemagen: cannot read %s, file: %v`
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
		return fmt.Errorf(NoDefinitionsErr, definitionsFile)
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
		return nil, fmt.Errorf(MissingDefinitionsErr)
	}
	def := make(map[string]interface{})
	for _, tok := range req {
		content, ok := s.definitions[tok]
		if !ok {
			return nil, fmt.Errorf(MissingOneDefinitionErr, tok)
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
		service = "schema"
	}
	fName := strings.TrimSuffix(filepath.Base(path), ".json")
	if _, ok := s.services[service]; !ok {
		dir, err := ioutil.TempDir("", "schema_bin")
		if err != nil {
			return err
		}
		s.services[service] = dir
	}
	file, err := os.OpenFile(
		filepath.Join(s.services[service], fName), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return
	}
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
func (s *schg) walkFunc() func(string, os.FileInfo, error) error {
	return func(path string, info os.FileInfo, extErr error) error {
		if extErr != nil {
			return extErr
		}
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
				return fmt.Errorf(SchemaHasDefinitionsErr, info.Name(), mapSchema)
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
		if err = os.MkdirAll(filepath.Join(schemaOutBase, serv), 0755); err != nil {
			return
		}
	}
	return
}

// saveAsGoBinData creates a `schema.go` source file for each parsed service.
// Output file contains a compressed data representation of parsed schemas
// and `_bindata` map which keys represent json methods' name.
func (s *schg) saveAsGoBinData(schemaOutBase string) (err error) {
	for serv, path := range s.services {
		bdCfg := &bindata.Config{
			Package:   serv,
			Input:     []bindata.InputConfig{bindata.InputConfig{Path: path}},
			Output:    filepath.Join(schemaOutBase, serv, "schema.go"),
			Prefix:    path,
			Recursive: true,
		}
		if err = bindata.Generate(bdCfg); err != nil {
			return
		}
	}
	return
}

// createBindSchemaFiles makes additional bind.go file. The file contains
// Schemas map which has ready to use JSON schema documents.
func (s *schg) createBindSchemaFiles(schemaOutBase string) (err error) {
	for serv := range s.services {
		file, err := os.OpenFile(filepath.Join(
			schemaOutBase, serv, outputFile), os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0755)
		if err != nil {
			return fmt.Errorf(CannotOpenFileErr, err)
		}
		defer file.Close()
		_, err = file.WriteString(fmt.Sprintf(bindTemplate, serv))
		if err != nil {
			return fmt.Errorf(CannotWriteToFileErr, file.Name(), err)
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
	if err = s.loadDefinitions(schemaInBase); err != nil {
		log.Println(fmt.Sprintf(CannotReadFileErr, definitionsFile, err))
	}
	if err = filepath.Walk(schemaInBase, s.walkFunc()); err != nil {
		return
	}
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

// Glob: TODO
func (s *schg) Glob() error {
	fmt.Printf("So we globbing\n")
	return nil
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
