package main

import (
	"os"

	"github.com/Miku0139oao/linode-agent/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
