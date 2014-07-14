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

	gobd "github.com/jteeuwen/go-bindata"
)

var (
	// definitionsFile is a json file which should contain all definitions
	// refered in other shemas.
	definitionsFile = `definitions.json`
	// definitions map contains partialy parsed json-schema definitions
	// grouped by their names.
	definitions map[string]interface{}

	// services is a helper map that contains service name as key and
	// path to temporarily created folder for marshaled methods.
	services = make(map[string]string)

	// mergeSchemas if enabled schemgen creates one schema.go file which
	// contain schemas from all subdirectories.
	mergeSchemas = true
)

const (
	NoDefinitionsErr        = `schemagen: invalid %s file format(missing definitions)`
	MissingDefinitionsErr   = `schemagen: missing definitions`
	MissingOneDefinitionErr = `schemagen: missing definition %s`
	SchemaHasDefinitionsErr = `schemagen: %s file must not have "definitions" filed %#v`
	CannotOpenFileErr       = `schemagen: cannot open file: %v`
	CannotWriteToFileErr    = `schemagen: cannot write binding template to file %s: %v`
)

// loadDefinitions reads all definitions from `definitionsFile` file which needs
// to be located in 'schemaInBase' directory. If this function fails the program
// will not parse schema files which contain '$ref' field.
func loadDefinitions(schemaInBase string) (err error) {
	data, err := ioutil.ReadFile(filepath.Join(schemaInBase, definitionsFile))
	if err != nil {
		return
	}
	if err = json.Unmarshal(data, &definitions); err != nil {
		return
	}
	var ok bool
	if definitions, ok = definitions[`definitions`].(map[string]interface{}); !ok {
		return fmt.Errorf(NoDefinitionsErr, definitionsFile)
	}
	return
}

// findReferences recursively searches schema for `$ref` token and,
// if found token has #/definitions/* structure, adds definition name
// into a return slice.
func findReferences(schema map[string]interface{}) []string {
	refs := make([]string, 0)
	for name, cont := range schema {
		switch reflect.ValueOf(cont).Kind() {
		case reflect.Map:
			refs = append(refs, findReferences(cont.(map[string]interface{}))...)
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
func makeDefinitions(req []string) (map[string]interface{}, error) {
	if definitions == nil || (len(definitions) == 0 && len(req) != 0) {
		return nil, fmt.Errorf(MissingDefinitionsErr)
	}
	def := make(map[string]interface{})
	for _, tok := range req {
		content, ok := definitions[tok]
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
func dumpToTmpDirs(path string, data []byte) (err error) {
	service := filepath.Base(filepath.Dir(path))
	if mergeSchemas {
		service = "schema"
	}
	fName := strings.TrimSuffix(filepath.Base(path), ".json")
	if _, ok := services[service]; !ok {
		dir, err := ioutil.TempDir("", "schema_bin")
		if err != nil {
			return err
		}
		services[service] = dir
	}
	file, err := os.OpenFile(
		filepath.Join(services[service], fName), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return
	}
	return nil
}

// walkFunc is executed for each nondefinition JSON schema file.
// It creates unmarshaled interface map, injects referenced definitions into it,
// and dumps to temporary directory in order to further processing.
func walkFunc(path string, info os.FileInfo, extErr error) error {
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
		def, err := makeDefinitions(findReferences(mapSchema))
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
		if err := dumpToTmpDirs(path, marshaled); err != nil {
			return err
		}
	}
	return nil
}

// createPaths if necessary, creates service named folders in output path.
func createPaths(schemaOutBase string) (err error) {
	for s, _ := range services {
		if err = os.MkdirAll(filepath.Join(schemaOutBase, s), 0755); err != nil {
			return
		}
	}
	return
}

// saveAsGoBinData creates a `schema.go` source file for each parsed service.
// Output file contains a compressed data representation of parsed schemas
// and `_bindata` map which keys represent json methods' name.
func saveAsGoBinData(schemaOutBase string) (err error) {
	for s, path := range services {
		bdCfg := &gobd.Config{
			Package:   s,
			Input:     []gobd.InputConfig{gobd.InputConfig{Path: path}},
			Output:    filepath.Join(schemaOutBase, s, "schema.go"),
			Prefix:    path,
			Recursive: true,
		}
		if err = gobd.Translate(bdCfg); err != nil {
			return
		}
	}
	return
}

// createBindSchemaFiles makes additional bind.go file. The file contains
// Schemas map which has ready to use JSON schema documents.
func createBindSchemaFiles(schemaOutBase string) (err error) {
	for s, _ := range services {
		file, err := os.OpenFile(filepath.Join(
			schemaOutBase, s, "bind.go"), os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0755)
		if err != nil {
			return fmt.Errorf(CannotOpenFileErr, err)
		}
		defer file.Close()
		_, err = file.WriteString(fmt.Sprintf(bindTemplate, s))
		if err != nil {
			return fmt.Errorf(CannotWriteToFileErr, file.Name(), err)
		}
	}
	return
}

// Merge if set true schemgen creates one schema.go file which
// contain schemas from all subdirectories.
func Merge(merge bool) { mergeSchemas = merge }

// Generate loads definitions from schemaInBase/definitions.json file and
// uses them with other JSON schemas got from folders representing service
// name. If function successed schemaOutBase directory will contain exacly
// the same folder structure as in schemaInBase. Each folder will have
// a schema.go file with binarized schemas collected in '_bindata' map.
func Generate(schemaInBase, schemaOutBase string) (err error) {
	if err = loadDefinitions(schemaInBase); err != nil {
		log.Println(`Cannot read `, definitionsFile, ` file: `, err)
	}
	if err = filepath.Walk(schemaInBase, walkFunc); err != nil {
		return
	}
	if err = createPaths(schemaOutBase); err != nil {
		return
	}
	if err = saveAsGoBinData(schemaOutBase); err != nil {
		return
	}
	if err = createBindSchemaFiles(schemaOutBase); err != nil {
		return
	}
	return
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
