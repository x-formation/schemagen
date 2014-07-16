// NAME:
//	 schemagen - Schema generator
//
// USAGE:
//	 schemagen                                    Run in glob mode.
//	 schemagen --separate                         Run in glob mode creating seperate schemas per service.
//	 schemagen --input . --output dir             Run for single input directory.
//	 schemagen --input . --output dir --separate  Run for single input directory creating seperate schemas per service.
//	 schemagen --help                             Show this message.`

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/x-formation/schemagen"
)

var (
	merge bool
	in    string
	out   string
	h     bool
)

const usage = `NAME:
	schemagen - Schema generator

USAGE:
	schemagen                                    Run in glob mode.
	schemagen --separate                         Run in glob mode creating seperate schemas per service.
	schemagen --input . --output dir             Run for single input directory.
	schemagen --input . --output dir --separate  Run for single input directory creating seperate schemas per service.
	schemagen --help                             Show this message.
`

func init() {
	flag.BoolVar(&merge, "separate", merge, "Generate go schemas per service.")
	flag.StringVar(&in, "input", in, "JSON files input directory.")
	flag.StringVar(&out, "output", out, "Go source files output directory.")
	flag.BoolVar(&h, "help", h, "Show this message.")
	flag.Usage = func() {
		fmt.Print(usage)
		os.Exit(1)
	}
}

func main() {
	flag.Parse()
	if h {
		fmt.Print(usage)
		return
	}
	if flag.NArg() != 0 || (in != "") != (out != "") {
		fmt.Fprintf(os.Stderr, usage)
		os.Exit(1)
	}
	var err error
	schg := schemagen.New(!merge)
	if in != "" {
		err = schg.Generate(in, out)
	} else {
		err = schg.Glob()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	return
}
