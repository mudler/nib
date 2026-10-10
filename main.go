package main

import (
	"os"

	"github.com/mudler/nib/app"
	"github.com/mudler/nib/codeindex"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == codeindex.InternalWorkerArg {
		if err := codeindex.ServeWorker(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(app.Main(os.Args))
}
