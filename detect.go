package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"

	cv "gocv.io/x/gocv"
)

const (
	detectInputSize  = 416
	detectConfThresh = 0.5
	detectNMSThresh  = 0.4
)

// classes we care about for this CCTV feed; everything else COCO can detect
// (chairs, bottles, etc.) is ignored.
var detectWanted = map[string]bool{
	"person": true,
	"car":    true,
}

type Detector struct {
	net        cv.Net
	outNames   []string
	classNames []string
}

func NewDetector(weightsPath, cfgPath, namesPath string) (*Detector, error) {
	data, err := os.ReadFile(namesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read class names: %w", err)
	}
	classNames := strings.Split(strings.TrimSpace(string(data)), "\n")

	net := cv.ReadNet(weightsPath, cfgPath)
	if net.Empty() {
		return nil, fmt.Errorf("failed to load network from %s / %s", weightsPath, cfgPath)
	}
	if err := net.SetPreferableBackend(cv.NetBackendDefault); err != nil {
		return nil, fmt.Errorf("failed to set backend: %w", err)
	}
	if err := net.SetPreferableTarget(cv.NetTargetCPU); err != nil {
		return nil, fmt.Errorf("failed to set target: %w", err)
	}

	outNames := outputLayerNames(&net)

	return &Detector{net: net, outNames: outNames, classNames: classNames}, nil
}

func (d *Detector) Close() error {
	return d.net.Close()
}

// outputLayerNames resolves the names of YOLO's unconnected output layers
// (yolov4-tiny has two: one per detection scale). GetUnconnectedOutLayers
// returns 1-based layer indices, matching OpenCV's C++ API.
func outputLayerNames(net *cv.Net) []string {
	layerNames := net.GetLayerNames()
	indices := net.GetUnconnectedOutLayers()

	names := make([]string, 0, len(indices))
	for _, idx := range indices {
		names = append(names, layerNames[idx-1])
	}
	return names
}

// Detect runs YOLOv4-tiny on img and draws boxes for any detected person or
// car directly onto it. Returns how many of each were found.
func (d *Detector) Detect(img *cv.Mat) (persons int, cars int) {
	blob := cv.BlobFromImage(*img, 1/255.0, image.Pt(detectInputSize, detectInputSize), cv.NewScalar(0, 0, 0, 0), true, false)
	defer blob.Close()

	d.net.SetInput(blob, "")
	outs := d.net.ForwardLayers(d.outNames)
	defer func() {
		for _, out := range outs {
			out.Close()
		}
	}()

	imgW, imgH := img.Cols(), img.Rows()

	var boxes []image.Rectangle
	var confidences []float32
	var classIDs []int

	for _, out := range outs {
		for row := 0; row < out.Rows(); row++ {
			objectness := out.GetFloatAt(row, 4)
			if objectness < detectConfThresh {
				continue
			}

			bestClass := -1
			bestScore := float32(0)
			for c := 5; c < out.Cols(); c++ {
				score := out.GetFloatAt(row, c)
				if score > bestScore {
					bestScore = score
					bestClass = c - 5
				}
			}
			if bestClass < 0 || bestClass >= len(d.classNames) {
				continue
			}
			className := d.classNames[bestClass]
			if !detectWanted[className] {
				continue
			}

			confidence := objectness * bestScore
			if confidence < detectConfThresh {
				continue
			}

			centerX := out.GetFloatAt(row, 0) * float32(imgW)
			centerY := out.GetFloatAt(row, 1) * float32(imgH)
			width := out.GetFloatAt(row, 2) * float32(imgW)
			height := out.GetFloatAt(row, 3) * float32(imgH)

			left := int(centerX - width/2)
			top := int(centerY - height/2)

			boxes = append(boxes, image.Rect(left, top, left+int(width), top+int(height)))
			confidences = append(confidences, confidence)
			classIDs = append(classIDs, bestClass)
		}
	}

	if len(boxes) == 0 {
		return 0, 0
	}
	// gocv's NMSBoxes panics on an empty input slice, hence the guard above.
	kept := cv.NMSBoxes(boxes, confidences, detectConfThresh, detectNMSThresh)

	for _, i := range kept {
		box := boxes[i]
		className := d.classNames[classIDs[i]]

		boxColor := color.RGBA{R: 0, G: 255, B: 0, A: 0}
		if className == "car" {
			boxColor = color.RGBA{R: 0, G: 128, B: 255, A: 0}
			cars++
		} else {
			persons++
		}

		cv.Rectangle(img, box, boxColor, 2)
		label := fmt.Sprintf("%s %.0f%%", className, confidences[i]*100)
		cv.PutText(img, label, image.Pt(box.Min.X, box.Min.Y-8), cv.FontHersheyPlain, 1.2, boxColor, 2)
	}

	return persons, cars
}
