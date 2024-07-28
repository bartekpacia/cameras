package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
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

	user = os.Getenv("DVR_USER")
	if user == "" {
		log.Fatalln("DVR_USER is empty")
	}

	password = os.Getenv("DVR_PASSWORD")
	if password == "" {
		log.Fatalln("DVR_PASSWORD is empty")
	}

	address = os.Getenv("DVR_ADDRESS")
	if address == "" {
		log.Fatalln("DVR_ADDRESS is empty")
	}

	port = os.Getenv("DVR_PORT")
	if port == "" {
		log.Fatalln("DVR_PORT is empty")
	}
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

	videoWriter, err := createVideoWriter(&img, idc)
	if err != nil {
		log.Fatalln("failed to create video writer:", err)
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

func createVideoWriter(img *cv.Mat, idc int) (*cv.VideoWriter, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working dir: %w", err)
	}
	dirPath := filepath.Join(dir, "recordings")

	if _, err := os.Stat(dirPath); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dirPath, os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create recordings dir: %v", err)
		}
	}

	fileName := fmt.Sprintf("idc%d_%s.mkv", idc, uuid.New())
	fullPath := filepath.Join(dirPath, fileName)

	videoWriter, err := cv.VideoWriterFile(fullPath, "X264", 15, img.Cols(), img.Rows(), true)
	return videoWriter, err
}
