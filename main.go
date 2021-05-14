package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"gocv.io/x/gocv"
)

var (
	user     string
	password string
	address  string
	port     string
	idc      string
)

func init() {
	log.SetFlags(0)

	err := godotenv.Load()
	if err != nil {
		log.Fatalln("failed to load environment variables")
	}

	user = os.Getenv("USER")
	password = os.Getenv("PASSWORD")
	address = os.Getenv("ADDRESS")
	port = os.Getenv("PORT")
	idc = os.Getenv("IDC")
}

func main() {
	flag.Parse()
	url := fmt.Sprintf("rtsp://%s:%s@%s:%s/mode=real&idc=%s&ids=1", user, password, address, port, idc)
	capture, err := gocv.OpenVideoCapture(url)
	if err != nil {
		log.Fatalln("failed to open video capture:", err)
	}
	window := gocv.NewWindow("video capture " + idc)
	img := gocv.NewMat()

	for {
		capture.Read(&img)
		window.IMShow(img)
		window.WaitKey(1)

		fmt.Printf("type: %s, bytes: %d\n", img.Type(), len(img.ToBytes()))
	}
}
