# from tutorial: https://youtu.be/kEcWUZ8unmc

import sys
from pathlib import Path

import cv2
from ultralytics import YOLO, settings
import numpy as np

import torch

print(
    f"Metal Performance Shaders backend available? {torch.backends.mps.is_available()}"
)
print(f"cuda available? {torch.cuda.is_available()}")


settings.update({'datasets_dir': Path.home() / '.cache' / 'ultralytics'})
model = YOLO("yolo11n.pt")

cap = cv2.VideoCapture(sys.argv[1])
while True:
    ret, frame = cap.read()
    if not ret:
        break

    results = model(frame, device="mps")
    result = results[0]
    bboxes = np.array(result.boxes.xyxy.cpu(), dtype="int")
    classes = np.array(result.boxes.cls.cpu(), dtype="int")
    # print(result)

    for cls_idx, bbox in zip(classes, bboxes):
        (x, y, w, h) = bbox
        cls_name = model.names[cls_idx]
        if cls_name == 'tv':
            continue

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
