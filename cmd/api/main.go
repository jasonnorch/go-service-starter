// Command api is the service's entry point.
package main

import (
	"context"
	"os"

	"github.com/jasonnorch/go-service-starter/internal/app"
	"github.com/jasonnorch/go-service-starter/internal/logging"
)

// version is set at build time: -ldflags "-X main.version=$(VERSION)".
var version = "dev"

// main does nothing but translate run's result into an exit code.
//
// os.Exit skips deferred functions, so anything that must happen on the way out
// has to live in a function that returns normally. Writing os.Exit(1) inline at
// each failure point works right up until the first deferred cleanup is added,
// at which point every existing exit path silently stops running it. The split
// makes that impossible rather than something to remember.
func main() {
	os.Exit(run())
}

// run owns the process lifecycle and returns the intended exit code.
func run() int {
	if err := app.Run(context.Background(), version); err != nil {
		// The last-resort logger. A failure to load the configuration is the
		// one thing that cannot be reported through the logger that same
		// configuration describes.
		logging.Bootstrap(os.Stderr).Error("exiting", "error", err)
		return 1
	}
	return 0
}
