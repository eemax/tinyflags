package main

import (
	"os"

	"github.com/eemax/tinyflags/internal/cli"
)

func main() {
	app := cli.NewApp(os.Stdout, os.Stderr)
	os.Exit(app.Execute(os.Args[1:]))
}
