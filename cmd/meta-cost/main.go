// Command meta-cost observes local Muse usage and estimates its cost.
package main

import (
	"io/fs"
	"os"

	metacostcenter "github.com/omenking/meta-costcenter"
	"github.com/omenking/meta-costcenter/internal/costcenter"
)

func main() {
	web, err := fs.Sub(metacostcenter.Web, "web")
	if err != nil {
		panic(err)
	}
	os.Exit(costcenter.Main(os.Args[1:], costcenter.Assets{Web: web, Pricing: metacostcenter.Pricing}))
}
