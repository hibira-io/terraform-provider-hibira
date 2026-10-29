// Command terraform-provider-hibira is the Terraform provider for Hibira.
//
// Regenerate docs/ from the schema, examples/ and templates/:
//
//go:generate go tool tfplugindocs generate --provider-name hibira
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/hibira-io/terraform-provider-hibira/internal/provider"
)

// version is set by goreleaser.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/hibira-io/hibira",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
