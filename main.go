// Command golem is a thin HTTP client for the golem control-plane v1 API that
// builders run inside their devcontainer/Codespace. All privilege lives in the
// API; this binary just shapes requests, attaches the Bearer key, and renders
// the result.
//
// Usage:
//
//	golem whoami
//	golem status
//	golem publish [--force]
//	golem config list | get KEY | set KEY=VALUE | rm KEY
//	golem env set KEY=VALUE
//	golem secret set KEY[=VALUE] | rm KEY
//	golem logs [--stream console|errors|ci] [--follow]
//	golem schedules list | sync
//	golem restart
//	golem open
//	golem version
//	golem help
//
// Configuration is read from the environment:
//
//	GOLEM_API_KEY  (required) — Bearer token for every API call.
//	GOLEM_API_URL  (optional) — API base, defaults to https://platform.tools.deadnet.co
package main

import (
	"fmt"
	"os"

	"github.com/deadnetco/golem-cli/internal/cli"
)

// version is stamped at build time via:
//
//	go build -ldflags "-X main.version=v0.1.0" -o golem .
//
// It defaults to "dev" for un-stamped local builds.
var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, "golem: "+err.Error())
		os.Exit(1)
	}
}
