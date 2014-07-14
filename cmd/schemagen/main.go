package main

import (
	"log"
	"os"

	"github.com/codegangsta/cli"
	"github.com/x-formation/schemagen"
)

var (
	// schemaInBase is a base *.json files directory.
	schemaInBase = ``
	// schemaOutBase is an output directory for binarized schemas.
	schemaOutBase = ``
	// each service will contain its own schema.go file.
	separate = false
)

func init() {
	app := cli.NewApp()
	app.Name = "schemagen"
	app.Usage = "generate *.go files from JSON schema"
	app.Flags = []cli.Flag{
		cli.StringFlag{Name: "input, i", Usage: "JSON files directory"},
		cli.StringFlag{Name: "output, o", Usage: "Go source files output directory"},
		cli.BoolFlag{Name: "separate, s", Usage: "generate Go schemas per service"},
	}
	app.Action = func(c *cli.Context) {
		schemaInBase = c.String("input")
		schemaOutBase = c.String("output")
		separate = c.Bool("separate")
	}
	app.Run(os.Args)
}

func main() {
	schemagen.Merge(!separate)
	if err := schemagen.Generate(schemaInBase, schemaOutBase); err != nil {
		log.Fatal(err)
	}
}
