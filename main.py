import cv2
import os
import argparse
import uuid
import time
from pathlib import Path
import numpy as np

def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument('--idc', type=int, default=1, help='camera number')
    parser.add_argument('--file', type=str, default='', help='video file to read from instead of RTSP')
    parser.add_argument('--tracking', action='store_true', help='whether to track human movement')
    return parser.parse_args()

def check_env_vars():
    required_vars = ['DVR_USER', 'DVR_PASSWORD', 'DVR_ADDRESS', 'DVR_PORT']
    for var in required_vars:
        value = os.getenv(var)
        if not value:
            raise ValueError(f"{var} is empty")
    return {var.lower().replace('dvr_', ''): os.getenv(var) for var in required_vars}

def create_video_writer(frame, idc):
    dir_path = Path.cwd() / 'recordings'
    dir_path.mkdir(exist_ok=True)
    
    filename = f"idc{idc}_{uuid.uuid4()}.mkv"
    full_path = str(dir_path / filename)
    
    height, width = frame.shape[:2]
    fourcc = cv2.VideoWriter.fourcc('X', '2', '6', '4')
    return cv2.VideoWriter(full_path, fourcc, 15, (width, height))

def main():
    args = parse_args()
    env_vars = check_env_vars()
    
    # Add YOLO initialization
    net = cv2.dnn.readNet("yolo/yolov3.weights", "yolo/yolov3.cfg")
    layer_names = net.getLayerNames()
    output_layers = [layer_names[i - 1] for i in net.getUnconnectedOutLayers()]
    with open("yolo/coco.names", "r") as f:
        classes = f.read().strip().split("\n")
    
    if args.file:
        print("opening from video file")
        capture = cv2.VideoCapture(args.file)
    else:
        url = f"rtsp://{env_vars['user']}:{env_vars['password']}@{env_vars['address']}:{env_vars['port']}/mode=real&idc={args.idc}&ids=1"
        capture = cv2.VideoCapture(url)
    
    if not capture.isOpened():
        raise RuntimeError("failed to open video capture")

    window_name = f"video capture {args.idc}"
    cv2.namedWindow(window_name)
    
    ret, frame = capture.read()
    if not ret:
        raise RuntimeError("failed to read a frame from video capture")
    
    video_writer = create_video_writer(frame, args.idc)
    
    try:
        while True:
            ret, frame = capture.read()
            if not ret:
                break

            video_writer.write(frame)

            # Perform object detection using YOLO
            height, width = frame.shape[:2]
            blob = cv2.dnn.blobFromImage(image=frame, scalefactor=1/255.0, size=(416, 416), swapRB=True, crop=False)
            net.setInput(blob)
            layer_outputs = net.forward(output_layers)

            boxes = []
            confidences = []
            class_ids = []

            for output in layer_outputs:
                for detection in output:
                    scores = detection[5:]
                    class_id = np.argmax(scores)
                    confidence = scores[class_id]

                    if confidence > 0.5:
                        center_x = int(detection[0] * width)
                        center_y = int(detection[1] * height)
                        w = int(detection[2] * width)
                        h = int(detection[3] * height)

                        x = int(center_x - w/2)
                        y = int(center_y - h/2)

                        boxes.append([x, y, w, h])
                        confidences.append(float(confidence))
                        class_ids.append(class_id)

            indexes = cv2.dnn.NMSBoxes(boxes, confidences, score_threshold=0.5, nms_threshold=0.4)

            for i in range(len(boxes)):
                if i in indexes:
                    x, y, w, h = boxes[i]
                    label = str(classes[class_ids[i]])
                    cv2.rectangle(frame, (x, y), (x + w, y + h), (0, 255, 0), 2)
                    cv2.putText(frame, label, (x, y - 10), cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 255, 0), 2)
            
            cv2.imshow(window_name, frame)
            if cv2.waitKey(1) & 0xFF == ord('q'):  # Add 'q' to quit
                break
            
            timestamp = ''
            if not args.file:
                now = time.localtime()
                timestamp = f"{now.tm_hour:02d}:{now.tm_min:02d}:{now.tm_sec:02d}"
            
            height, width = frame.shape[:2]
            frame_type = frame.dtype
            print(f"{timestamp} new frame ({frame_type}, {height}x{width})")
            
    finally:
        capture.release()
        video_writer.release()
        cv2.destroyAllWindows()

if __name__ == "__main__":
    main() 
