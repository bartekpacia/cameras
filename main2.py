# from tutorial: https://youtu.be/kEcWUZ8unmc

import sys
from pathlib import Path

import cv2
from ultralytics import YOLO, settings
import numpy as np

import torch

backend_mps_available = torch.backends.mps.is_available()
backend_cuda_available = torch.backends.cudnn.is_available()

print(f"Metal Performance Shaders available? {backend_mps_available}")
print(f"CUDA available? {backend_cuda_available}")

settings.update({"datasets_dir": str(Path.home() / ".cache")})
print("set datasets_dir to", settings["datasets_dir"])
model = YOLO("yolo11n.pt")  # yolov8m.pt, yolo11n.pt

cap = cv2.VideoCapture(sys.argv[1])
while True:
    ret, frame = cap.read()
    if not ret:
        break

    fps = cap.get(cv2.CAP_PROP_FPS)
    print(f"{fps=}")

    results = model.predict(frame, device="mps")
    print(f'found {len(results)} results')
    for result in results:
        if not result.boxes:
            continue

        bboxes = np.array(result.boxes.xyxy.cpu(), dtype="int")
        classes = np.array(result.boxes.cls.cpu(), dtype="int")

        for cls_idx, bbox in zip(classes, bboxes):
            (x, y, w, h) = bbox
            cls_name = model.names[cls_idx]
            cv2.putText(
                img=frame,
                text=cls_name,
                org=(x, y - 10),
                fontFace=cv2.FONT_HERSHEY_SIMPLEX,
                fontScale=0.5,
                color=(0, 255, 0),
                thickness=2,
            )
            cv2.rectangle(frame, pt1=(x, y), pt2=(w, h), color=(0, 255, 0), thickness=2)

    cv2.imshow("frame", frame)
    key = cv2.waitKey(1)
    if key == ord("q"):
        break

cap.release()
cv2.destroyAllWindows()
