package skins

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/bedrock-tool/bedrocktool/utils"

	"github.com/google/subcommands"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sirupsen/logrus"
)

type SkinProxyCMD struct {
	server_address     string
	filter             string
	only_with_geometry bool
}

func (*SkinProxyCMD) Name() string     { return "skins-proxy" }
func (*SkinProxyCMD) Synopsis() string { return "download skins from players on a server with proxy" }

func (c *SkinProxyCMD) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.server_address, "address", "", "remote server address")
	f.StringVar(&c.filter, "filter", "", "player name filter prefix")
	f.BoolVar(&c.only_with_geometry, "only-geom", false, "only save skins with geometry")
}

func (c *SkinProxyCMD) Usage() string {
	return c.Name() + ": " + c.Synopsis() + "\n" + utils.SERVER_ADDRESS_HELP
}

func (c *SkinProxyCMD) Execute(ctx context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	address, hostname, err := utils.ServerInput(ctx, c.server_address)
	if err != nil {
		logrus.Error(err)
		return 1
	}
	out_path := fmt.Sprintf("skins/%s", hostname)
	os.MkdirAll(out_path, 0o755)

	proxy := utils.NewProxy(logrus.StandardLogger())
	proxy.PacketCB = func(pk packet.Packet, proxy *utils.ProxyContext, toServer bool) (packet.Packet, error) {
		if !toServer {
			process_packet_skins(proxy.Client, out_path, pk, c.filter, c.only_with_geometry)
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
	utils.RegisterCommand(&SkinProxyCMD{})
}
