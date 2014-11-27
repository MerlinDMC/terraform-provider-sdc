package main

import (
	sdc "github.com/MerlinDMC/terraform-provider-sdc/provider"
	"github.com/hashicorp/terraform/plugin"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: sdc.Provider,
	})
}
