// Command pvm-exit is the standalone pvm exit server for a VPS.
package main

import (
	"os"

	"github.com/huangzheng2016/pvm/internal/buildinfo"
	"github.com/huangzheng2016/pvm/internal/exitsvc"
)

func main() {
	os.Exit(exitsvc.Main(os.Args[1:], buildinfo.Version))
}
