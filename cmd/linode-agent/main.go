package main

import (
	"os"

	"github.com/miku0139/linode-agent/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
