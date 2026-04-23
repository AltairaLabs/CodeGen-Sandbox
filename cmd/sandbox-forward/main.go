// Package main is the sandbox-forward CLI: tunnels localhost traffic from a
// developer's laptop into a remote sandbox via the OIDC-authed HTTPS API.
//
// Subcommands:
//
//	proxy      — TCP listener or stdio tunnel to /api/port-forward
//	ssh-setup  — generate ed25519 keypair, POST pubkey, write ~/.ssh/config block
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.SetFlags(0)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := dispatch(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// dispatch routes the first positional argument to the matching subcommand.
func dispatch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "proxy":
		return runProxy(ctx, args[1:])
	case "ssh-setup":
		return runSSHSetup(ctx, args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `sandbox-forward: tunnel local traffic into a remote sandbox

Usage:
  sandbox-forward proxy --server URL --port N [--local-port M] [auth flags]
  sandbox-forward proxy --server URL --ssh [auth flags]         # stdio (ProxyCommand)
  sandbox-forward proxy --server URL --port N --stdio [auth flags]
  sandbox-forward ssh-setup NAME --server URL [auth flags]

Auth flags (all subcommands):
  --bearer VALUE        Authorization: Bearer VALUE
  --bearer-file PATH    Read bearer value from file (trailing newline trimmed)
  --cookie NAME=VALUE   Cookie header (repeatable)
  --header KEY=VALUE    Arbitrary header (repeatable)
`)
}
