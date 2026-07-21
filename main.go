// terraform-provider-openinfra lets you declare open-infra resources in HCL.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/harn3ss/terraform-provider-openinfra/internal/provider"
)

// version is set at release time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/harn3ss/openinfra",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
