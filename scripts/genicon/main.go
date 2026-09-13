//go:build ignore

// genicon renders the xcut brand mark (see internal/brandicon) into an
// .ico file for the Windows exe resource step:
//
//	go run scripts/genicon/main.go -out xcut.ico
//
// The committed .syso in cmd/xcut/ is generated FROM this icon by rsrc;
// regenerate both whenever the mark changes.
package main

import (
	"flag"

	"log"
	"os"

	"github.com/xiabee/XCut/internal/brandicon"
)

func main() {
	out := flag.String("out", "xcut.ico", "output .ico path")
	flag.Parse()

	ico, err := brandicon.BrandICO()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, ico, 0o644); err != nil {
		log.Fatal(err)
	}
}
