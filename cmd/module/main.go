package main

import (
	"multiplexer"

	// Components: all built-in component APIs implement DoCommand.
	_ "go.viam.com/rdk/components/register_apis"

	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"

	// Services: only the ones whose typed clients implement DoCommand.
	// services/register_apis would also pull in mlmodel and baseremotecontrol,
	// whose clients don't define DoCommand and would always return "unimplemented".
	_ "go.viam.com/rdk/services/datamanager"
	_ "go.viam.com/rdk/services/discovery"
	generic "go.viam.com/rdk/services/generic"
	_ "go.viam.com/rdk/services/motion"
	_ "go.viam.com/rdk/services/navigation"
	_ "go.viam.com/rdk/services/shell"
	_ "go.viam.com/rdk/services/slam"
	_ "go.viam.com/rdk/services/video"
	_ "go.viam.com/rdk/services/vision"
	_ "go.viam.com/rdk/services/worldstatestore"
)

func main() {
	module.ModularMain(resource.APIModel{API: generic.API, Model: multiplexer.ResourceMultiplexer})
}
