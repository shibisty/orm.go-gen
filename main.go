// Command orm-gen generates orm.Mapper methods for models, so that the orm
// maps rows and documents to them without reflection.
//
//	//go:generate go run orm-gen
//	//go:generate go run orm-gen -type User,Post -o models_orm.go
//
// Flags:
//
//	-type   comma-separated struct types (default: every struct with db tags)
//	-o      output file (default orm_gen.go, or orm_gen_test.go with -tests)
//	-tests  read _test.go files too, for models declared in tests
//	-check  do not write; exit 1 if the output file is missing or out of date (CI)
//
// The package directory is the argument (default the current directory).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("orm-gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	types := fs.String("type", "", "comma-separated struct types (default: every struct with db tags)")
	out := fs.String("o", "", "output file (default orm_gen.go, or orm_gen_test.go with -tests)")
	tests := fs.Bool("tests", false, "read _test.go files too")
	check := fs.Bool("check", false, "exit 1 if the output file is out of date instead of writing it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "orm-gen: one package directory at most")
		return 2
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	o := Options{Dir: dir, Tests: *tests}
	if *types != "" {
		for _, t := range strings.Split(*types, ",") {
			if t = strings.TrimSpace(t); t != "" {
				o.Types = append(o.Types, t)
			}
		}
	}
	src, _, err := Generate(o)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	name := *out
	if name == "" {
		name = "orm_gen.go"
		if *tests {
			name = "orm_gen_test.go"
		}
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(dir, name)
	}
	if *check {
		cur, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(bytes.ReplaceAll(cur, []byte("\r\n"), []byte("\n")), src) {
			fmt.Fprintf(stderr, "orm-gen: %s is out of date; run go generate\n", name)
			return 1
		}
		fmt.Fprintf(stdout, "%s is up to date\n", name)
		return 0
	}
	if err := os.WriteFile(name, src, 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "orm-gen: wrote %s\n", name)
	return 0
}
