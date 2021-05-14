package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	cv "gocv.io/x/gocv"
)

var (
	user     string
	password string
	address  string
	port     string
	idc      int
)

func init() {
	log.SetFlags(0)
	flag.IntVar(&idc, "idc", 1, "camera number")

	err := godotenv.Load()
	if err != nil {
		log.Fatalln("failed to load environment variables")
	}

	user = os.Getenv("USER")
	password = os.Getenv("PASSWORD")
	address = os.Getenv("ADDRESS")
	port = os.Getenv("PORT")
}

func main() {
	flag.Parse()

	url := fmt.Sprintf("rtsp://%s:%s@%s:%s/mode=real&idc=%d&ids=1", user, password, address, port, idc)
	capture, err := cv.OpenVideoCapture(url)
	if err != nil {
		log.Fatalln("failed to open video capture:", err)
	}
	window := cv.NewWindow("video capture " + fmt.Sprint(idc))

	img := cv.NewMat()
	defer img.Close()

	if ok := capture.Read(&img); !ok {
		log.Fatalln("failed to read a frame from video capture to matrix")
	}

	filename := fmt.Sprintf("cam_%d.mkv", idc)
	videoWriter, err := cv.VideoWriterFile(filename, "X264", 10, img.Cols(), img.Rows(), true)
	if err != nil {
		log.Fatalln("failed to create VideoWriter:", err)
	}
	defer videoWriter.Close()

	for {
		if ok := capture.Read(&img); !ok {
			log.Fatalln("failed to read a frame from video capture to matrix")
		}

		window.IMShow(img)
		window.WaitKey(1)

		err = videoWriter.Write(img)
		if err != nil {
			log.Fatalln("failed to write a frame to VideoWriter")
		}

		fmt.Printf("new frame: type: %s, bytes: %s\n", img.Type(), lenReadable(len(img.ToBytes()), 2))
	}
}
