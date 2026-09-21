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

	capture, err := openCapture()
	if err != nil {
		if videoFile != "" {
			log.Fatalln("failed to open video capture:", err)
		}
		log.Println("initial connection failed:", err)
		capture = reconnect()
	}
	window := cv.NewWindow("video capture " + fmt.Sprint(idc))

	var detector *Detector
	if tracking {
		detector, err = NewDetector("models/yolov4-tiny.weights", "models/yolov4-tiny.cfg", "models/coco.names")
		if err != nil {
			log.Fatalln("failed to load detector:", err)
		}
		defer detector.Close()
	}

	img := cv.NewMat()
	defer img.Close()

	if ok := capture.Read(&img); !ok {
		if videoFile != "" {
			log.Fatalln("failed to read a frame from video capture to matrix")
		}
		capture = reconnect()
		if ok := capture.Read(&img); !ok {
			log.Fatalln("failed to read a frame right after reconnecting")
		}
	}

	videoWriter, err := createVideoWriter(&img, idc)
	if err != nil {
		log.Fatalln("failed to create video writer:", err)
	}
	defer videoWriter.Close()

	for {
		if ok := capture.Read(&img); !ok {
			if videoFile != "" {
				log.Fatalln("failed to read a frame from video capture to matrix")
			}
			log.Println("lost connection to camera, reconnecting...")
			capture = reconnect()
			continue
		}

		var detectionInfo string
		if detector != nil {
			persons, cars := detector.Detect(&img)
			detectionInfo = fmt.Sprintf(", persons=%d cars=%d", persons, cars)
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

		fmt.Printf("%s new frame (%s, %s)%s\n", t, img.Type(), xy, detectionInfo)
	}
}

func openCapture() (*cv.VideoCapture, error) {
	if videoFile != "" {
		fmt.Println("opening from video file")
		return cv.OpenVideoCapture(videoFile)
	}
	url := fmt.Sprintf("rtsp://%s:%s@%s:%s/mode=real&idc=%d&ids=1", user, password, address, port, idc)
	return cv.OpenVideoCapture(url)
}

// reconnect blocks, retrying with exponential backoff (capped at 30s), until
// the RTSP stream is reachable again and yielding an actual frame. Only
// meant for the live RTSP path: a -file input has no "reconnect", it just
// runs out of frames.
func reconnect() *cv.VideoCapture {
	backoff := 2 * time.Second
	for {
		capture, err := openCapture()
		if err == nil {
			probe := cv.NewMat()
			ok := capture.Read(&probe)
			probe.Close()
			if ok {
				log.Printf("reconnected to camera idc=%d\n", idc)
				return capture
			}
		}
		capture.Close()

		log.Printf("camera idc=%d unreachable, retrying in %s\n", idc, backoff)
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
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
