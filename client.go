package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/credentials"

	_ "embed"
	_ "image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text"

	pb "github.com/WalisNw/clock-in-out-client/proto"

	"google.golang.org/grpc"
)

var (
	_host     string
	_port     string
	_insecure string
	_version  string
	_id       string
)

const (
	ScreenWidth        = 480
	ScreenHeight       = 270
	Padding            = 18
	Row                = 24
	RegularTermination = "terminate"
	AlertInterval      = 480
	CountDownInterval  = 300
	DateTimeLayout = "2006/01/02 15:04:05"
)

var (
	//go:embed static/font.ttf
	ttf         []byte
	regularFont font.Face

	//go:embed static/checked.png
	checkedPng   []byte
	checkedImage *ebiten.Image

	//go:embed static/unchecked.png
	uncheckedPng   []byte
	uncheckedImage *ebiten.Image
)

type Flag uint16

const (
	Connecting Flag = 1 << iota
	Loading
	Select
	Clock
	Record
	Clocking
	Querying
	CountDown
	Result
)

type Func uint8

const (
	FuncClock Func = 1 << iota
	FuncQuery
)

func init() {
	tt, err := opentype.Parse(ttf)
	if err != nil {
		log.Printf("Failed to parse ttf. err: %v", err)
		os.Exit(1)
	}
	regularFont, err = opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    18,
		DPI:     72,
		Hinting: font.HintingFull,
	})

	checkedImage = ebiten.NewImageFromImage(loadImage(checkedPng, "checked.png"))
	uncheckedImage = ebiten.NewImageFromImage(loadImage(uncheckedPng, "unchecked.png"))
}

func loadImage(i []byte, name string) image.Image {
	img, _, err := image.Decode(bytes.NewReader(i))
	if err != nil {
		log.Printf("Failed to load image [%v]. err: %v", name, err)
		os.Exit(1)
	}
	return img
}

func repeatingKeyPressed(key ebiten.Key) bool {
	const (
		delay    = 30
		interval = 3
	)
	d := inpututil.KeyPressDuration(key)
	if d == 1 {
		return true
	}
	if d >= delay && (d-delay)%interval == 0 {
		return true
	}
	return false
}

type Game struct {
	gRPC      *gRPC
	counter   uint16
	flag      Flag
	funcFlag  Func
	tick      uint
	alert     string
	msg       string
	clockType pb.ClockType
	queryType pb.QueryType
	records   []*pb.Record
}

func (g *Game) Update() error {
	switch g.flag {
	case Connecting:
		g.flag = Loading
		go func() {
			fmt.Println("connecting...")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var (
				opts []grpc.DialOption
			)
			if _insecure != "" {
				opts = append(opts, grpc.WithInsecure())
			} else {
				opts = append(opts, grpc.WithAuthority(_host))
				cred := credentials.NewTLS(&tls.Config{
					InsecureSkipVerify: false,
				})
				opts = append(opts, grpc.WithTransportCredentials(cred))
			}
			opts = append(opts, grpc.WithBlock())
			conn, err := grpc.DialContext(ctx, fmt.Sprintf("%s:%s", _host, _port), opts...)
			if err != nil {
				log.Printf("Failed to connect. err: %v", err)
				g.alert = "連線異常"
				g.tick = AlertInterval
				g.flag = Connecting
				return
			}
			g.gRPC.conn = conn
			g.gRPC.client = pb.NewClockServiceClient(conn)
			g.flag = Select
			g.funcFlag = FuncClock
		}()
	case Loading:
		g.msg = "連線中"
	case Select:
		switch {
		case repeatingKeyPressed(ebiten.KeyEnter), repeatingKeyPressed(ebiten.KeyArrowRight):
			switch g.funcFlag {
			case FuncClock:
				g.flag = Clock
			case FuncQuery:
				g.flag = Record
			}
		case repeatingKeyPressed(ebiten.KeyArrowUp):
			g.funcFlag = FuncClock
		case repeatingKeyPressed(ebiten.KeyArrowDown):
			g.funcFlag = FuncQuery
		}
	case Clock:
		switch {
		case repeatingKeyPressed(ebiten.KeyEnter), repeatingKeyPressed(ebiten.KeyArrowRight):
			g.flag = Clocking
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				id, _ := strconv.Atoi(_id)
				res, err := g.gRPC.client.Clock(ctx, &pb.ClockRequest{Member: &pb.Member{Id: int32(id)}, Type: g.clockType})
				if err != nil {
					log.Printf("Failed to clock in/out. err: %v", err)
					g.alert = "打卡失敗"
					g.tick = AlertInterval
					g.flag = Select
					return
				}
				g.msg = fmt.Sprintf("%v %v", res.Result, res.Time.AsTime().Local().Format(DateTimeLayout))
				g.flag = CountDown
				g.tick = CountDownInterval
			}()
		case repeatingKeyPressed(ebiten.KeyArrowUp):
			g.clockType = pb.ClockType_CLOCK_IN
		case repeatingKeyPressed(ebiten.KeyArrowDown):
			g.clockType = pb.ClockType_CLOCK_OUT
		case repeatingKeyPressed(ebiten.KeyArrowLeft):
			g.flag = Select
		}
	case Record:
		switch {
		case repeatingKeyPressed(ebiten.KeyEnter), repeatingKeyPressed(ebiten.KeyArrowRight):
			g.flag = Querying
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				id, _ := strconv.Atoi(_id)
				res, err := g.gRPC.client.Query(ctx, &pb.QueryRequest{Member: &pb.Member{Id: int32(id)}, Type: g.queryType})
				if err != nil {
					log.Printf("Failed to query records. err: %v", err)
					g.alert = "查詢失敗"
					g.tick = AlertInterval
					g.flag = Select
					return
				}
				g.records = res.Records
				g.flag = Result
			}()
		case repeatingKeyPressed(ebiten.KeyArrowUp):
			g.queryType = pb.QueryType_DAY
		case repeatingKeyPressed(ebiten.KeyArrowDown):
			g.queryType = pb.QueryType_LAST_SEVEN
		case repeatingKeyPressed(ebiten.KeyArrowLeft):
			g.flag = Select
		}
	case Clocking, Querying:
		g.msg = "請稍候"
	case CountDown:
		if repeatingKeyPressed(ebiten.KeyEnter) || g.tick == 0 {
			fmt.Println("Shutdown!")
			return errors.New(RegularTermination)
		}
	case Result:
		if repeatingKeyPressed(ebiten.KeyArrowLeft) {
			g.flag = Record
		}
	}
	g.counter++
	g.tick--
	if g.tick == 0 {
		g.alert = ""
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	text.Draw(screen, fmt.Sprintf("現在時間: %s", time.Now().Format(DateTimeLayout)), regularFont, Padding, Padding+Row, color.White)
	text.Draw(screen, g.alert, regularFont, Padding, Padding+Row*2, color.RGBA64{R: 0xffff, A: 0xff00})
	switch g.flag {
	case Select:
		text.Draw(screen, "請選擇:", regularFont, Padding, Padding+Row*5, color.White)
		op := &ebiten.DrawImageOptions{}
		if g.funcFlag == FuncClock {
			op.GeoM.Translate(Padding, Row*7+2)
			screen.DrawImage(checkedImage, op)
			op.GeoM.Translate(0, Row)
			screen.DrawImage(uncheckedImage, op)
		} else {
			op.GeoM.Translate(Padding, Row*8+2)
			screen.DrawImage(checkedImage, op)
			op.GeoM.Translate(0, Row*-1)
			screen.DrawImage(uncheckedImage, op)
		}
		text.Draw(screen, "打卡", regularFont, Padding+20, Padding+Row*7, color.White)
		text.Draw(screen, "查詢", regularFont, Padding+20, Padding+Row*8, color.White)
	case Clock:
		text.Draw(screen, "請選擇:", regularFont, Padding, Padding+Row*5, color.White)
		op := &ebiten.DrawImageOptions{}
		if g.clockType == pb.ClockType_CLOCK_IN {
			op.GeoM.Translate(Padding, Row*7+2)
			screen.DrawImage(checkedImage, op)
			op.GeoM.Translate(0, Row)
			screen.DrawImage(uncheckedImage, op)
		} else {
			op.GeoM.Translate(Padding, Row*8+2)
			screen.DrawImage(checkedImage, op)
			op.GeoM.Translate(0, Row*-1)
			screen.DrawImage(uncheckedImage, op)
		}
		text.Draw(screen, "上班打卡", regularFont, Padding+20, Padding+Row*7, color.White)
		text.Draw(screen, "下班打卡", regularFont, Padding+20, Padding+Row*8, color.White)
	case Record:
		text.Draw(screen, "請選擇:", regularFont, Padding, Padding+Row*5, color.White)
		op := &ebiten.DrawImageOptions{}
		if g.queryType == pb.QueryType_DAY {
			op.GeoM.Translate(Padding, Row*7+2)
			screen.DrawImage(checkedImage, op)
			op.GeoM.Translate(0, Row)
			screen.DrawImage(uncheckedImage, op)
		} else {
			op.GeoM.Translate(Padding, Row*7+2)
			screen.DrawImage(uncheckedImage, op)
			op.GeoM.Translate(0, Row)
			screen.DrawImage(checkedImage, op)
		}
		text.Draw(screen, "本日", regularFont, Padding+20, Padding+Row*7, color.White)
		text.Draw(screen, "前七日", regularFont, Padding+20, Padding+Row*8, color.White)

	case Loading, Clocking, Querying:
		msg := g.msg
		msg += strings.Repeat(".", int(g.counter)%180/30)
		text.Draw(screen, msg, regularFont, Padding, Padding+Row*5, color.White)
	case CountDown:
		text.Draw(screen, g.msg, regularFont, Padding, Padding+Row*5, color.White)
		text.Draw(screen, fmt.Sprintf("將於 %d 秒後自動關閉或按<Enter>直接關閉", (g.tick/60)+1), regularFont, Padding, Padding+Row*7, color.White)
	case Result:
		text.Draw(screen, "日期", regularFont, Padding, Padding+Row*3, color.White)
		text.Draw(screen, "上班", regularFont, 200, Padding+Row*3, color.White)
		text.Draw(screen, "下班", regularFont, 340, Padding+Row*3, color.White)
		for i, r := range g.records {
			text.Draw(screen, r.Date, regularFont, Padding, Padding+Row*(4+i), color.White)
			text.Draw(screen, r.In, regularFont, 200, Padding+Row*(4+i), color.White)
			text.Draw(screen, r.Out, regularFont, 340, Padding+Row*(4+i), color.White)
		}
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

type gRPC struct {
	conn   *grpc.ClientConn
	client pb.ClockServiceClient
}

func (g *gRPC) close() {
	if g.conn != nil {
		fmt.Printf("client disconnected")
		_ = g.conn.Close()
	}
}

func NewGame() (*Game, func()) {
	g := &Game{gRPC: &gRPC{}, flag: Connecting}
	return g, g.gRPC.close
}

func main() {
	ebiten.SetWindowTitle(fmt.Sprintf("NW 打卡系統 - v%s", _version))
	ebiten.SetWindowSize(ScreenWidth, ScreenHeight)
	g, closeConn := NewGame()
	defer closeConn()
	if err := ebiten.RunGame(g); err != nil && err.Error() != RegularTermination {
		log.Printf("an error occurred: %v", err)
	}
}
