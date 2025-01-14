package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/bedrock-tool/bedrocktool/utils"

	_ "github.com/bedrock-tool/bedrocktool/subcommands"
	_ "github.com/bedrock-tool/bedrocktool/subcommands/skins"
	_ "github.com/bedrock-tool/bedrocktool/subcommands/world"

	"github.com/google/subcommands"
	"github.com/sirupsen/logrus"
)

func main() {
	defer func() {
		if err := recover(); err != nil {
			logrus.Errorf("Fatal Error occurred.")
			println("")
			println("--COPY FROM HERE--")
			logrus.Infof("Version: %s", utils.Version)
			logrus.Infof("Cmdline: %s", os.Args)
			logrus.Errorf("Error: %s", err)
			fmt.Println("stacktrace from panic: \n" + string(debug.Stack()))
			println("--END COPY HERE--")
			println("")
			println("if you want to report this error, please open an issue at")
			println("https://github.com/bedrock-tool/bedrocktool/issues")
			println("And attach the error info, describe what you did to get this error.")
			println("Thanks!\n")
			if utils.G_interactive {
				input := bufio.NewScanner(os.Stdin)
				input.Scan()
			}
			os.Exit(1)
		}
	}()

	logrus.SetLevel(logrus.DebugLevel)
	if utils.Version != "" {
		logrus.Infof("bedrocktool version: %s", utils.Version)
	}

	newVersion, err := utils.Updater.UpdateAvailable()
	if err != nil {
		logrus.Error(err)
	}

	if newVersion != "" {
		logrus.Infof("Update Available: %s", newVersion)
	}

	ctx, cancel := context.WithCancel(context.Background())

	flag.BoolVar(&utils.G_debug, "debug", false, "debug mode")
	flag.BoolVar(&utils.G_preload_packs, "preload", false, "preload resourcepacks for proxy")
	enable_dns := flag.Bool("dns", false, "enable dns server for consoles")

	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.ImportantFlag("debug")
	subcommands.ImportantFlag("dns")
	subcommands.ImportantFlag("preload")
	subcommands.HelpCommand()

	{ // interactive input
		if len(os.Args) < 2 {
			select {
			case <-ctx.Done():
				return
			default:
				fmt.Println("Available commands:")
				for name, desc := range utils.ValidCMDs {
					fmt.Printf("\t%s\t%s\n", name, desc)
				}
				fmt.Printf("Use '%s <command>' to run a command\n", os.Args[0])

				cmd, cancelled := utils.User_input(ctx, "Input Command: ")
				if cancelled {
					return
				}
				os.Args = append(os.Args, cmd)
				utils.G_interactive = true
			}
		}
	}

	flag.Parse()

	if *enable_dns {
		utils.InitDNS()
	}

	// exit cleanup
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		println("cancelling")
		cancel()
	}()

	subcommands.Execute(ctx)

	if utils.G_interactive {
		logrus.Info("Press Enter to exit.")
		input := bufio.NewScanner(os.Stdin)
		input.Scan()
	}
}

type TransCMD struct {
	auth bool
}

func (*TransCMD) Name() string     { return "trans" }
func (*TransCMD) Synopsis() string { return "" }

func (c *TransCMD) SetFlags(f *flag.FlagSet) {
	f.BoolVar(&c.auth, "auth", false, "if it should login to xbox")
}

func (c *TransCMD) Usage() string {
	return c.Name() + ": " + c.Synopsis() + "\n"
}

func (c *TransCMD) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	const (
		BLACK_FG = "\033[30m"
		BOLD     = "\033[1m"
		BLUE     = "\033[46m"
		PINK     = "\033[45m"
		WHITE    = "\033[47m"
		RESET    = "\033[0m"
	)
	if c.auth {
		utils.GetTokenSource()
	}
	fmt.Println(BLACK_FG + BOLD + BLUE + " Trans " + PINK + " Rights " + WHITE + " Are " + PINK + " Human " + BLUE + " Rights " + RESET)
	return 0
}

func init() {
	utils.RegisterCommand(&TransCMD{})
}
