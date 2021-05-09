package main

import (
	"flag"
	"fmt"
	"log"

	"gocv.io/x/gocv"
)

var (
	user     string
	password string
	idc      string
)

func init() {
	log.SetFlags(0)
	flag.StringVar(&user, "user", "", "")
	flag.StringVar(&password, "password", "", "")
	flag.StringVar(&idc, "idc", "", "")
}

func main() {
	flag.Parse()
	url := fmt.Sprintf("rtsp://%s:%s@192.168.1.100:554/mode=real&idc=%s&ids=1", user, password, idc)
	capture, err := gocv.OpenVideoCapture(url)
	if err != nil {
		log.Fatalln("failed to open video capture:", err)
	}
	window := gocv.NewWindow("capture")
	img := gocv.NewMat()

	for {
		capture.Read(&img)
		window.IMShow(img)
		window.WaitKey(1)
	}
}
