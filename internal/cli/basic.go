package cli

import (
	"encoding/json"
	"fmt"

	"github.com/xiabee/XCut/internal/version"
	"github.com/xiabee/XCut/internal/xcerr"
)

func versionShort() string { return version.Version }

func init() {
	register("version", "print build information", cmdVersion)
}

func cmdVersion(a *App, _ []string) error {
	fmt.Fprintln(a.Stdout, version.String())
	return nil
}

func init() {
	register("config", "show or validate configuration (config show)", cmdConfig)
}

func cmdConfig(a *App, args []string) error {
	if len(args) != 1 || (args[0] != "show" && args[0] != "path") {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut config show|path", nil)
	}
	switch args[0] {
	case "path":
		fmt.Fprintln(a.Stdout, a.CfgPath)
	case "show":
		b, err := json.MarshalIndent(a.Cfg, "", "  ")
		if err != nil {
			return xcerr.E(xcerr.CodeInternal, "cannot serialize config", err)
		}
		fmt.Fprintln(a.Stdout, string(b))
	}
	return nil
}
