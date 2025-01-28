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
    parser.add_argument('--no-save', action='store_true', help="whether to save the video")
    parser.add_argument('--tracking', action='store_true', help='whether to track human movement')
    return parser.parse_args()

def check_env_vars():
    required_vars = ['DVR_USER', 'DVR_PASSWORD', 'DVR_ADDRESS', 'DVR_PORT']
    for var in required_vars:
        value = os.getenv(var)
        if not value:
            raise ValueError(f"{var} is empty")
    return {var.lower().replace('dvr_', ''): os.getenv(var) for var in required_vars}

def main():
    args = parse_args()
    env_vars = check_env_vars()

    # print(cv2.getBuildInformation())
    
    # Add YOLO initialization
    net = cv2.dnn.readNet("yolo/yolov3.weights", "yolo/yolov3.cfg")
    layer_names = net.getLayerNames()
    output_layers = [layer_names[i - 1] for i in net.getUnconnectedOutLayers()]
    with open("yolo/coco.names", "r") as f:
        classes = f.read().strip().split("\n")

    net.setPreferableBackend(cv2.dnn.DNN_BACKEND_OPENCV)
    net.setPreferableTarget(cv2.dnn.DNN_TARGET_CPU)
    
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
    
    height, width = frame.shape[:2]

    artifacts_dir_path = Path.cwd() / 'recordings'
    artifacts_dir_path.mkdir(exist_ok=True)
    
    raw_filename = f"idc{args.idc}_{uuid.uuid4()}.mp4"
    raw_full_path = str(artifacts_dir_path / raw_filename)

    annotated_filename = f"idc{args.idc}_{uuid.uuid4()}.mp4"
    annotated_full_path = str(artifacts_dir_path / annotated_filename)

    # fourcc = cv2.VideoWriter.fourcc('X', '2', '6', '4')
    fourcc = cv2.VideoWriter.fourcc('m', 'p', '4', 'v')

    raw_video_writer = cv2.VideoWriter(raw_full_path, fourcc, fps=15, frameSize=(width, height))
    annotated_video_writer = cv2.VideoWriter(annotated_full_path, fourcc, fps=15, frameSize=(width, height))

    frame_number = 0
    try:
        while True:
            ret, frame = capture.read()
            if not ret:
                break

            frame_number += 1

            if not args.no_save:
                raw_video_writer.write(frame)

            if args.tracking:
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

                            print(f"detected {classes[class_id]} at {x=} {y=} {w=} {h=}, frame {frame_number}")

                indexes = cv2.dnn.NMSBoxes(boxes, confidences, score_threshold=0.5, nms_threshold=0.4)

                for i in range(len(boxes)):
                    if i in indexes:
                        x, y, w, h = boxes[i]
                        label = str(classes[class_ids[i]])
                        cv2.rectangle(frame, (x, y), (x + w, y + h), (0, 255, 0), 2)
                        cv2.putText(frame, label, (x, y - 10), cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 255, 0), 2)
            
            if not args.no_save:
                annotated_video_writer.write(frame)
            
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
    except KeyboardInterrupt:
        if args.no_save:
            if os.path.exists(raw_full_path):
                os.remove(raw_full_path)
            if os.path.exists(annotated_full_path):
                os.remove(annotated_full_path)
            
    finally:
        capture.release()
        raw_video_writer.release()
        annotated_video_writer.release()
        cv2.destroyAllWindows()

if __name__ == "__main__":
    main() 
