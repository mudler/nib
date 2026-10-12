//go:build ignore

// Normal generation reproduces provenance.json. -refresh resolves main once;
// -revision selects an explicitly reviewed immutable refresh instead.
package main

import (
	"flag"
	"fmt"
	"github.com/mudler/nib/llmprovider/catalog/internal/cataloggen"
	"net/http"
	"os"
)

func main() {
	refresh := flag.Bool("refresh", false, "refresh from upstream main (resolve once)")
	revision := flag.String("revision", "", "refresh from an explicit immutable commit")
	flag.Parse()
	if err := run(*refresh, *revision); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(refresh bool, revision string) error {
	if refresh && revision != "" {
		return fmt.Errorf("choose -refresh or -revision, not both")
	}
	updating := refresh || revision != ""
	if refresh {
		var e error
		revision, e = cataloggen.ResolveMain(http.DefaultClient)
		if e != nil {
			return e
		}
	} else if !updating {
		p, e := cataloggen.ReadProvenance(".")
		if e != nil {
			return e
		}
		revision = p.Revision
	}
	summary, e := cataloggen.Generate(http.DefaultClient, cataloggen.SourceURL(revision), ".", revision, updating)
	if e != nil {
		return e
	}
	fmt.Print(summary)
	return nil
}
