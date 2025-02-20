package subcommands

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bedrock-tool/bedrocktool/utils"

	"github.com/google/subcommands"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sirupsen/logrus"
)

type ChatLogCMD struct {
	Address string
	verbose bool
}

func (*ChatLogCMD) Name() string     { return "chat-log" }
func (*ChatLogCMD) Synopsis() string { return "logs chat to a file" }

func (c *ChatLogCMD) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.Address, "address", "", "remote server address")
	f.BoolVar(&c.verbose, "v", false, "verbose")
}

func (c *ChatLogCMD) Usage() string {
	return c.Name() + ": " + c.Synopsis() + "\n" + utils.SERVER_ADDRESS_HELP
}

func (c *ChatLogCMD) Execute(ctx context.Context, flags *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	address, hostname, err := utils.ServerInput(ctx, c.Address)
	if err != nil {
		logrus.Error(err)
		return 1
	}

	filename := fmt.Sprintf("%s_%s_chat.log", hostname, time.Now().Format("2006-01-02_15-04-05_Z07"))
	f, err := os.Create(filename)
	if err != nil {
		logrus.Fatal(err)
	}
	defer f.Close()

	proxy := utils.NewProxy(logrus.StandardLogger())
	proxy.PacketCB = func(pk packet.Packet, proxy *utils.ProxyContext, toServer bool) (packet.Packet, error) {
		if text, ok := pk.(*packet.Text); ok {
			logLine := text.Message
			if c.verbose {
				logLine += fmt.Sprintf("   (TextType: %d | XUID: %s | PlatformChatID: %s)", text.TextType, text.XUID, text.PlatformChatID)
			}
			f.WriteString(fmt.Sprintf("[%s] ", time.Now().Format(time.RFC3339)))
			logrus.Info(logLine)
			if toServer {
				f.WriteString("SENT: ")
			}
			f.WriteString(logLine + "\n")
		}
		return pk, nil
	}

	if err := proxy.Run(ctx, address); err != nil {
		logrus.Error(err)
		return 1
	}

	return 0
}

func init() {
	utils.RegisterCommand(&ChatLogCMD{})
}
