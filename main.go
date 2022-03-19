package main

import (
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	cv "gocv.io/x/gocv"
)

var (
	user     string
	password string
	address  string
	port     string

	idc       int
	videoFile string
	tracking  bool
)

func init() {
	log.SetFlags(0)
	flag.IntVar(&idc, "idc", 1, "camera number")
	flag.StringVar(&videoFile, "file", "", "video file to read from instead of RTSP")
	flag.BoolVar(&tracking, "tracking", false, "whether to track human movement")

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

	var capture *cv.VideoCapture
	var err error
	if videoFile != "" {
		fmt.Println("opening from video file")
		capture, err = cv.OpenVideoCapture(videoFile)
	} else {
		url := fmt.Sprintf("rtsp://%s:%s@%s:%s/mode=real&idc=%d&ids=1", user, password, address, port, idc)
		capture, err = cv.OpenVideoCapture(url)
	}

	if err != nil {
		log.Fatalln("failed to open video capture:", err)
	}
	window := cv.NewWindow("video capture " + fmt.Sprint(idc))

	img := cv.NewMat()
	defer img.Close()

	if ok := capture.Read(&img); !ok {
		log.Fatalln("failed to read a frame from video capture to matrix")
	}

	filename := fmt.Sprintf("idc%d_%s.mkv", idc, uuid.New())
	videoWriter, err := cv.VideoWriterFile(filename, "X264", 10, img.Cols(), img.Rows(), true)
	if err != nil {
		log.Fatalln("failed to create VideoWriter:", err)
	}
	defer videoWriter.Close()

	for {
		if ok := capture.Read(&img); !ok {
			log.Fatalln("failed to read a frame from video capture to matrix")
		}

		// window.IMShow(img)
		// window.WaitKey(1)
		gray1 := cv.NewMat()
		cv.CvtColor(img, &gray1, cv.ColorRGBAToGray)
		gray2 := cv.NewMat()
		cv.GaussianBlur(gray1, &gray2, image.Point{}, 100, 100, cv.BorderTransparent)

		window.IMShow(gray2)
		window.WaitKey(1)

		err = videoWriter.Write(img)
		if err != nil {
			log.Fatalln("failed to write a frame to VideoWriter")
		}

		var t string
		if videoFile != "" {
			t = ""
		} else {
			now := time.Now()
			t = fmt.Sprintf("%02d:%02d:%02d", now.Hour(), now.Minute(), now.Second())
		}

		xy := fmt.Sprintf("%dx%d", img.Rows(), img.Cols())
		size := lenReadable(len(img.ToBytes()), 2)

		fmt.Printf("%s new frame (%s, %s, %s)\n", t, img.Type(), xy, size)
	}
}
