// Copyright (c) 2017-2019 Elastos Foundation
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.
//

package script

import (
	"fmt"
	"github.com/pkg/errors"
	"os"
	"strings"

	"github.com/elastos/Elastos.ELA/cmd/common"
	"github.com/elastos/Elastos.ELA/cmd/script/api"

	"github.com/urfave/cli"
	"github.com/yuin/gopher-lua"
)

func setArgForScript(L *lua.LState, luaFileArgs []string) {
	argtb := L.NewTable()
	for j := 0; j < len(luaFileArgs); j += 2 {
		L.RawSet(argtb, lua.LString(luaFileArgs[j]),
			lua.LString(luaFileArgs[j+1]))
	}
	L.SetGlobal("arg", argtb)
}

//./ela-cli script -f "test/transaction/test_arg.lua  -publickey 1233 " --rpcport  3000
//at least should be    ./ela-cli script -f "test/transaction/test_arg.lua"
func analysisArgsForScriptNew(L *lua.LState) (err error, luaFileName string,
	luaFileArg []string) {
	//var luaFileName string
	//var luaFileArg []string

	if len(os.Args) <= 3 {
		return errors.New("os.Args length <= 3"), luaFileName, luaFileArg
	}
	for i := 3; i < len(os.Args); i++ {
		if strings.Contains(os.Args[i], ".lua") {
			luaFileArg = strings.Fields(os.Args[i])
			luaFileName = luaFileArg[0]
			luaFileArg = luaFileArg[1:]
			break
		}
	}
	return nil, luaFileName, luaFileArg
}

func scriptAction(c *cli.Context) error {
	if c.NumFlags() == 0 {
		cli.ShowSubcommandHelp(c)
		return nil
	}

	fileContent := c.String("file")
	strContent := c.String("str")
	testContent := c.String("test")

	L := lua.NewState()
	defer L.Close()
	L.PreloadModule("api", api.Loader)
	api.RegisterDataType(L)

	if strContent != "" {
		if err := L.DoString(strContent); err != nil {
			panic(err)
		}
	}

	if fileContent != "" {
		fmt.Println(os.Args)
		if len(os.Args) > 3 {
			err, luaFileName, luaFileArg := analysisArgsForScriptNew(L)
			if err != nil {
				panic(err)
			}
			setArgForScript(L, luaFileArg)
			fmt.Println(luaFileName)
			if err = L.DoFile(luaFileName); err != nil {
				panic(err)
			}
		} else {
			panic(errors.New("os.Args length <= 3"))
		}

	}

	if testContent != "" {
		fmt.Println("begin white box")
		if err := L.DoFile(testContent); err != nil {
			println(err.Error())
			os.Exit(1)
		} else {
			os.Exit(0)
		}
	}

	return nil
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:        "script",
		Usage:       "Test the blockchain via lua script",
		Description: "With ela-cli test, you could test blockchain.",
		ArgsUsage:   "[args]",
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "file, f",
				Usage: "test file",
			},
			cli.StringFlag{
				Name:  "str, s",
				Usage: "test string",
			},
			cli.StringFlag{
				Name:  "test, t",
				Usage: "white box test",
			},
		},
		Action: scriptAction,
		OnUsageError: func(c *cli.Context, err error, isSubcommand bool) error {
			common.PrintError(c, err, "script")
			return cli.NewExitError("", 1)
		},
	}
}
