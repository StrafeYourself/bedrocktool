package world

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bedrock-tool/bedrocktool/utils"
	"github.com/bedrock-tool/bedrocktool/utils/nbtconv"

	"github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/item"
	"github.com/df-mc/dragonfly/server/item/inventory"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/df-mc/dragonfly/server/world/mcdb"
	"github.com/df-mc/goleveldb/leveldb/opt"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/subcommands"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sandertv/gophertunnel/minecraft/resource"
	"github.com/sirupsen/logrus"
	//_ "github.com/df-mc/dragonfly/server/block" // to load blocks
	//_ "net/http/pprof"
)

type TPlayerPos struct {
	Position mgl32.Vec3
	Pitch    float32
	Yaw      float32
	HeadYaw  float32
}

type itemContainer struct {
	OpenPacket *packet.ContainerOpen
	Content    *packet.InventoryContent
}

// the state used for drawing and saving

type WorldState struct {
	ctx                context.Context
	ispre118           bool
	voidgen            bool
	chunks             map[protocol.ChunkPos]*chunk.Chunk
	blockNBT           map[protocol.SubChunkPos][]map[string]any
	openItemContainers map[byte]*itemContainer
	airRid             uint32

	Dim          world.Dimension
	WorldName    string
	ServerName   string
	worldCounter int
	packs        map[string]*resource.Pack

	withPacks           bool
	saveImage           bool
	experimentInventory bool

	PlayerPos TPlayerPos
	proxy     *utils.ProxyContext

	// ui
	ui *MapUI
}

func NewWorldState() *WorldState {
	w := &WorldState{
		chunks:             make(map[protocol.ChunkPos]*chunk.Chunk),
		blockNBT:           make(map[protocol.SubChunkPos][]map[string]any),
		openItemContainers: make(map[byte]*itemContainer),
		Dim:                nil,
		WorldName:          "world",
		PlayerPos:          TPlayerPos{},
		airRid:             6692,
	}
	w.ui = NewMapUI(w)
	return w
}

var dimension_ids = map[uint8]world.Dimension{
	0: world.Overworld,
	1: world.Nether,
	2: world.End,
	// < 1.18
	10: world.Overworld_legacy,
	11: world.Nether,
	12: world.End,
}

var (
	black_16x16  = image.NewRGBA(image.Rect(0, 0, 16, 16))
	Offset_table [24]protocol.SubChunkOffset
)

func init() {
	for i := range Offset_table {
		Offset_table[i] = protocol.SubChunkOffset{0, int8(i), 0}
	}
	draw.Draw(black_16x16, image.Rect(0, 0, 16, 16), image.Black, image.Point{}, draw.Src)
	utils.RegisterCommand(&WorldCMD{})
}

type WorldCMD struct {
	Address             string
	packs               bool
	enableVoid          bool
	saveImage           bool
	experimentInventory bool
}

func (*WorldCMD) Name() string     { return "worlds" }
func (*WorldCMD) Synopsis() string { return "download a world from a server" }

func (p *WorldCMD) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.Address, "address", "", "remote server address")
	f.BoolVar(&p.packs, "packs", false, "save resourcepacks to the worlds")
	f.BoolVar(&p.enableVoid, "void", true, "if false, saves with default flat generator")
	f.BoolVar(&p.saveImage, "image", false, "saves an png of the map at the end")
	f.BoolVar(&p.experimentInventory, "inv", false, "enable experimental block inventory saving")
}

func (c *WorldCMD) Usage() string {
	return c.Name() + ": " + c.Synopsis() + "\n" + utils.SERVER_ADDRESS_HELP
}

func (c *WorldCMD) Execute(ctx context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	/*
		go func() {
			http.ListenAndServe(":8000", nil)
		}()
	*/

	server_address, hostname, err := utils.ServerInput(ctx, c.Address)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	w := NewWorldState()
	w.voidgen = c.enableVoid
	w.ServerName = hostname
	w.withPacks = c.packs
	w.saveImage = c.saveImage
	w.experimentInventory = c.experimentInventory
	w.ctx = ctx

	proxy := utils.NewProxy(logrus.StandardLogger())
	proxy.ConnectCB = w.OnConnect
	proxy.PacketCB = func(pk packet.Packet, proxy *utils.ProxyContext, toServer bool) (packet.Packet, error) {
		var forward bool
		if toServer {
			pk, forward = w.ProcessPacketClient(pk)
		} else {
			pk, forward = w.ProcessPacketServer(pk)
		}
		if !forward {
			return nil, nil
		}
		return pk, nil
	}

	err = proxy.Run(ctx, server_address)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	w.SaveAndReset()
	return 0
}

func (w *WorldState) setnameCommand(cmdline []string) bool {
	w.WorldName = strings.Join(cmdline, " ")
	w.proxy.SendMessage(fmt.Sprintf("worldName is now: %s", w.WorldName))
	return true
}

func (w *WorldState) toggleVoid(cmdline []string) bool {
	w.voidgen = !w.voidgen
	w.proxy.SendMessage(fmt.Sprintf("using void generator: %t", w.voidgen))
	return true
}

func (w *WorldState) ProcessLevelChunk(pk *packet.LevelChunk) {
	_, exists := w.chunks[pk.Position]
	if exists {
		return
	}

	ch, blockNBTs, err := chunk.NetworkDecode(w.airRid, pk.RawPayload, int(pk.SubChunkCount), w.Dim.Range(), w.ispre118)
	if err != nil {
		logrus.Error(err)
		return
	}
	if blockNBTs != nil {
		w.blockNBT[protocol.SubChunkPos{
			pk.Position.X(), 0, pk.Position.Z(),
		}] = blockNBTs
	}

	w.chunks[pk.Position] = ch

	if pk.SubChunkRequestMode == protocol.SubChunkRequestModeLegacy {
		w.ui.SetChunk(pk.Position, ch)
	} else {
		w.ui.SetChunk(pk.Position, nil)
		// request all the subchunks

		max := w.Dim.Range().Height() / 16
		if pk.SubChunkRequestMode == protocol.SubChunkRequestModeLimited {
			max = int(pk.HighestSubChunk)
		}

		w.proxy.Server.WritePacket(&packet.SubChunkRequest{
			Dimension: int32(w.Dim.EncodeDimension()),
			Position: protocol.SubChunkPos{
				pk.Position.X(), 0, pk.Position.Z(),
			},
			Offsets: Offset_table[:max],
		})
	}
}

func (w *WorldState) ProcessSubChunk(pk *packet.SubChunk) {
	pos_to_redraw := make(map[protocol.ChunkPos]bool)

	for _, sub := range pk.SubChunkEntries {
		var (
			abs_x  = pk.Position[0] + int32(sub.Offset[0])
			abs_y  = pk.Position[1] + int32(sub.Offset[1])
			abs_z  = pk.Position[2] + int32(sub.Offset[2])
			subpos = protocol.SubChunkPos{abs_x, abs_y, abs_z}
			pos    = protocol.ChunkPos{abs_x, abs_z}
		)
		ch := w.chunks[pos]
		if ch == nil {
			logrus.Errorf("the server didnt send the chunk before the subchunk!")
			continue
		}
		blockNBT, err := ch.ApplySubChunkEntry(uint8(abs_y), &sub)
		if err != nil {
			logrus.Error(err)
		}
		if blockNBT != nil {
			w.blockNBT[subpos] = blockNBT
		}

		pos_to_redraw[pos] = true
	}

	// redraw the chunks
	for pos := range pos_to_redraw {
		w.ui.SetChunk(pos, w.chunks[pos])
	}
	w.ui.SchedRedraw()
}

func (w *WorldState) ProcessAnimate(pk *packet.Animate) {
	if pk.ActionType == packet.AnimateActionSwingArm {
		w.ui.ChangeZoom()
		w.proxy.SendPopup(fmt.Sprintf("Zoom: %d", w.ui.zoomLevel))
	}
}

func (w *WorldState) ProcessChangeDimension(pk *packet.ChangeDimension) {
	if len(w.chunks) > 0 {
		w.SaveAndReset()
	} else {
		logrus.Info("Skipping save because the world didnt contain any chunks.")
		w.Reset()
	}
	dim_id := pk.Dimension
	if w.ispre118 {
		dim_id += 10
	}
	w.Dim = dimension_ids[uint8(dim_id)]
}

func (w *WorldState) SetPlayerPos(Position mgl32.Vec3, Pitch, Yaw, HeadYaw float32) {
	last := w.PlayerPos
	w.PlayerPos = TPlayerPos{
		Position: Position,
		Pitch:    Pitch,
		Yaw:      Yaw,
		HeadYaw:  HeadYaw,
	}

	if int(last.Position.X()) != int(w.PlayerPos.Position.X()) || int(last.Position.Z()) != int(w.PlayerPos.Position.Z()) {
		w.ui.SchedRedraw()
	}
}

func (w *WorldState) Reset() {
	w.chunks = make(map[protocol.ChunkPos]*chunk.Chunk)
	w.WorldName = fmt.Sprintf("world-%d", w.worldCounter)
	w.ui.Reset()
}

// writes the world to a folder, resets all the chunks
func (w *WorldState) SaveAndReset() {
	logrus.Infof("Saving world %s  %d chunks", w.WorldName, len(w.chunks))

	// open world
	folder := path.Join("worlds", fmt.Sprintf("%s/%s", w.ServerName, w.WorldName))
	os.RemoveAll(folder)
	os.MkdirAll(folder, 0o777)

	provider, err := mcdb.New(logrus.StandardLogger(), folder, opt.DefaultCompression)
	if err != nil {
		logrus.Fatal(err)
	}

	// save chunk data
	for cp, c := range w.chunks {
		provider.SaveChunk((world.ChunkPos)(cp), c, w.Dim)
	}

	// save block nbt data
	blockNBT := make(map[protocol.ChunkPos][]map[string]any)
	for scp, v := range w.blockNBT { // 3d to 2d
		cp := protocol.ChunkPos{scp.X(), scp.Z()}
		blockNBT[cp] = append(blockNBT[cp], v...)
	}
	for cp, v := range blockNBT {
		err = provider.SaveBlockNBT((world.ChunkPos)(cp), v, w.Dim)
		if err != nil {
			logrus.Error(err)
		}
	}

	// write metadata
	s := provider.Settings()
	player := w.proxy.Server.GameData().PlayerPosition
	s.Spawn = cube.Pos{
		int(player.X()),
		int(player.Y()),
		int(player.Z()),
	}
	s.Name = w.WorldName

	// set gamerules
	ld := provider.LevelDat()
	gd := w.proxy.Server.GameData()
	for _, gr := range gd.GameRules {
		switch gr.Name {
		case "commandblockoutput":
			ld.CommandBlockOutput = gr.Value.(bool)
		case "maxcommandchainlength":
			ld.MaxCommandChainLength = int32(gr.Value.(uint32))
		case "commandblocksenabled":
			ld.CommandsEnabled = gr.Value.(bool)
		case "dodaylightcycle":
			ld.DoDayLightCycle = gr.Value.(bool)
		case "doentitydrops":
			ld.DoEntityDrops = gr.Value.(bool)
		case "dofiretick":
			ld.DoFireTick = gr.Value.(bool)
		case "domobloot":
			ld.DoMobLoot = gr.Value.(bool)
		case "domobspawning":
			ld.DoMobSpawning = gr.Value.(bool)
		case "dotiledrops":
			ld.DoTileDrops = gr.Value.(bool)
		case "doweathercycle":
			ld.DoWeatherCycle = gr.Value.(bool)
		case "drowningdamage":
			ld.DrowningDamage = gr.Value.(bool)
		case "doinsomnia":
			ld.DoInsomnia = gr.Value.(bool)
		case "falldamage":
			ld.FallDamage = gr.Value.(bool)
		case "firedamage":
			ld.FireDamage = gr.Value.(bool)
		case "keepinventory":
			ld.KeepInventory = gr.Value.(bool)
		case "mobgriefing":
			ld.MobGriefing = gr.Value.(bool)
		case "pvp":
			ld.PVP = gr.Value.(bool)
		case "showcoordinates":
			ld.ShowCoordinates = gr.Value.(bool)
		case "naturalregeneration":
			ld.NaturalRegeneration = gr.Value.(bool)
		case "tntexplodes":
			ld.TNTExplodes = gr.Value.(bool)
		case "sendcommandfeedback":
			ld.SendCommandFeedback = gr.Value.(bool)
		case "randomtickspeed":
			ld.RandomTickSpeed = int32(gr.Value.(uint32))
		case "doimmediaterespawn":
			ld.DoImmediateRespawn = gr.Value.(bool)
		case "showdeathmessages":
			ld.ShowDeathMessages = gr.Value.(bool)
		case "functioncommandlimit":
			ld.FunctionCommandLimit = int32(gr.Value.(uint32))
		case "spawnradius":
			ld.SpawnRadius = int32(gr.Value.(uint32))
		case "showtags":
			ld.ShowTags = gr.Value.(bool)
		case "freezedamage":
			ld.FreezeDamage = gr.Value.(bool)
		case "respawnblocksexplode":
			ld.RespawnBlocksExplode = gr.Value.(bool)
		case "showbordereffect":
			ld.ShowBorderEffect = gr.Value.(bool)
		// todo
		default:
			logrus.Warnf("unknown gamerule: %s\n", gr.Name)
		}
	}

	ld.RandomSeed = int64(gd.WorldSeed)

	// void world
	if w.voidgen {
		ld.FlatWorldLayers = `{"biome_id":1,"block_layers":[{"block_data":0,"block_id":0,"count":1},{"block_data":0,"block_id":0,"count":2},{"block_data":0,"block_id":0,"count":1}],"encoding_version":3,"structure_options":null}`
		ld.Generator = 2
	}

	provider.SaveSettings(s)
	provider.Close()
	w.worldCounter += 1

	for k, p := range w.packs {
		logrus.Infof("Adding resource pack: %s\n", k)
		pack_folder := path.Join(folder, "resource_packs", k)
		os.MkdirAll(pack_folder, 0o755)
		data := make([]byte, p.Len())
		p.ReadAt(data, 0)
		utils.UnpackZip(bytes.NewReader(data), int64(len(data)), pack_folder)
	}

	if w.saveImage {
		f, _ := os.Create(folder + ".png")
		png.Encode(f, w.ui.ToImage())
		f.Close()
	}

	// zip it
	filename := folder + ".mcworld"

	if err := utils.ZipFolder(filename, folder); err != nil {
		fmt.Println(err)
	}
	logrus.Infof("Saved: %s\n", filename)
	os.RemoveAll(folder)
	w.Reset()
}

func (w *WorldState) OnConnect(proxy *utils.ProxyContext) {
	w.proxy = proxy
	gd := w.proxy.Server.GameData()

	if len(gd.CustomBlocks) > 0 {
		logrus.Info("Using Custom Blocks")
		/*
			for _, be := range gd.CustomBlocks {
				b := block.ServerCustomBlock(be)
				world.RegisterBlock(b)
			}
		*/
	}

	if w.withPacks {
		go func() {
			w.packs, _ = utils.GetPacks(w.proxy.Server)
		}()
	}

	{ // check game version
		gv := strings.Split(gd.BaseGameVersion, ".")
		var err error
		if len(gv) > 1 {
			var ver int
			ver, err = strconv.Atoi(gv[1])
			w.ispre118 = ver < 18
		}
		if err != nil || len(gv) <= 1 {
			logrus.Info("couldnt determine game version, assuming > 1.18")
		}

		dim_id := gd.Dimension
		if w.ispre118 {
			logrus.Info("using legacy (< 1.18)")
			dim_id += 10
		}
		w.Dim = dimension_ids[uint8(dim_id)]
	}

	w.proxy.SendMessage("use /setname <worldname>\nto set the world name")

	w.ui.Start()
	go func() { // send map item
		select {
		case <-w.ctx.Done():
			return
		default:
			for {
				time.Sleep(1 * time.Second)
				if w.proxy.Client != nil {
					err := w.proxy.Client.WritePacket(&MAP_ITEM_PACKET)
					if err != nil {
						return
					}
				}
			}
		}
	}()

	proxy.AddCommand(utils.IngameCommand{
		Exec: w.setnameCommand,
		Cmd: protocol.Command{
			Name:        "setname",
			Description: "set user defined name for this world",
			Overloads: []protocol.CommandOverload{
				{
					Parameters: []protocol.CommandParameter{
						{
							Name:     "name",
							Type:     protocol.CommandArgTypeString,
							Optional: false,
						},
					},
				},
			},
		},
	})

	proxy.AddCommand(utils.IngameCommand{
		Exec: w.toggleVoid,
		Cmd: protocol.Command{
			Name:        "void",
			Description: "toggle if void generator should be used",
		},
	})
}

func (w *WorldState) ProcessPacketClient(pk packet.Packet) (packet.Packet, bool) {
	forward := true
	switch pk := pk.(type) {
	case *packet.MovePlayer:
		w.SetPlayerPos(pk.Position, pk.Pitch, pk.Yaw, pk.HeadYaw)
	case *packet.PlayerAuthInput:
		w.SetPlayerPos(pk.Position, pk.Pitch, pk.Yaw, pk.HeadYaw)
	case *packet.MapInfoRequest:
		if pk.MapID == VIEW_MAP_ID {
			w.ui.SchedRedraw()
			forward = false
		}
	case *packet.MobEquipment:
		if pk.NewItem.Stack.NBTData["map_uuid"] == int64(VIEW_MAP_ID) {
			forward = false
		}
	case *packet.Animate:
		w.ProcessAnimate(pk)
	}
	return pk, forward
}

// stackToItem converts a network ItemStack representation back to an item.Stack.
func stackToItem(it protocol.ItemStack) item.Stack {
	t, ok := world.ItemByRuntimeID(it.NetworkID, int16(it.MetadataValue))
	if !ok {
		t = block.Air{}
	}
	if it.BlockRuntimeID > 0 {
		// It shouldn't matter if it (for whatever reason) wasn't able to get the block runtime ID,
		// since on the next line, we assert that the block is an item. If it didn't succeed, it'll
		// return air anyway.
		b, _ := world.BlockByRuntimeID(uint32(it.BlockRuntimeID))
		if t, ok = b.(world.Item); !ok {
			t = block.Air{}
		}
	}
	//noinspection SpellCheckingInspection
	if nbter, ok := t.(world.NBTer); ok && len(it.NBTData) != 0 {
		t = nbter.DecodeNBT(it.NBTData).(world.Item)
	}
	s := item.NewStack(t, int(it.Count))
	return nbtconv.ReadItem(it.NBTData, &s)
}

func (w *WorldState) ProcessPacketServer(pk packet.Packet) (packet.Packet, bool) {
	switch pk := pk.(type) {
	case *packet.ChangeDimension:
		w.ProcessChangeDimension(pk)
	case *packet.LevelChunk:
		w.ProcessLevelChunk(pk)
		w.proxy.SendPopup(fmt.Sprintf("%d chunks loaded\nname: %s", len(w.chunks), w.WorldName))
	case *packet.SubChunk:
		w.ProcessSubChunk(pk)
	case *packet.ContainerOpen:
		if w.experimentInventory {
			// add to open containers
			existing, ok := w.openItemContainers[pk.WindowID]
			if !ok {
				existing = &itemContainer{}
			}
			w.openItemContainers[pk.WindowID] = &itemContainer{
				OpenPacket: pk,
				Content:    existing.Content,
			}
		}
	case *packet.InventoryContent:
		if w.experimentInventory {
			// save content
			fmt.Printf("WindowID: %d\n", pk.WindowID)
			existing, ok := w.openItemContainers[byte(pk.WindowID)]
			if !ok {
				if pk.WindowID == 0x0 { // inventory
					w.openItemContainers[byte(pk.WindowID)] = &itemContainer{
						Content: pk,
					}
				}
				break
			}
			existing.Content = pk
		}
	case *packet.ContainerClose:
		if w.experimentInventory {
			switch pk.WindowID {
			case protocol.WindowIDArmour: // todo handle
			case protocol.WindowIDOffHand: // todo handle
			case protocol.WindowIDUI:
			case protocol.WindowIDInventory: // todo handle
			default:
				// find container info
				existing, ok := w.openItemContainers[byte(pk.WindowID)]
				if !ok {
					logrus.Warn("Closed window that wasnt open")
					break
				}

				if existing.Content == nil {
					break
				}

				pos := existing.OpenPacket.ContainerPosition
				cp := protocol.SubChunkPos{pos.X() << 4, pos.Z() << 4}

				// create inventory
				inv := inventory.New(len(existing.Content.Content), nil)
				for i, c := range existing.Content.Content {
					item := stackToItem(c.Stack)
					inv.SetItem(i, item)
				}

				// put into subchunk
				nbts := w.blockNBT[cp]
				for i, v := range nbts {
					nbt_pos := protocol.BlockPos{v["x"].(int32), v["y"].(int32), v["z"].(int32)}
					if nbt_pos == pos {
						w.blockNBT[cp][i]["Items"] = nbtconv.InvToNBT(inv)
					}
				}

				w.proxy.SendMessage("Saved Block Inventory")

				// remove it again
				delete(w.openItemContainers, byte(pk.WindowID))
			}
		}
	}
	return pk, true
}
