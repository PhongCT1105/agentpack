// agentpack packages an agentic development environment — skills, MCP
// servers, agents, rules, commands, settings — into a portable, secrets-free
// pack directory, and restores such packs onto another machine.
package main

import (
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
